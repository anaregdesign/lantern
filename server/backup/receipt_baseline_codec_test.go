package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func TestReceiptBaselineCodecCanonicalRoundTrip(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	codec := ReceiptBaselineCodec{}
	capture := producerCapture(archive)
	capture.Retired = producerRetiredSnapshot(
		t,
		archive.Policy,
		archive.Receipts.ClockHighWaterMillis,
		0x72,
	)
	raw, err := codec.EncodeCombinedReceiptBaseline(context.Background(), capture)
	if err != nil {
		t.Fatal(err)
	}
	if got := string(raw[:8]); got != "LANTCBLN" {
		t.Fatalf("combined baseline magic = %q", got)
	}
	if got := binary.BigEndian.Uint16(raw[8:10]); got != 1 {
		t.Fatalf("combined baseline version = %d, want 1", got)
	}
	active, retired, metadata, err := decodeCombinedReceiptBaseline(raw)
	if err != nil {
		t.Fatal(err)
	}
	if want := encodedWholeStateArchive(t, archive); !bytes.Equal(active, want) {
		t.Fatal("combined baseline changed the canonical active archive member")
	}
	if !reflect.DeepEqual(retired, capture.Retired) {
		t.Fatalf("combined baseline retired state = %+v, want %+v", retired, capture.Retired)
	}
	if metadata.ActiveEpoch != archive.Policy.Epoch ||
		metadata.MaxEntries != archive.Policy.MaxEntries ||
		metadata.MaxBytes != archive.Policy.MaxBytes ||
		metadata.ClockHighWaterMillis != archive.Receipts.ClockHighWaterMillis {
		t.Fatalf("combined baseline retired metadata = %+v", metadata)
	}
	stage, err := codec.StageCombinedReceiptBaseline(
		context.Background(),
		raw,
		archive.Policy,
		time.Hour,
		nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	if stage.CutoffLocalSeq != archive.Graph[0].GetHeader().GetCutoffLocalSeq() ||
		stage.CutoffHLC != archive.Origins[0].LastHLC ||
		stage.Receipts.PolicyFingerprint() != archive.Receipts.PolicyFingerprint ||
		!reflect.DeepEqual(stage.Retired, capture.Retired) {
		t.Fatalf("staged baseline provenance = %+v", stage)
	}
	if got, _, ok := stage.Graph.GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("staged baseline edge = %v, %t", got, ok)
	}
}

func TestReceiptBaselineCodecRejectsCanonicalRetiredStateFromDifferentCut(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	differentHighWater := archive.Receipts.ClockHighWaterMillis + 1
	for _, tc := range []struct {
		name  string
		state mutationreceipt.RetiredCatalogSnapshot
	}{
		{
			name:  "empty",
			state: producerEmptyRetiredSnapshot(t, archive.Policy, differentHighWater),
		},
		{
			name: "nonempty",
			state: producerRetiredSnapshot(
				t,
				archive.Policy,
				differentHighWater,
				0x74,
			),
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := encodeRetiredCatalogArchive(archive.Policy, tc.state); err != nil {
				t.Fatalf("retired fixture is not internally canonical: %v", err)
			}
			capture := producerCapture(archive)
			capture.Retired = tc.state
			raw, err := (ReceiptBaselineCodec{}).EncodeCombinedReceiptBaseline(
				t.Context(),
				capture,
			)
			if raw != nil || !errors.Is(err, errReceiptCombinedBaseline) {
				t.Fatalf("cross-cut retired state encoded %d bytes: %v", len(raw), err)
			}
		})
	}
}

