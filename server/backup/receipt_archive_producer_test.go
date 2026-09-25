package backup

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/service"
)

type receiptArchiveWALFunc func(mutationlog.Entry) error

func (f receiptArchiveWALFunc) Write(entry mutationlog.Entry) error { return f(entry) }

type receiptWholeStateBackupCaptureFunc func(
	context.Context,
	mutationreceipt.Config,
) (service.ReceiptWholeStateBackupCapture, error)

func (f receiptWholeStateBackupCaptureFunc) CaptureForBackup(
	ctx context.Context,
	policy mutationreceipt.Config,
) (service.ReceiptWholeStateBackupCapture, error) {
	return f(ctx, policy)
}

type receiptWholeStateSourceSpy struct {
	backupCalls  int
	captureCalls int
	capture      service.ReceiptWholeStateBackupCapture
}

func (s *receiptWholeStateSourceSpy) Capture(
	context.Context,
	mutationreceipt.Config,
) (service.ReceiptWholeStateCapture, error) {
	s.captureCalls++
	return service.ReceiptWholeStateCapture{}, errors.New("ordinary Capture must not be called")
}

func (s *receiptWholeStateSourceSpy) CaptureForBackup(
	_ context.Context,
	policy mutationreceipt.Config,
) (service.ReceiptWholeStateBackupCapture, error) {
	s.backupCalls++
	if policy != s.capture.WholeState.Policy || s.backupCalls != 1 {
		return service.ReceiptWholeStateBackupCapture{}, errors.New("unexpected second source read or policy")
	}
	return s.capture, nil
}

