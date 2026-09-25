package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

func TestReceiptBaselineCodecCanonicalRoundTrip(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	codec := ReceiptBaselineCodec{}
	raw, err := codec.EncodeReceiptBaseline(context.Background(), producerCapture(archive))
	if err != nil {
		t.Fatal(err)
	}
	if want := encodedWholeStateArchive(t, archive); !bytes.Equal(raw, want) {
		t.Fatal("baseline adapter diverged from canonical archive encoder")
	}
	stage, err := codec.StageReceiptBaseline(context.Background(), raw, archive.Policy, time.Hour, nil)
	if err != nil {
		t.Fatal(err)
	}
	if stage.CutoffLocalSeq != archive.Graph[0].GetHeader().GetCutoffLocalSeq() ||
		stage.CutoffHLC != archive.Origins[0].LastHLC ||
		stage.Receipts.PolicyFingerprint() != archive.Receipts.PolicyFingerprint {
		t.Fatalf("staged baseline provenance = %+v", stage)
	}
	if got, _, ok := stage.Graph.GetEdgeDetail("tail", "head"); !ok || got != 1.5 {
		t.Fatalf("staged baseline edge = %v, %t", got, ok)
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
	codec := ReceiptBaselineCodec{}
	if stage, err := codec.StageReceiptBaseline(context.Background(), noncanonical, archive.Policy, time.Hour, nil); stage != nil ||
		!errors.Is(err, errWholeStateArchive) {
		t.Fatalf("noncanonical archive staged: %+v, %v", stage, err)
	}
	wrong := archive.Policy
	wrong.MaxEntries++
	if stage, err := codec.StageReceiptBaseline(context.Background(), raw, wrong, time.Hour, nil); stage != nil ||
		!errors.Is(err, errWholeStateArchive) {
		t.Fatalf("mismatched policy staged: %+v, %v", stage, err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if raw, err := codec.EncodeReceiptBaseline(canceled, service.ReceiptWholeStateCapture{}); raw != nil ||
		!errors.Is(err, context.Canceled) {
		t.Fatalf("canceled encode = %d bytes, %v", len(raw), err)
	}
}

func TestReceiptBaselineCodecDurableRuntimeInstallRestartAndSuffixReplay(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
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
	if err := primary.InstallReceiptBaseline(context.Background(), producerCapture(archive)); err != nil {
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
