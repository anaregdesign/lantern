package dataruntime

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func newGraphPolicyTestRuntime(t *testing.T) (*ServingRuntime, *Executor) {
	t.Helper()
	graph := graphcache.NewGraphCacheWithStaging[string, *pb.Vertex](time.Hour)
	runtime, err := NewGraphOnlyServingRuntime(graph, mutationlog.New(mutationlog.Options{Capacity: 128}), hlc.New(hlc.NodeID{0x61}, hlc.Options{}))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	return runtime, runtime.NewExecutor(nil).WithTombstoneTTL(time.Hour)
}

func TestGraphPolicyValidationOwnershipAndGenerations(t *testing.T) {
	for _, owner := range []*GraphPolicyOwner{nil, {}} {
		if !errors.Is(owner.AvailableGraphPolicy(), ErrGraphPolicyUnavailable) {
			t.Fatal("absent owner advertised graph policy support")
		}
	}
	valid := GraphPolicyConfig{GCInterval: time.Hour}
	for _, config := range []GraphPolicyConfig{
		{}, {GCInterval: -1}, {GCInterval: time.Second, GCEdgeBudget: -1},
		{GCInterval: time.Second, MaxVertices: -1}, {GCInterval: time.Second, MaxEdges: -1},
		{GCInterval: time.Second, MaxVertexCausalEntries: -1}, {GCInterval: time.Second, MaxEdgeCausalEntries: -1},
	} {
		if config.Validate() == nil {
			t.Errorf("invalid policy accepted: %+v", config)
		}
	}
	large := GraphPolicyConfig{GCInterval: time.Duration(1<<63 - 1), MaxVertices: int(^uint(0) >> 1), MaxEdges: int(^uint(0) >> 1)}
	if err := large.Validate(); err != nil {
		t.Fatal(err)
	}
	runtime, primary := newGraphPolicyTestRuntime(t)
	if _, err := NewGraphPolicyOwner(runtime, NewExecutor(runtime.graph), valid); err == nil {
		t.Fatal("foreign primary accepted")
	}
	owner, err := NewGraphPolicyOwner(runtime, primary, valid)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.AvailableGraphPolicy(); err != nil {
		t.Fatal(err)
	}
	if _, err := NewGraphPolicyOwner(runtime, primary, valid); err == nil {
		t.Fatal("second graph/GC owner accepted")
	}
	next := GraphPolicySnapshot{Generation: 1, Config: large}
	if err := owner.Apply(next); err != nil {
		t.Fatal(err)
	}
	if err := owner.Apply(next); err != nil {
		t.Fatalf("idempotent apply: %v", err)
	}
	for _, conflict := range []GraphPolicySnapshot{{Config: valid}, {Generation: 1, Config: valid}} {
		if err := owner.Apply(conflict); !errors.Is(err, ErrGraphPolicyConflict) {
			t.Fatalf("conflict = %v", err)
		}
	}
	if owner.Snapshot() != next || primary.capacityLimits().MaxVertices != large.MaxVertices {
		t.Fatal("effective generation not atomically installed")
	}
}

func TestGraphPolicyOwnsOnlyItsAvailableInstalledRuntimeAndPrimary(t *testing.T) {
	runtime, primary := newGraphPolicyTestRuntime(t)
	owner, err := NewGraphPolicyOwner(runtime, primary, GraphPolicyConfig{GCInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	otherRuntime, otherPrimary := newGraphPolicyTestRuntime(t)
	for _, invalid := range []struct {
		owner   *GraphPolicyOwner
		runtime *ServingRuntime
		primary *Executor
	}{
		{nil, runtime, primary}, {&GraphPolicyOwner{}, runtime, primary},
		{owner, nil, primary}, {owner, runtime, nil},
		{owner, otherRuntime, otherPrimary}, {owner, runtime, runtime.NewExecutor(nil)},
	} {
		if invalid.owner.OwnsInstallation(invalid.runtime, invalid.primary) {
			t.Fatal("unavailable or foreign graph installation accepted")
		}
	}
	if !owner.OwnsInstallation(runtime, primary) {
		t.Fatal("actual graph installation refused")
	}
	// An available resource alone is insufficient: the current runtime slot
	// must still point to the exact owner named by certification.
	runtime.graphPolicy.Store(nil)
	if owner.OwnsInstallation(runtime, primary) {
		t.Fatal("uninstalled owner accepted")
	}
	runtime.graphPolicy.Store(owner)
	if err := runtime.Close(); err != nil {
		t.Fatal(err)
	}
	if owner.OwnsInstallation(runtime, primary) {
		t.Fatal("closed runtime owner accepted")
	}
}

func TestGraphPolicyShrinkRetainsDataAndReplicationRestoreBypass(t *testing.T) {
	runtime, primary := newGraphPolicyTestRuntime(t)
	owner, err := NewGraphPolicyOwner(runtime, primary, GraphPolicyConfig{GCInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := primary.PutVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "a"}, {Key: "b"}}}); err != nil {
		t.Fatal(err)
	}
	before := runtime.graph.CausalMetadataStats()
	config := GraphPolicyConfig{GCInterval: time.Hour, MaxVertices: 1, MaxEdges: 1, MaxVertexCausalEntries: 1, MaxEdgeCausalEntries: 1}
	preview, err := owner.Preview(config)
	if err != nil || !preview.VerticesOverLimit || !preview.VertexCausalOverLimit || preview.VertexFootprint != 2 {
		t.Fatalf("shrink preview = %+v, %v", preview, err)
	}
	if err := owner.Apply(GraphPolicySnapshot{Generation: 1, Config: config}); err != nil {
		t.Fatal(err)
	}
	if runtime.graph.VertexCount() != 2 || runtime.graph.CausalMetadataStats().VertexEntries != before.VertexEntries {
		t.Fatal("shrink evicted graph or causal floors")
	}
	if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "local-soft"}}); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("future local soft admission = %v", err)
	}
	config.MaxVertices, config.MaxEdges = 0, 0
	if err := owner.Apply(GraphPolicySnapshot{Generation: 2, Config: config}); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "local-causal"}}); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("future local causal admission = %v", err)
	}
	origin := hlc.NodeID{0x62}
	ts := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	if err := primary.ApplyMutation(t.Context(), &pb.Mutation{
		Seq: 1, Origin: origin[:], Hlc: hlcToProto(ts),
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "replicated"}}}},
	}); err != nil {
		t.Fatalf("replicated union rejected: %v", err)
	}
	if _, err := primary.RestoreVertices(t.Context(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "restored"}}}); err != nil {
		t.Fatalf("restore rejected: %v", err)
	}
	if runtime.graph.VertexCount() != 4 {
		t.Fatalf("replicated/restored graph count = %d", runtime.graph.VertexCount())
	}
	config.MaxVertexCausalEntries = 0
	if err := owner.Apply(GraphPolicySnapshot{Generation: 3, Config: config}); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "resumed"}}); err != nil {
		t.Fatalf("raised cap did not resume local admission: %v", err)
	}
}