func decodedProducerArchive(t *testing.T, raw []byte) wholeStateArchive {
	t.Helper()
	got, err := decodeWholeStateArchive(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReceiptArchiveProducerUsesOneCombinedDetachedCut(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	capture := producerBackupCapture(a)
	capture.WholeState.Retired = producerRetiredSnapshot(
		t,
		a.Policy,
		a.Receipts.ClockHighWaterMillis,
		0x76,
	)
	source := &receiptWholeStateSourceSpy{capture: capture}
	product, err := produceReceiptWholeStateArchive(context.Background(), source, a.Policy)
	if err != nil || source.backupCalls != 1 || source.captureCalls != 0 {
		t.Fatalf("producer = %v, backup calls=%d, ordinary calls=%d", err, source.backupCalls, source.captureCalls)
	}
	raw, walCutRaw, retiredRaw := product.bytes()
	got := decodedProducerArchive(t, raw)
	if !reflect.DeepEqual(got.Receipts, a.Receipts) || !reflect.DeepEqual(got.Origins, a.Origins) || got.Policy != a.Policy || len(got.Graph) != len(a.Graph) {
		t.Fatalf("detached archive lost receipt, origin, policy, or graph section: %+v", got)
	}
	for i := range a.Graph {
		if !proto.Equal(got.Graph[i], a.Graph[i]) {
			t.Fatalf("graph frame %d changed during composition", i)
		}
	}
	bound, err := decodeReceiptArchiveWALCut(walCutRaw)
	if err != nil {
		t.Fatal(err)
	}
	witness := source.capture.WALTip
	if bound.archiveSHA256 != sha256.Sum256(raw) ||
		bound.cutSeq != witness.Seq || bound.cutOffset != witness.Offset ||
		bound.cutSHA256 != witness.SHA256 || bound.cutChainSHA256 != witness.ChainSHA256 ||
		bound.tipSeq != witness.Seq || bound.tipOffset != witness.Offset ||
		bound.tipSHA256 != witness.SHA256 || bound.tipChainSHA256 != witness.ChainSHA256 {
		t.Fatalf("manifest = %+v, want captured witness %+v at both cut and tip", bound, witness)
	}
	retired, _, err := decodeRetiredCatalogArchive(retiredRaw)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(retired, source.capture.WholeState.Retired) {
		t.Fatalf("retired catalog = %+v, want %+v", retired, source.capture.WholeState.Retired)
	}
	repeated, err := produceReceiptWholeStateArchive(
		context.Background(),
		receiptWholeStateBackupCaptureFunc(func(
			context.Context,
			mutationreceipt.Config,
		) (service.ReceiptWholeStateBackupCapture, error) {
			return capture, nil
		}),
		a.Policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	repeatedArchive, repeatedWALCut, repeatedRetired := repeated.bytes()
	if !bytes.Equal(repeatedArchive, raw) ||
		!bytes.Equal(repeatedWALCut, walCutRaw) ||
		!bytes.Equal(repeatedRetired, retiredRaw) {
		t.Fatal("identical one-cut capture produced nondeterministic member bytes")
	}
	if product.nodeID != source.capture.NodeID || product.generation != source.capture.Generation {
		t.Fatalf("product identity = %x/%x, want %x/%x",
			product.nodeID, product.generation, source.capture.NodeID, source.capture.Generation)
	}
	// Mutating the source after its only read cannot change the returned bytes.
	a.Graph[1].GetVertex().Vertex.Key = "changed"
	a.Receipts.Receipts[0].Result[0] = 0
	a.Origins[0].LastSeq++
	archiveCopy, walCutCopy, retiredCopy := product.bytes()
	again := decodedProducerArchive(t, archiveCopy)
	if !proto.Equal(again.Graph[1], got.Graph[1]) || !reflect.DeepEqual(again.Receipts, got.Receipts) ||
		!reflect.DeepEqual(again.Origins, got.Origins) {
		t.Fatal("archive bytes changed with later source mutation")
	}
	raw[0] ^= 1
	walCutRaw[0] ^= 1
	retiredRaw[0] ^= 1
	ownedArchive, ownedWALCut, ownedRetired := product.bytes()
	if !bytes.Equal(archiveCopy, ownedArchive) ||
		!bytes.Equal(walCutCopy, ownedWALCut) ||
		!bytes.Equal(retiredCopy, ownedRetired) {
		t.Fatal("caller mutation changed the immutable archive product")
	}
	raw = ownedArchive
	if _, err := decodeWholeStateArchive(bytes.NewReader(raw[:len(raw)-1])); !errors.Is(err, errWholeStateArchive) {
		t.Fatalf("truncated archive accepted: %v", err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[wholeStateArchiveHeaderSize+8] ^= 1
	if _, err := decodeWholeStateArchive(bytes.NewReader(tampered)); !errors.Is(err, errWholeStateArchive) {
		t.Fatalf("tampered archive accepted: %v", err)
	}
}

func TestReceiptArchiveProducerFailsWithoutPartialProduct(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	for _, tc := range []struct {
		name   string
		source ReceiptSource
	}{
		{"nil source", nil},
		{"capture failure", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			return producerBackupCapture(a), errors.New("capture failed after partial work")
		})},
		{"zero capture", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			return service.ReceiptWholeStateBackupCapture{}, nil
		})},
		{"missing witness", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.WALTip = mutationlog.FileWALTipWitness{}
			return bad, nil
		})},
		{"missing node ID", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.NodeID = hlc.NodeID{}
			return bad, nil
		})},
		{"missing generation", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.Generation = [16]byte{}
			return bad, nil
		})},
		{"node ID mismatch", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.NodeID[0] ^= 0xff
			return bad, nil
		})},
		{"missing whole state", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			return service.ReceiptWholeStateBackupCapture{WALTip: producerWALTipWitness(0)}, nil
		})},
		{"invalid policy", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.WholeState.Policy.MaxEntries = 0
			return bad, nil
		})},
		{"zero retired catalog", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.WholeState.Retired = mutationreceipt.RetiredCatalogSnapshot{}
			return bad, nil
		})},
		{"lower retired high-water", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.WholeState.Retired = producerEmptyRetiredSnapshot(
				t,
				bad.WholeState.Policy,
				bad.WholeState.Receipts.ClockHighWaterMillis-1,
			)
			return bad, nil
		})},
		{"invalid graph", receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
			bad := producerBackupCapture(wholeStateArchiveFixture(t))
			bad.WholeState.Graph[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
			return bad, nil
		})},
	} {
		t.Run(tc.name, func(t *testing.T) {
			product, err := produceReceiptWholeStateArchive(context.Background(), tc.source, a.Policy)
			if err == nil || product != (receiptBackupSetProduct{}) {
				t.Fatalf("failed producer returned product: %#v, %v", product, err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	product, err := produceReceiptWholeStateArchive(canceled, receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
		calls++
		return producerBackupCapture(a), nil
	}), a.Policy)
	if !errors.Is(err, context.Canceled) || product != (receiptBackupSetProduct{}) || calls != 0 {
		t.Fatalf("canceled producer = %#v, %v, %d source calls", product, err, calls)
	}
	ctx, cancelDuringCapture := context.WithCancel(context.Background())
	product, err = produceReceiptWholeStateArchive(ctx, receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
		cancelDuringCapture()
		return producerBackupCapture(a), nil
	}), a.Policy)
	if !errors.Is(err, context.Canceled) || product != (receiptBackupSetProduct{}) {
		t.Fatalf("producer canceled after capture = %#v, %v", product, err)
	}
	f := newReceiptArchiveFixture(t, nil)
	wrongPolicy := f.policy
	wrongPolicy.Retention = 2 * time.Hour
	product, err = produceReceiptWholeStateArchive(context.Background(), f.backupSource, wrongPolicy)
	if !errors.Is(err, mutationreceipt.ErrInvalidSnapshot) || product != (receiptBackupSetProduct{}) {
		t.Fatalf("live capture policy mismatch = %#v, %v", product, err)
	}
}