func TestReceiptBaselineCodecRejectsNoncanonicalAndMismatchedPolicy(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	raw := encodedWholeStateArchive(t, archive)
	firstRecord := wholeStateArchiveHeaderSize
	payloadSize := int(binary.BigEndian.Uint32(raw[firstRecord+1 : firstRecord+5]))
	payloadStart := firstRecord + 5
	payload := raw[payloadStart : payloadStart+payloadSize]
	number, wireType, tagSize := protowire.ConsumeTag(payload)
	header, valueSize := protowire.ConsumeBytes(payload[tagSize:])
	if number != 1 || wireType != protowire.BytesType || valueSize < 0 || tagSize+valueSize != len(payload) {
		t.Fatalf("unexpected encoded Snapshot header: %d %v %d", number, wireType, valueSize)
	}

	t.Run("corruption bounds and uncombined input", func(t *testing.T) {
		archive := wholeStateArchiveFixture(t)
		capture := producerCapture(archive)
		capture.Retired = producerRetiredSnapshot(
			t,
			archive.Policy,
			archive.Receipts.ClockHighWaterMillis,
			0x73,
		)
		codec := ReceiptBaselineCodec{}
		raw, err := codec.EncodeCombinedReceiptBaseline(t.Context(), capture)
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			name   string
			mutate func([]byte) []byte
		}{
			{
				name: "magic",
				mutate: func(raw []byte) []byte {
					raw[0] ^= 1
					return raw
				},
			},
			{
				name: "retired magic",
				mutate: func(raw []byte) []byte {
					copy(raw[:8], "LANT"+"BLN2")
					return raw
				},
			},
			{
				name: "retired version",
				mutate: func(raw []byte) []byte {
					binary.BigEndian.PutUint16(raw[8:10], 2)
					return raw
				},
			},
			{
				name: "section digest",
				mutate: func(raw []byte) []byte {
					raw[32] ^= 1
					return raw
				},
			},
			{
				name: "overall digest",
				mutate: func(raw []byte) []byte {
					raw[len(raw)-1] ^= 1
					return raw
				},
			},
			{
				name: "oversized active section",
				mutate: func(raw []byte) []byte {
					binary.BigEndian.PutUint64(raw[16:24], uint64(wholeStateArchiveMaxBytes)+1)
					return raw
				},
			},
			{
				name: "trailing byte",
				mutate: func(raw []byte) []byte {
					return append(raw, 0)
				},
			},
			{
				name: "retired epoch count allocation bomb",
				mutate: func(raw []byte) []byte {
					return mutateCombinedRetiredSection(t, raw, func(retired []byte) {
						binary.BigEndian.PutUint64(retired[40:48], uint64(^uint32(0)))
						binary.BigEndian.PutUint32(retired[56:60], ^uint32(0))
						footer := len(retired) - 5 - retiredCatalogArchiveFooterSize
						binary.BigEndian.PutUint64(retired[footer+5:footer+13], uint64(^uint32(0)))
					})
				},
			},
			{
				name: "retired receipt count allocation bomb",
				mutate: func(raw []byte) []byte {
					return mutateCombinedRetiredSection(t, raw, func(retired []byte) {
						const huge = uint64(^uint32(0))
						binary.BigEndian.PutUint64(retired[40:48], huge)
						binary.BigEndian.PutUint64(retired[60:68], huge)
						firstEpochPayload := retiredCatalogArchiveHeaderSize + 5
						binary.BigEndian.PutUint64(
							retired[firstEpochPayload+72:firstEpochPayload+80],
							huge,
						)
						footer := len(retired) - 5 - retiredCatalogArchiveFooterSize
						binary.BigEndian.PutUint64(retired[footer+13:footer+21], huge)
					})
				},
			},
			{
				name: "retired active epoch differs",
				mutate: func(raw []byte) []byte {
					return mutateCombinedRetiredSection(t, raw, func(retired []byte) {
						retired[31] ^= 1
					})
				},
			},
			{
				name: "retired aggregate entry cap differs",
				mutate: func(raw []byte) []byte {
					return mutateCombinedRetiredSection(t, raw, func(retired []byte) {
						binary.BigEndian.PutUint64(
							retired[40:48],
							binary.BigEndian.Uint64(retired[40:48])+1,
						)
					})
				},
			},
			{
				name: "retired aggregate byte cap differs",
				mutate: func(raw []byte) []byte {
					return mutateCombinedRetiredSection(t, raw, func(retired []byte) {
						binary.BigEndian.PutUint64(
							retired[48:56],
							binary.BigEndian.Uint64(retired[48:56])+1,
						)
					})
				},
			},
			{
				name: "retired high-water differs",
				mutate: func(raw []byte) []byte {
					return mutateCombinedRetiredSection(t, raw, func(retired []byte) {
						binary.BigEndian.PutUint64(
							retired[32:40],
							binary.BigEndian.Uint64(retired[32:40])+1,
						)
					})
				},
			},
		} {
			t.Run(tc.name, func(t *testing.T) {
				damaged := tc.mutate(bytes.Clone(raw))
				if candidate, err := codec.StageCombinedReceiptBaseline(
					t.Context(),
					damaged,
					archive.Policy,
					time.Hour,
					nil,
				); candidate != nil || !errors.Is(err, errReceiptCombinedBaseline) {
					t.Fatalf("damaged combined baseline staged: candidate=%+v err=%v", candidate, err)
				}
			})
		}

		activeOnly := encodedWholeStateArchive(t, archive)
		if candidate, err := codec.StageCombinedReceiptBaseline(
			t.Context(),
			activeOnly,
			archive.Policy,
			time.Hour,
			nil,
		); candidate != nil || !errors.Is(err, errReceiptCombinedBaseline) {
			t.Fatalf("active-only private baseline staged: candidate=%+v err=%v", candidate, err)
		}

		highWaterMismatch := capture
		highWaterMismatch.Retired.ClockHighWaterMillis++
		if raw, err := codec.EncodeCombinedReceiptBaseline(t.Context(), highWaterMismatch); raw != nil || err == nil {
			t.Fatalf("retired high-water mismatch encoded: %d bytes, %v", len(raw), err)
		}

		overCapacity := capture
		overCapacity.Retired = mutationreceipt.RetiredCatalogSnapshot{
			Version:              1,
			ClockHighWaterMillis: archive.Receipts.ClockHighWaterMillis,
		}
		for seed := byte(0x74); len(overCapacity.Retired.Epochs) <= archive.Policy.MaxEntries; seed++ {
			state := producerRetiredSnapshot(
				t,
				archive.Policy,
				archive.Receipts.ClockHighWaterMillis,
				seed,
			)
			overCapacity.Retired.Epochs = append(overCapacity.Retired.Epochs, state.Epochs[0])
		}
		if raw, err := codec.EncodeCombinedReceiptBaseline(t.Context(), overCapacity); raw != nil ||
			!errors.Is(err, mutationreceipt.ErrRetiredCatalogCapacity) {
			t.Fatalf("over-capacity retired catalog encoded: %d bytes, %v", len(raw), err)
		}
	})
	var fields [][]byte
	for len(header) != 0 {
		fieldNumber, fieldType, n := protowire.ConsumeTag(header)
		m := protowire.ConsumeFieldValue(fieldNumber, fieldType, header[n:])
		if n < 0 || m < 0 {
			t.Fatal("invalid canonical header fixture")
		}
		fields = append(fields, append([]byte(nil), header[:n+m]...))
		header = header[n+m:]
	}
	var reorderedHeader []byte
	for i := len(fields) - 1; i >= 0; i-- {
		reorderedHeader = append(reorderedHeader, fields[i]...)
	}
	var reorderedPayload []byte
	reorderedPayload = protowire.AppendTag(reorderedPayload, number, protowire.BytesType)
	reorderedPayload = protowire.AppendBytes(reorderedPayload, reorderedHeader)
	noncanonical := append([]byte(nil), raw[:payloadStart]...)
	noncanonical = append(noncanonical, reorderedPayload...)
	noncanonical = append(noncanonical, raw[payloadStart+payloadSize:]...)
	binary.BigEndian.PutUint32(noncanonical[firstRecord+1:firstRecord+5], uint32(len(reorderedPayload)))
	footer := len(noncanonical) - 5 - wholeStateArchiveFooterSize
	digest := sha256.Sum256(noncanonical[:footer])
	copy(noncanonical[footer+5+24:], digest[:])
	if _, err := decodeWholeStateArchive(bytes.NewReader(noncanonical)); err != nil {
		t.Fatalf("test noncanonical archive is not semantically valid: %v", err)
	}
	retiredRaw, err := encodeRetiredCatalogArchive(archive.Policy, producerCapture(archive).Retired)
	if err != nil {
		t.Fatal(err)
	}
	noncanonicalCombined, err := encodeCombinedReceiptBaseline(noncanonical, retiredRaw)
	if err != nil {
		t.Fatal(err)
	}
	canonicalCombined, err := encodeCombinedReceiptBaseline(raw, retiredRaw)
	if err != nil {
		t.Fatal(err)
	}
	codec := ReceiptBaselineCodec{}
	if stage, err := codec.StageCombinedReceiptBaseline(
		context.Background(),
		noncanonicalCombined,
		archive.Policy,
		time.Hour,
		nil,
	); stage != nil ||
		!errors.Is(err, errWholeStateArchive) {
		t.Fatalf("noncanonical archive staged: %+v, %v", stage, err)
	}
	wrong := archive.Policy
	wrong.MaxEntries++
	if stage, err := codec.StageCombinedReceiptBaseline(
		context.Background(),
		canonicalCombined,
		wrong,
		time.Hour,
		nil,
	); stage != nil ||
		!errors.Is(err, errWholeStateArchive) {
		t.Fatalf("mismatched policy staged: %+v, %v", stage, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if raw, err := codec.EncodeCombinedReceiptBaseline(canceled, service.ReceiptWholeStateCapture{}); raw != nil ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("canceled encode = %d bytes, %v", len(raw), err)
	}
}

