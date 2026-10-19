package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"testing/synctest"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type publicationGateSample struct {
	category           string
	wait, body, unlock time.Duration
}

type publicationReadSample struct {
	attempt int
	outcome string
}

type publicationApplySample struct {
	delivery, outcome string
	acquired          bool
	committed         uint64
	pending           int
}

type testPublicationObserver struct {
	noopHotPathMetrics
	mu      sync.Mutex
	gates   []publicationGateSample
	reads   []publicationReadSample
	applies []publicationApplySample
}

type publicationUnlockObserver struct {
	testPublicationObserver
	gate *publicationGate
	held bool
}

func (m *publicationUnlockObserver) OnPublicationGate(category string, wait, body, unlock time.Duration) {
	raw := &m.gate.RWMutex
	if !raw.TryLock() {
		m.held = true
	} else {
		// Inspect only mutex ownership, without advancing service generation.
		raw.Unlock()
	}
	m.testPublicationObserver.OnPublicationGate(category, wait, body, unlock)
}

func (m *testPublicationObserver) OnPublicationGate(category string, wait, body, unlock time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.gates = append(m.gates, publicationGateSample{category, wait, body, unlock})
}

func (m *testPublicationObserver) OnPublicationRead(attempt int, outcome string, _ time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reads = append(m.reads, publicationReadSample{attempt, outcome})
}

func (m *testPublicationObserver) OnPublicationApply(delivery, outcome string, _ time.Duration, acquired bool, committed uint64, pending int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.applies = append(m.applies, publicationApplySample{delivery, outcome, acquired, committed, pending})
}

func observationMutation(origin hlc.NodeID, seq uint64) *pb.Mutation {
	return &pb.Mutation{Origin: origin[:], Seq: seq, Hlc: newHLC(int64(seq), origin),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
			Vertex: &pb.Vertex{Key: "observed", Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		}}}}
}

func closeObservationLog(t *testing.T, log *mutationlog.Log) {
	t.Helper()
	if err := log.Close(); err != nil {
		t.Error(err)
	}
}

func TestPublicationObservationUsesFourSamplesAndExportsAfterUnlock(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	metrics := &publicationUnlockObserver{gate: &svc.replicationCutMu}
	svc.WithHotPathMetrics(metrics)
	samples := []time.Duration{0, 2 * time.Millisecond, 5 * time.Millisecond, 12 * time.Millisecond}
	index := 0
	svc.publicationClock = func() time.Time {
		if index >= len(samples) {
			t.Fatal("unexpected timing sample")
		}
		stamp := time.Unix(1_000_000_000, int64(samples[index]))
		index++
		return stamp
	}
	gate := svc.lockPublicationGate("local_put", false)
	gate.Unlock()
	want := publicationGateSample{"local_put", 2 * time.Millisecond, 3 * time.Millisecond, 7 * time.Millisecond}
	if metrics.held || index != 4 || len(metrics.gates) != 1 || metrics.gates[0] != want {
		t.Fatalf("ownership=%t samples=%d records=%+v", metrics.held, index, metrics.gates)
	}
	if svc.replicationCutMu.generation != 1 {
		t.Fatal("instrumentation changed generation advancement")
	}
}