func TestReceiptArchiveProducerRejectsMiswiredSourcePolicy(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*mutationreceipt.Config)
	}{
		{"epoch", func(p *mutationreceipt.Config) { p.Epoch = mutationreceipt.Epoch{0x73} }},
		{"retention", func(p *mutationreceipt.Config) { p.Retention = 2 * time.Hour }},
		{"entry cap", func(p *mutationreceipt.Config) { p.MaxEntries++ }},
		{"byte cap", func(p *mutationreceipt.Config) { p.MaxBytes++ }},
		{"high-water lower bound", func(p *mutationreceipt.Config) { p.ClockHighWater = a.Policy.ClockHighWater.Add(time.Hour) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requested := a.Policy
			tc.change(&requested)
			calls := 0
			product, err := produceReceiptWholeStateArchive(context.Background(), receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
				calls++
				return producerBackupCapture(a), nil
			}), requested)
			if !errors.Is(err, errWholeStateArchive) || product != (receiptBackupSetProduct{}) || calls != 1 {
				t.Fatalf("miswired source returned %#v after %d reads: %v", product, calls, err)
			}
		})
	}
}

func TestReceiptArchiveProducerRejectsInvalidCapturedWitness(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	for _, tc := range []struct {
		name   string
		change func(*service.ReceiptWholeStateBackupCapture)
	}{
		{"archive sequence mismatch", func(c *service.ReceiptWholeStateBackupCapture) {
			c.WALTip.Seq++
			c.WALTip.Offset++
		}},
		{"offset below header", func(c *service.ReceiptWholeStateBackupCapture) {
			c.WALTip.Offset = receiptArchiveWALZeroOffset - 1
		}},
		{"zero sequence with framed offset", func(c *service.ReceiptWholeStateBackupCapture) {
			c.WALTip.Seq = 0
		}},
		{"nonzero sequence at header offset", func(c *service.ReceiptWholeStateBackupCapture) {
			c.WALTip.Offset = receiptArchiveWALZeroOffset
		}},
		{"zero prefix digest", func(c *service.ReceiptWholeStateBackupCapture) {
			c.WALTip.SHA256 = [sha256.Size]byte{}
		}},
		{"zero chain digest", func(c *service.ReceiptWholeStateBackupCapture) {
			c.WALTip.ChainSHA256 = [sha256.Size]byte{}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := producerBackupCapture(archive)
			tc.change(&capture)
			product, err := produceReceiptWholeStateArchive(
				context.Background(),
				receiptWholeStateBackupCaptureFunc(func(
					context.Context,
					mutationreceipt.Config,
				) (service.ReceiptWholeStateBackupCapture, error) {
					return capture, nil
				}),
				archive.Policy,
			)
			if !errors.Is(err, errReceiptArchiveWALCut) ||
				product != (receiptBackupSetProduct{}) {
				t.Fatalf("invalid witness produced %#v: %v", product, err)
			}
		})
	}
}