func mutateCombinedRetiredSection(
	t testing.TB,
	raw []byte,
	mutate func([]byte),
) []byte {
	t.Helper()
	damaged := bytes.Clone(raw)
	activeSize := binary.BigEndian.Uint64(damaged[16:24])
	retiredSize := binary.BigEndian.Uint64(damaged[24:32])
	retiredStart := receiptCombinedBaselineHeaderSize + int(activeSize)
	retiredEnd := retiredStart + int(retiredSize)
	if retiredStart < receiptCombinedBaselineHeaderSize ||
		retiredEnd > len(damaged)-receiptCombinedBaselineFooterSize {
		t.Fatal("invalid combined baseline fixture")
	}
	retired := damaged[retiredStart:retiredEnd]
	mutate(retired)
	footer := len(retired) - 5 - retiredCatalogArchiveFooterSize
	if footer < retiredCatalogArchiveHeaderSize || retired[footer] != retiredCatalogFooterRecord {
		t.Fatal("invalid retired baseline fixture")
	}
	retiredDigest := sha256.Sum256(retired[:footer])
	copy(retired[footer+5+16:], retiredDigest[:])
	sectionDigest := sha256.Sum256(retired)
	copy(damaged[64:96], sectionDigest[:])
	containerDigest := sha256.Sum256(damaged[:len(damaged)-receiptCombinedBaselineFooterSize])
	copy(damaged[len(damaged)-receiptCombinedBaselineFooterSize:], containerDigest[:])
	return damaged
}