func TestPublicationObservationSeparatesConvoyWaitFromOwnerBody(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	log := mutationlog.New(mutationlog.Options{Capacity: 16})
	defer closeObservationLog(t, log)
	metrics := &testPublicationObserver{}
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).
		WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil).WithHotPathMetrics(metrics)
	var ticks atomic.Int64
	var samples atomic.Int32
	remoteRequested := make(chan struct{})
	svc.publicationClock = func() time.Time {
		tick := ticks.Load()
		// Two owner samples, then Apply start/preflight/request samples.
		if samples.Add(1) == 5 {
			close(remoteRequested)
		}
		return time.Unix(1_000_000_000, tick)
	}
	// Mutex waits are not durably blocking for synctest. Explicit barriers and
	// an observability-only injected clock calibrate the actual lock order.
	var core sync.RWMutex
	core.RLock()
	var releaseOnce sync.Once
	releaseReader := func() { releaseOnce.Do(core.RUnlock) }
	defer releaseReader()
	owned, writerDone := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(writerDone)
		gate := svc.lockPublicationGate("local_put", false)
		close(owned)
		core.Lock()
		ticks.Add(int64(3 * time.Millisecond))
		core.Unlock()
		gate.Unlock()
	}()
	select {
	case <-owned:
	case <-ctx.Done():
		t.Fatal("local writer did not acquire gate", ctx.Err())
	}
	remoteDone := make(chan error, 1)
	go func() { remoteDone <- svc.ApplyMutation(ctx, observationMutation(hlc.NodeID{2}, 1)) }()
	select {
	case <-remoteRequested:
	case <-ctx.Done():
		t.Fatal("remote Apply did not request gate", ctx.Err())
	}
	if svc.publicationWaiting.count("remote_apply") != 1 {
		t.Fatal("blocked acquisition missing from current waiter count")
	}
	ticks.Add(int64(2 * time.Millisecond))
	releaseReader()
	select {
	case <-writerDone:
	case <-ctx.Done():
		t.Fatal("writer did not finish", ctx.Err())
	}
	select {
	case err := <-remoteDone:
		if err != nil {
			t.Fatal(err)
		}
	case <-ctx.Done():
		t.Fatal("remote Apply did not finish", ctx.Err())
	}
	if svc.LocalSeq(hlc.NodeID{2}) != 1 || log.Len() != 1 {
		t.Fatal("instrumented convoy lost contiguous publication")
	}
	if svc.publicationWaiting.count("remote_apply") != 0 || svc.publicationWaiting.count("local_put") != 0 {
		t.Fatal("completed acquisition retained a waiter")
	}
	if len(metrics.gates) != 2 {
		t.Fatalf("gate observations = %+v", metrics.gates)
	}
	for _, sample := range metrics.gates {
		if sample.unlock < 0 {
			t.Fatal("negative Unlock interval")
		}
		switch sample.category {
		case "local_put":
			if sample.wait != 0 || sample.body != 5*time.Millisecond {
				t.Fatalf("local owner = %+v", sample)
			}
		case "remote_apply":
			if sample.wait != 5*time.Millisecond || sample.body != 0 {
				t.Fatalf("remote wait attributed to owner body: %+v", sample)
			}
		default:
			t.Fatalf("unexpected category %q", sample.category)
		}
	}
}

func TestPublicationObservationCountsThreeConflictsAndLockedFallback(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		metrics := &testPublicationObserver{}
		svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithHotPathMetrics(metrics)
		write, written := make(chan struct{}), make(chan struct{})
		go func() {
			for range 3 {
				<-write
				gate := svc.lockPublicationGate("local_put", false)
				gate.Unlock()
				written <- struct{}{}
			}
		}()
		captures := 0
		err := svc.withPublicGraphReadRetry(context.Background(), func() error {
			captures++
			time.Sleep(time.Millisecond)
			if captures <= 3 {
				write <- struct{}{}
				<-written
			}
			return nil
		})
		if err != nil || captures != 4 || len(metrics.reads) != 4 {
			t.Fatalf("captures=%d records=%+v err=%v", captures, metrics.reads, err)
		}
		for i, sample := range metrics.reads {
			want := "conflict"
			if i == 3 {
				want = "consistent"
			}
			if sample.attempt != i+1 || sample.outcome != want {
				t.Fatalf("read[%d] = %+v", i, sample)
			}
		}
	})
}

func TestPublicationObservationSeparatesQueuedDuplicateAndPrefixDrain(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 16})
	defer closeObservationLog(t, log)
	metrics := &testPublicationObserver{}
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).
		WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil).WithHotPathMetrics(metrics)
	origin := hlc.NodeID{2}
	third := observationMutation(origin, 3)
	for range 2 {
		if err := svc.ApplyMutation(t.Context(), third); err != nil {
			t.Fatal(err)
		}
	}
	conflict := proto.Clone(third).(*pb.Mutation)
	conflict.GetOp().GetPutVertex().GetVertex().Key = "conflicting"
	if err := svc.ApplyMutation(t.Context(), conflict); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("pending conflict = %v", err)
	}
	for _, seq := range []uint64{1, 2, 2} {
		if err := svc.ApplyMutation(t.Context(), observationMutation(origin, seq)); err != nil {
			t.Fatal(err)
		}
	}
	want := []publicationApplySample{
		{"new_sequence", "queued", true, 0, 1},
		{"pending_duplicate", "pending_duplicate", true, 0, 1},
		{"pending_duplicate", "rejected", true, 0, 1},
		{"new_sequence", "committed", true, 1, 1},
		{"new_sequence", "committed", true, 2, 0},
		{"committed_duplicate", "committed_duplicate", true, 0, 0},
	}
	if len(metrics.applies) != len(want) {
		t.Fatalf("apply records = %+v", metrics.applies)
	}
	for i := range want {
		if metrics.applies[i] != want[i] {
			t.Fatalf("apply[%d] = %+v, want %+v", i, metrics.applies[i], want[i])
		}
	}
	if log.Len() != 3 || svc.LocalSeq(origin) != 3 {
		t.Fatal("measurement changed dedup or contiguous publication")
	}
}