func TestReceiptArchiveProducerDoesNotReopenMissingWALPath(t *testing.T) {
	archive := wholeStateArchiveFixture(t)
	missingPath := filepath.Join(t.TempDir(), "missing", "receipt.wal")
	source := receiptWholeStateBackupCaptureFunc(func(
		context.Context,
		mutationreceipt.Config,
	) (service.ReceiptWholeStateBackupCapture, error) {
		if _, err := os.Stat(missingPath); !errors.Is(err, os.ErrNotExist) {
			return service.ReceiptWholeStateBackupCapture{}, errors.New("test WAL path unexpectedly exists")
		}
		return producerBackupCapture(archive), nil
	})
	product, err := produceReceiptWholeStateArchive(context.Background(), source, archive.Policy)
	if err != nil {
		t.Fatalf("captured-witness production inspected missing WAL path: %v", err)
	}
	raw, walCutRaw, retiredRaw := product.bytes()
	if len(raw) == 0 || len(walCutRaw) != receiptArchiveWALCutSize || len(retiredRaw) == 0 {
		t.Fatalf("captured-witness product sizes = %d, %d, %d", len(raw), len(walCutRaw), len(retiredRaw))
	}
	if _, err := bindReceiptArchiveFileWAL(
		raw,
		missingPath,
		archiveWALStringDecode,
		archiveWALValidEntry,
	); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("stable-path binder against missing WAL = %v, want not-exist", err)
	}
}

func TestReceiptArchiveProducerManifestMatchesClosedFileWALCut(t *testing.T) {
	archiveRaw, path := archiveWALFixture(t, 2, 2)
	archive := decodedProducerArchive(t, archiveRaw)
	cut, err := mutationlog.InspectFileWALCut(
		path,
		2,
		archiveWALStringDecode,
		archiveWALValidEntry,
	)
	if err != nil {
		t.Fatal(err)
	}
	witness := mutationlog.FileWALTipWitness{
		Seq:         cut.Seq,
		Offset:      cut.Offset,
		SHA256:      cut.SHA256,
		ChainSHA256: cut.ChainSHA256,
	}
	capture := producerBackupCapture(archive)
	capture.WALTip = witness
	product, err := produceReceiptWholeStateArchive(
		context.Background(),
		receiptWholeStateBackupCaptureFunc(func(
			context.Context,
			mutationreceipt.Config,
		) (service.ReceiptWholeStateBackupCapture, error) {
			return capture, nil
		}),
		archive.Policy,
	)
	if err != nil {
		t.Fatal(err)
	}
	gotArchive, manifestRaw, retiredRaw := product.bytes()
	if !bytes.Equal(gotArchive, archiveRaw) {
		t.Fatal("producer archive differs from the canonical captured state")
	}
	manifest, err := decodeReceiptArchiveWALCut(manifestRaw)
	if err != nil {
		t.Fatal(err)
	}
	if manifest.cutSeq != cut.Seq || manifest.cutOffset != cut.Offset ||
		manifest.cutSHA256 != cut.SHA256 || manifest.cutChainSHA256 != cut.ChainSHA256 ||
		manifest.tipSeq != cut.ObservedLast || manifest.tipOffset != cut.ObservedOffset ||
		manifest.tipSHA256 != cut.ObservedSHA256 ||
		manifest.tipChainSHA256 != cut.ObservedChainSHA256 {
		t.Fatalf("producer manifest = %+v, want closed FileWAL cut %+v", manifest, cut)
	}
	if _, _, err := decodeRetiredCatalogArchive(retiredRaw); err != nil {
		t.Fatalf("decode retired catalog: %v", err)
	}
}

