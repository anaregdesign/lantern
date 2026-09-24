package backup

import (
	"bytes"
	"context"
	"errors"
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

func producerCapture(a wholeStateArchive) service.ReceiptWholeStateCapture {
	return service.ReceiptWholeStateCapture{Graph: a.Graph, Receipts: a.Receipts, Policy: a.Policy, Origins: a.Origins}
}

func decodedProducerArchive(t *testing.T, raw []byte) wholeStateArchive {
	t.Helper()
	got, err := decodeWholeStateArchive(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return got
}

func TestReceiptArchiveProducerUsesOneDetachedCut(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	calls := 0
	source := func(_ context.Context, policy mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
		calls++
		if policy != a.Policy || calls != 1 {
			return service.ReceiptWholeStateCapture{}, errors.New("unexpected second source read or policy")
		}
		return producerCapture(a), nil
	}
	raw, err := produceReceiptWholeStateArchive(context.Background(), source, a.Policy)
	if err != nil || calls != 1 {
		t.Fatalf("producer = %v, calls=%d", err, calls)
	}
	got := decodedProducerArchive(t, raw)
	if !reflect.DeepEqual(got.Receipts, a.Receipts) || !reflect.DeepEqual(got.Origins, a.Origins) || got.Policy != a.Policy || len(got.Graph) != len(a.Graph) {
		t.Fatalf("detached archive lost receipt, origin, policy, or graph section: %+v", got)
	}
	for i := range a.Graph {
		if !proto.Equal(got.Graph[i], a.Graph[i]) {
			t.Fatalf("graph frame %d changed during composition", i)
		}
	}
	// Mutating the source after its only read cannot change the returned bytes.
	a.Graph[1].GetVertex().Vertex.Key = "changed"
	a.Receipts.Receipts[0].Result[0] = 0
	a.Origins[0].LastSeq++
	again := decodedProducerArchive(t, raw)
	if !proto.Equal(again.Graph[1], got.Graph[1]) || !reflect.DeepEqual(again.Receipts, got.Receipts) ||
		!reflect.DeepEqual(again.Origins, got.Origins) {
		t.Fatal("archive bytes changed with later source mutation")
	}
	if _, err := decodeWholeStateArchive(bytes.NewReader(raw[:len(raw)-1])); !errors.Is(err, errWholeStateArchive) {
		t.Fatalf("truncated archive accepted: %v", err)
	}
	tampered := append([]byte(nil), raw...)
	tampered[wholeStateArchiveHeaderSize+8] ^= 1
	if _, err := decodeWholeStateArchive(bytes.NewReader(tampered)); !errors.Is(err, errWholeStateArchive) {
		t.Fatalf("tampered archive accepted: %v", err)
	}
}

func TestReceiptArchiveProducerFailsWithoutPartialBytes(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	for _, tc := range []struct {
		name   string
		source service.ReceiptWholeStateSource
	}{
		{"nil source", nil},
		{"capture failure", func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
			return producerCapture(a), errors.New("capture failed after partial work")
		}},
		{"invalid policy", func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
			bad := producerCapture(wholeStateArchiveFixture(t))
			bad.Policy.MaxEntries = 0
			return bad, nil
		}},
		{"invalid graph", func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
			bad := producerCapture(wholeStateArchiveFixture(t))
			bad.Graph[0].GetHeader().Format = pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1
			return bad, nil
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			raw, err := produceReceiptWholeStateArchive(context.Background(), tc.source, a.Policy)
			if err == nil || raw != nil {
				t.Fatalf("failed producer returned bytes: %d, %v", len(raw), err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	calls := 0
	raw, err := produceReceiptWholeStateArchive(canceled, func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
		calls++
		return producerCapture(a), nil
	}, a.Policy)
	if !errors.Is(err, context.Canceled) || raw != nil || calls != 0 {
		t.Fatalf("canceled producer = %d bytes, %v, %d source calls", len(raw), err, calls)
	}
	ctx, cancelDuringCapture := context.WithCancel(context.Background())
	raw, err = produceReceiptWholeStateArchive(ctx, func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
		cancelDuringCapture()
		return producerCapture(a), nil
	}, a.Policy)
	if !errors.Is(err, context.Canceled) || raw != nil {
		t.Fatalf("producer canceled after capture = %d bytes, %v", len(raw), err)
	}
	f := newReceiptArchiveFixture(t, nil)
	wrongPolicy := f.policy
	wrongPolicy.Retention = 2 * time.Hour
	raw, err = produceReceiptWholeStateArchive(context.Background(), f.source, wrongPolicy)
	if !errors.Is(err, mutationreceipt.ErrInvalidSnapshot) || raw != nil {
		t.Fatalf("live capture policy mismatch = %d bytes, %v", len(raw), err)
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
			raw, err := produceReceiptWholeStateArchive(context.Background(), func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
				calls++
				return producerCapture(a), nil
			}, requested)
			if !errors.Is(err, errWholeStateArchive) || raw != nil || calls != 1 {
				t.Fatalf("miswired source returned %d bytes after %d reads: %v", len(raw), calls, err)
			}
		})
	}
}

func TestReceiptArchiveProducerRejectsOversizeFrame(t *testing.T) {
	a := wholeStateArchiveFixture(t)
	a.Graph[1].GetVertex().Vertex.Value = &pb.Vertex_String_{String_: strings.Repeat("v", wholeStateArchiveMaxFrame)}
	raw, err := produceReceiptWholeStateArchive(context.Background(), func(context.Context, mutationreceipt.Config) (service.ReceiptWholeStateCapture, error) {
		return producerCapture(a), nil
	}, a.Policy)
	if !errors.Is(err, errWholeStateArchive) || raw != nil {
		t.Fatalf("oversize frame produced %d bytes: %v", len(raw), err)
	}
}

type receiptArchiveFixture struct {
	cache   *graphcache.GraphCache[string, *pb.Vertex]
	log     *mutationlog.Log
	service *service.LanternService
	source  service.ReceiptWholeStateSource
	policy  mutationreceipt.Config
	clock   *hlc.Clock
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
		if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
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
	return receiptArchiveFixture{cache, log, svc, source, policy, clock}
}

func assertProducerGraphCut(t *testing.T, a wholeStateArchive, seq uint64, live, tombstone bool) {
	t.Helper()
	if a.Graph[0].GetHeader().GetCutoffLocalSeq() != seq || len(a.Receipts.Receipts) != 0 ||
		a.Graph[0].GetHeader().GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_RECEIPT_V1 ||
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
	beforeRaw, err := produceReceiptWholeStateArchive(context.Background(), f.source, f.policy)
	if err != nil {
		t.Fatal(err)
	}
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
		raw, err := produceReceiptWholeStateArchive(context.Background(), f.source, f.policy)
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
	raw, err := produceReceiptWholeStateArchive(context.Background(), f.source, f.policy)
	if err == nil || raw != nil {
		t.Fatalf("poisoned publication produced %d bytes: %v", len(raw), err)
	}
}