func TestPublicationObservationDistinguishesNewSequenceContributionDedup(t *testing.T) {
	localLog := mutationlog.New(mutationlog.Options{Capacity: 16})
	remoteLog := mutationlog.New(mutationlog.Options{Capacity: 16})
	defer closeObservationLog(t, localLog)
	defer closeObservationLog(t, remoteLog)
	localCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	remoteCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	localID, remoteID := hlc.NodeID{1}, hlc.NodeID{2}
	local := NewLanternService(localCache).WithReplication(localLog, hlc.New(localID, hlc.Options{}), nil)
	metrics := &testPublicationObserver{}
	remote := NewLanternService(remoteCache).WithReplication(remoteLog, hlc.New(remoteID, hlc.Options{}), nil).WithHotPathMetrics(metrics)
	var contribution graphcache.ContribID
	contribution[0] = 1
	request := &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "a", Head: "b", Weight: 2,
		Expiration: timestamppb.New(time.Now().Add(time.Hour))}}, ContribIds: [][]byte{contribution[:]}}
	for range 2 {
		if _, err := local.AddEdges(t.Context(), request); err != nil {
			t.Fatal(err)
		}
	}
	if localLog.Len() != 2 || local.LocalSeq(localID) != 2 {
		t.Fatal("a new request with a repeated contribution lost its original publication")
	}
	entries := localLog.RetainedEntries()
	first, second := mustGraphMutation(t, entries[0].Op), mustGraphMutation(t, entries[1].Op)
	for _, m := range []*pb.Mutation{first, first, second} {
		if err := remote.ApplyMutation(t.Context(), m); err != nil {
			t.Fatal(err)
		}
	}
	if remoteLog.Len() != 2 || remote.LocalSeq(localID) != 2 {
		t.Fatal("same origin/seq delivery was re-published or new sequence was suppressed")
	}
	for _, cache := range []*graphcache.GraphCache[string, *pb.Vertex]{localCache, remoteCache} {
		if weight, ok := cache.GetWeight("a", "b"); !ok || weight != 2 {
			t.Fatalf("contribution dedup changed weight: %v/%t", weight, ok)
		}
	}
	if len(metrics.applies) != 3 || metrics.applies[0].committed != 1 ||
		metrics.applies[1].delivery != "committed_duplicate" || metrics.applies[1].committed != 0 ||
		metrics.applies[2].delivery != "new_sequence" || metrics.applies[2].committed != 1 {
		t.Fatalf("duplicate kinds collapsed: %+v", metrics.applies)
	}
}

func TestPublicationObservationRetainsWALRetryAndPublicRefusal(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{Capacity: 16, WAL: &failOncePublicationWAL{}})
	defer closeObservationLog(t, log)
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	metrics := &testPublicationObserver{}
	svc := NewLanternService(cache).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil).WithHotPathMetrics(metrics)
	origin := hlc.NodeID{2}
	m := observationMutation(origin, 1)
	if err := svc.ApplyMutation(t.Context(), m); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("injected WAL result = %v", err)
	}
	if _, err := svc.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "observed"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("unpublished graph exposed: %v", err)
	}
	if err := svc.ApplyMutation(t.Context(), m); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.GetVertex(t.Context(), &pb.GetVertexRequest{Key: "observed"}); err != nil {
		t.Fatal(err)
	}
	if log.Len() != 1 || svc.LocalSeq(origin) != 1 || len(metrics.applies) != 2 {
		t.Fatal("retry changed graph/log/origin publication")
	}
	first, retry := metrics.applies[0], metrics.applies[1]
	if first.outcome != "rejected" || first.committed != 0 || first.pending != 1 ||
		retry.delivery != "pending_duplicate" || retry.outcome != "committed" || retry.committed != 1 || retry.pending != 0 {
		t.Fatalf("WAL retry observations = %+v", metrics.applies)
	}
}

func TestPublicationObservationDisabledDoesNotConsultClock(t *testing.T) {
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour))
	svc.publicationClock = func() time.Time { panic("disabled observations consulted clock") }
	gate := svc.lockPublicationGate("local_put", false)
	gate.Unlock()
	if err := svc.ApplyMutation(t.Context(), nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.withPublicGraphReadRetry(t.Context(), func() error { return nil }); err != nil {
		t.Fatal(err)
	}
}