func TestReceiptBaselineCodecDurableRuntimeInstallRestartAndSuffixReplay(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	capture := producerCapture(archive)
	capture.Retired = producerRetiredSnapshot(
		t,
		archive.Policy,
		archive.Receipts.ClockHighWaterMillis,
		0x75,
	)
	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := service.DurableReceiptWALRuntimeConfig{
		Path:       path,
		Receipt:    archive.Policy,
		Log:        mutationlog.Options{Capacity: 8, SubscriberBuffer: 2},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			graph.EnablePrefixIndex(func(key string) string { return key })
			graph.EnableSearchIndex(
				func(key string, _ *pb.Vertex) search.Document { return search.Text(key) },
				strings.Compare,
			)
			return nil
		},
		NodeID:        hlc.NodeID{0x44},
		Now:           archive.Policy.ClockHighWater,
		BaselineCodec: ReceiptBaselineCodec{},
	}
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	graphIdentity := runtime.GraphCache()
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replication, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replication); err != nil {
		t.Fatal(err)
	}
	if err := primary.InstallReceiptBaseline(context.Background(), capture); err != nil {
		t.Fatal(err)
	}
	if runtime.GraphCache() != graphIdentity {
		t.Fatal("baseline install replaced the GraphCache object")
	}
	if got, _, ok := graphIdentity.GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("installed baseline edge = %v, %t", got, ok)
	}
	if length, _, evicted := runtime.MutationLogStats(); length != 0 || evicted != 1 {
		t.Fatalf("post-marker log = len %d evicted %d, want 0 and 1", length, evicted)
	}
	if _, err := primary.PutVertex(context.Background(), &pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: "suffix-searchable", Expiration: timestamppb.New(time.Now().Add(time.Hour))},
	}); err != nil {
		t.Fatal(err)
	}
	if length, _, evicted := runtime.MutationLogStats(); length != 1 || evicted != 1 {
		t.Fatalf("post-suffix log = len %d evicted %d, want 1 and 1", length, evicted)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}

	config.Now = archive.Policy.ClockHighWater
	restarted, err := service.OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = restarted.Close() })
	restartedPrimary := restarted.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	restartedReplication, err := restarted.NewLanternReplicationService(restartedPrimary)
	if err != nil {
		t.Fatal(err)
	}
	if err := restarted.CertifyInstallation(restartedPrimary, restartedReplication); err != nil {
		t.Fatal(err)
	}
	source, policy, err := restarted.ReceiptWholeStateBackupSource(
		restartedPrimary,
		restartedReplication,
	)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := source.Capture(t.Context(), policy)
	if err != nil {
		t.Fatal(err)
	}
	wantRetired := capture.Retired
	wantRetired.ClockHighWaterMillis = recovered.Receipts.ClockHighWaterMillis
	for i := range wantRetired.Epochs {
		wantRetired.Epochs[i].State.ClockHighWaterMillis = recovered.Receipts.ClockHighWaterMillis
	}
	if recovered.Retired.ClockHighWaterMillis != recovered.Receipts.ClockHighWaterMillis ||
		!reflect.DeepEqual(recovered.Retired, wantRetired) {
		t.Fatalf("restarted retired catalog = %+v, want %+v", recovered.Retired, wantRetired)
	}
	if got, _, ok := restarted.GraphCache().GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("restarted baseline edge = %v, %t", got, ok)
	}
	if vertex, ok := restarted.GraphCache().GetVertex("suffix-searchable"); !ok || vertex.GetKey() != "suffix-searchable" {
		t.Fatalf("suffix replay vertex = %+v, %t", vertex, ok)
	}
	hits := restarted.GraphCache().SearchVertices("suffix", 10, "")
	if len(hits) == 0 || hits[0].ID != "suffix-searchable" {
		t.Fatalf("rebuilt suffix search index = %+v", hits)
	}
	if length, _, evicted := restarted.MutationLogStats(); length != 1 || evicted != 1 {
		t.Fatalf("restarted suffix log = len %d evicted %d, want 1 and 1", length, evicted)
	}
}