func TestReceiptArchiveProducerRejectsOversizeFrame(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	a.Graph[1].GetVertex().Vertex.Value = &pb.Vertex_String_{String_: strings.Repeat("v", wholeStateArchiveMaxFrame)}
	product, err := produceReceiptWholeStateArchive(context.Background(), receiptWholeStateBackupCaptureFunc(func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateBackupCapture, error) {
		return producerBackupCapture(a), nil
	}), a.Policy)
	if !errors.Is(err, errWholeStateArchive) || product != (receiptBackupSetProduct{}) {
		t.Fatalf("oversize frame produced %#v: %v", product, err)
	}
}

type receiptArchiveFixture struct {
	cache        *graphcache.GraphCache[string, *pb.Vertex]
	log          *mutationlog.Log
	service      *service.LanternService
	source       *service.ReceiptWholeStateSource
	backupSource ReceiptSource
	policy       mutationreceipt.Config
	clock        *hlc.Clock
}

func newReceiptArchiveFixture(t *testing.T, wal mutationlog.WAL) receiptArchiveFixture {
	t.Helper()
	policy := mutationreceipt.Config{Epoch: mutationreceipt.Epoch{0x71}, Retention: time.Hour, MaxEntries: 32, MaxBytes: 1 << 20}
	store, err := mutationreceipt.New(policy)
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	for _, key := range []string{"tail", "head"} {
		if err := cache.PutVertex(key, &pb.Vertex{
			Key:   key,
			Value: &pb.Vertex_Nil{Nil: true},
		}); err != nil {
			t.Fatal(err)
		}
	}
	cache.AddEdgeWithExpiration("tail", "head", 1, time.Now().Add(time.Hour))
	log := mutationlog.New(mutationlog.Options{Capacity: 32, SubscriberBuffer: 32, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0x72}, hlc.Options{})
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil).WithTombstoneTTL(time.Hour)
	source, err := service.NewReceiptWholeStateSource(svc, store)
	if err != nil {
		t.Fatal(err)
	}
	backupSource := receiptWholeStateBackupCaptureFunc(func(
		ctx context.Context,
		policy mutationreceipt.Config,
	) (service.ReceiptWholeStateBackupCapture, error) {
		capture, err := source.Capture(ctx, policy)
		if err != nil {
			return service.ReceiptWholeStateBackupCapture{}, err
		}
		var seq uint64
		if len(capture.Graph) != 0 && capture.Graph[0].GetHeader() != nil {
			seq = capture.Graph[0].GetHeader().GetCutoffLocalSeq()
		}
		return service.ReceiptWholeStateBackupCapture{
			WholeState: capture,
			WALTip:     producerWALTipWitness(seq),
			NodeID:     clock.NodeID(),
			Generation: [16]byte{0x73},
		}, nil
	})
	return receiptArchiveFixture{cache, log, svc, source, backupSource, policy, clock}
}