func TestGraphPolicyGCTransitionsJoinBeforePublicationCut(t *testing.T) {
	runtime, primary := newGraphPolicyTestRuntime(t)
	owner, err := NewGraphPolicyOwner(runtime, primary, GraphPolicyConfig{GCInterval: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	started := make(chan time.Duration, 8)
	var active, maximum atomic.Int32
	owner.watch = func(ctx context.Context, interval time.Duration) {
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		started <- interval
		<-ctx.Done()
		// A final hook may need the publication cut. Apply must not own it
		// while waiting for cancellation, or this read/join would deadlock.
		if _, err := owner.Preview(owner.Snapshot().Config); err != nil {
			t.Error(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- owner.RunGC(ctx) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("GC owner failed to join")
		}
	})
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		t.Fatal("GC owner did not start")
	}
	if err := owner.RunGC(t.Context()); !errors.Is(err, ErrGraphPolicyRunning) {
		t.Fatalf("second watcher = %v", err)
	}
	config := owner.Snapshot().Config
	config.GCInterval += time.Second
	config.GCEdgeBudget = 7
	if err := owner.Apply(GraphPolicySnapshot{Generation: 1, Config: config}); err != nil {
		t.Fatal(err)
	}
	select {
	case interval := <-started:
		if interval != config.GCInterval {
			t.Fatalf("new interval = %v", interval)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("replacement watcher did not start")
	}
	config.MaxVertices = 1
	if err := owner.Apply(GraphPolicySnapshot{Generation: 2, Config: config}); err != nil {
		t.Fatal(err)
	}
	if len(started) != 0 {
		t.Fatal("capacity-only generation unnecessarily restarted GC cadence")
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
		done <- nil
	case <-time.After(5 * time.Second):
		t.Fatal("GC did not stop")
	}
	if maximum.Load() != 1 || active.Load() != 0 {
		t.Fatalf("watcher overlap/leak: max %d active %d", maximum.Load(), active.Load())
	}
	if !errors.Is(owner.AvailableGraphPolicy(), ErrGraphPolicyStopped) {
		t.Fatal("stopped GC owner advertised support")
	}
	if err := owner.Apply(GraphPolicySnapshot{Generation: 3, Config: config}); !errors.Is(err, ErrGraphPolicyStopped) {
		t.Fatalf("stopped owner apply = %v", err)
	}
}

func TestGraphPolicyConcurrentWritesPreviewAndGCChanges(t *testing.T) {
	runtime, primary := newGraphPolicyTestRuntime(t)
	owner, err := NewGraphPolicyOwner(runtime, primary, GraphPolicyConfig{GCInterval: time.Nanosecond})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	ticks := make(chan struct{}, 1)
	runtime.graph.SetGCHooks(nil, func(time.Duration) {
		select {
		case ticks <- struct{}{}:
		default:
		}
	})
	done := make(chan error, 1)
	go func() { done <- owner.RunGC(ctx) }()
	defer func() { cancel(); <-done }()
	select {
	case <-ticks:
	case <-time.After(5 * time.Second):
		t.Fatal("actual Core GC did not execute")
	}
	var workers sync.WaitGroup
	workers.Add(2)
	go func() {
		defer workers.Done()
		for i := range 24 {
			_, err := primary.PutVertex(t.Context(), &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: fmt.Sprintf("local-%d", i)}})
			if err != nil && connect.CodeOf(err) != connect.CodeResourceExhausted {
				t.Error(err)
			}
		}
	}()
	go func() {
		defer workers.Done()
		for generation := uint64(1); generation <= 24; generation++ {
			config := GraphPolicyConfig{GCInterval: time.Nanosecond * time.Duration(1+generation%3), GCEdgeBudget: int(generation % 3), MaxVertices: int(generation % 5)}
			if err := owner.Apply(GraphPolicySnapshot{Generation: generation, Config: config}); err != nil {
				t.Error(err)
			}
			if _, err := owner.Preview(config); err != nil {
				t.Error(err)
			}
		}
	}()
	workers.Wait()
}