func TestReceiptBaselineCodecRestartReapsNaturallyExpiredTombstone(t *testing.T) {
	issued := time.Now().Truncate(time.Millisecond)
	expiration := issued.Add(time.Second)
	archive := wholeStateArchiveFixtureAt(t, issued)
	stamp := archive.Graph[0].GetHeader().GetCutoffHlc()
	archive.Graph = append(
		archive.Graph[:1],
		append([]*pb.SnapshotResponse{{
			Entry: &pb.SnapshotResponse_EdgeTombstone{
				EdgeTombstone: &pb.SnapshotEdgeTombstone{
					Tail: "expired", Head: "floor",
					Hlc: stamp, Expiration: timestamppb.New(expiration),
				},
			},
		}}, archive.Graph[1:]...)...,
	)
	archive.Graph[len(archive.Graph)-1].GetFooter().EdgeTombstoneCount = 1

	path := filepath.Join(t.TempDir(), "receipts.wal")
	config := service.DurableReceiptWALRuntimeConfig{
		Path: path, Receipt: archive.Policy,
		Log:        mutationlog.Options{Capacity: 8, SubscriberBuffer: 2},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			graph.EnablePrefixIndex(func(key string) string { return key })
			return nil
		},
		NodeID: hlc.NodeID{0x44}, Now: issued, BaselineCodec: ReceiptBaselineCodec{},
	}
	runtime, err := service.CreateDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	primary := runtime.NewLanternService(nil)
	if err := primary.InstallReceiptBaseline(context.Background(), producerCapture(archive)); err != nil {
		t.Fatal(err)
	}
	if got := runtime.GraphCache().SnapshotReplication().Tombstones.Edges; len(got) != 1 {
		t.Fatalf("live installed tombstone = %+v", got)
	}
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if wait := time.Until(expiration.Add(20 * time.Millisecond)); wait > 0 {
		time.Sleep(wait)
	}

	config.Now = expiration.Add(time.Second)
	restarted, err := service.OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatal(err)
	}
	defer restarted.Close()
	if got := restarted.GraphCache().SnapshotReplication().Tombstones.Edges; len(got) != 0 {
		t.Fatalf("restart resurrected expired tombstone: %+v", got)
	}
	if _, _, ok := restarted.GraphCache().GetEdgeDetail("expired", "floor"); ok {
		t.Fatal("restart resurrected an edge behind the expired tombstone")
	}
}