func assertProducerGraphCut(t *testing.T, a wholeStateArchive, seq uint64, live, tombstone bool) {
	t.Helper()
	if a.Graph[0].GetHeader().GetCutoffLocalSeq() != seq || len(a.Receipts.Receipts) != 0 ||
		a.Graph[0].GetHeader().GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT ||
		len(a.Origins) != int(seq) {
		t.Fatalf("graph/receipt/origin/log cut differs: header=%+v receipts=%+v origins=%+v", a.Graph[0].GetHeader(), a.Receipts, a.Origins)
	}
	if seq == 1 && a.Origins[0].LastSeq != 1 {
		t.Fatalf("origin cutoff = %+v", a.Origins)
	}
	var hasLive, hasTombstone bool
	for _, frame := range a.Graph {
		if e := frame.GetEdge(); e != nil && e.GetTail() == "tail" && e.GetHead() == "head" {
			hasLive = true
		}
		if e := frame.GetEdgeTombstone(); e != nil && e.GetTail() == "tail" && e.GetHead() == "head" {
			hasTombstone = true
		}
	}
	if hasLive != live || hasTombstone != tombstone {
		t.Fatalf("graph cut live/tombstone = %v/%v, want %v/%v", hasLive, hasTombstone, live, tombstone)
	}
}

func TestReceiptArchiveProducerBlocksStagedWALAndKeepsPublicSnapshotGraphOnly(t *testing.T) {
	entered := make(chan struct{})
	release := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-release:
		default:
			close(release)
		}
	})
	f := newReceiptArchiveFixture(t, receiptArchiveWALFunc(func(mutationlog.Entry) error {
		close(entered)
		<-release
		return nil
	}))
	beforeProduct, err := produceReceiptWholeStateArchive(context.Background(), f.backupSource, f.policy)
	if err != nil {
		t.Fatal(err)
	}
	beforeRaw, _, _ := beforeProduct.bytes()
	assertProducerGraphCut(t, decodedProducerArchive(t, beforeRaw), 0, true, false)
	commitDone := make(chan error, 1)
	go func() {
		_, err := f.service.DeleteEdges(context.Background(), &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "tail", Head: "head"}}})
		commitDone <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for staged WAL")
	}
	type archiveResult struct {
		raw []byte
		err error
	}
	captureDone := make(chan archiveResult, 1)
	started := make(chan struct{})
	go func() {
		close(started)
		product, err := produceReceiptWholeStateArchive(context.Background(), f.backupSource, f.policy)
		raw, _, _ := product.bytes()
		captureDone <- archiveResult{raw, err}
	}()
	<-started
	select {
	case result := <-captureDone:
		t.Fatalf("producer crossed staged WAL: %+v", result)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	select {
	case err := <-commitDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for Delete")
	}
	var after archiveResult
	select {
	case after = <-captureDone:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for archive")
	}
	if after.err != nil {
		t.Fatal(after.err)
	}
	assertProducerGraphCut(t, decodedProducerArchive(t, after.raw), 1, false, true)
	assertProducerGraphCut(t, decodedProducerArchive(t, beforeRaw), 0, true, false)
	rep := service.NewLanternReplicationService(f.log, f.cache, f.clock).WithOriginStates(f.service)
	sender := &receiptArchiveSnapshotSender{}
	if err := rep.Snapshot(context.Background(), &pb.SnapshotRequest{}, sender); err != nil {
		t.Fatal(err)
	}
	if len(sender.frames) < 2 || sender.frames[0].GetHeader().GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 {
		t.Fatalf("public Snapshot format changed: %+v", sender.frames)
	}
}

type receiptArchiveSnapshotSender struct{ frames []*pb.SnapshotResponse }

func (s *receiptArchiveSnapshotSender) Send(frame *pb.SnapshotResponse) error {
	s.frames = append(s.frames, frame)
	return nil
}

func TestReceiptArchiveProducerRejectsPoisonedPublication(t *testing.T) {
	f := newReceiptArchiveFixture(t, receiptArchiveWALFunc(func(mutationlog.Entry) error { return errors.New("indeterminate WAL") }))
	_, _ = f.service.DeleteEdges(context.Background(), &pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "tail", Head: "head"}}})
	product, err := produceReceiptWholeStateArchive(context.Background(), f.backupSource, f.policy)
	if err == nil || product != (receiptBackupSetProduct{}) {
		t.Fatalf("poisoned publication produced %#v: %v", product, err)
	}
}
