package dataruntime

import (
	"context"
	"sync"
	"testing"
	"time"

	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func testRequestPolicy() RequestPolicy {
	return RequestPolicy{
		Validation:       RequestValidationLimits{MaxKeyLen: 256, MaxBatchSize: 100, IlluminateMaxStep: 16, IlluminateMaxK: 100},
		Scan:             ScanLimits{ScanDefaultLimit: 2, ScanMaxLimit: 3, DeleteByPrefixDefaultLimit: 2, DeleteByPrefixMaxLimit: 3},
		Traversal:        TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 100, MaxTouchedEdges: 1000}, MaxResults: 10},
		TraversalTimeout: time.Second,
	}
}

func TestRequestPolicyValidate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*RequestPolicy)
	}{
		{"key", func(p *RequestPolicy) { p.Validation.MaxKeyLen = -1 }},
		{"batch", func(p *RequestPolicy) { p.Validation.MaxBatchSize = -1 }},
		{"step", func(p *RequestPolicy) { p.Validation.IlluminateMaxStep = -1 }},
		{"k", func(p *RequestPolicy) { p.Validation.IlluminateMaxK = -1 }},
		{"scan default", func(p *RequestPolicy) { p.Scan.ScanDefaultLimit = 0 }},
		{"scan max", func(p *RequestPolicy) { p.Scan.ScanMaxLimit = 0 }},
		{"scan pair", func(p *RequestPolicy) { p.Scan.ScanDefaultLimit = 4 }},
		{"delete default", func(p *RequestPolicy) { p.Scan.DeleteByPrefixDefaultLimit = 0 }},
		{"delete max", func(p *RequestPolicy) { p.Scan.DeleteByPrefixMaxLimit = 0 }},
		{"delete pair", func(p *RequestPolicy) { p.Scan.DeleteByPrefixDefaultLimit = 4 }},
		{"pushes", func(p *RequestPolicy) { p.Traversal.WorkBudget.MaxPushes = 0 }},
		{"touched", func(p *RequestPolicy) { p.Traversal.WorkBudget.MaxTouchedEdges = -1 }},
		{"results", func(p *RequestPolicy) { p.Traversal.MaxResults = 0 }},
		{"negative timeout", func(p *RequestPolicy) { p.TraversalTimeout = -time.Millisecond }},
		{"sub-millisecond timeout", func(p *RequestPolicy) { p.TraversalTimeout = time.Nanosecond }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := testRequestPolicy()
			tc.mutate(&p)
			if err := p.Validate(); err == nil {
				t.Fatal("invalid policy accepted")
			}
			if _, err := NewRequestPolicyOwner(RequestPolicySnapshot{Policy: p}); err == nil {
				t.Fatal("invalid initial policy accepted")
			}
		})
	}
	p := testRequestPolicy()
	p.Validation = RequestValidationLimits{}
	p.TraversalTimeout = 0
	if err := p.Validate(); err != nil {
		t.Fatalf("explicit disabled dimensions: %v", err)
	}
}

func TestRequestPolicyBootstrapRetainsActualInvalidValuesAtGenerationZero(t *testing.T) {
	actual := testRequestPolicy()
	actual.Scan.ScanDefaultLimit = 0
	owner := NewBootstrapRequestPolicyOwner(actual)
	if got := owner.RequestPolicySnapshot(); got.Generation != 0 || got.Policy != actual {
		t.Fatalf("bootstrap replaced actual values: %+v", got)
	}
	if err := owner.Apply(RequestPolicySnapshot{Generation: 1, Policy: actual}); err == nil {
		t.Fatal("bootstrap seam bypassed managed validation")
	}
	valid := testRequestPolicy()
	if err := owner.Apply(RequestPolicySnapshot{Generation: 1, Policy: valid}); err != nil {
		t.Fatal(err)
	}
	executor := NewExecutor(newFakeBackend())
	if _, err := executor.BootstrapRequestPolicy(RequestValidationLimits{MaxKeyLen: 4}); err != nil {
		t.Fatal(err)
	}
	executor.WithRequestPolicySource(owner)
	if !executor.UsesRequestPolicyOwner(owner) {
		t.Fatal("actual owner identity missing")
	}
	if _, err := executor.BootstrapRequestPolicy(RequestValidationLimits{}); err == nil {
		t.Fatal("bound owner was treated as unowned startup")
	}
}

func TestRequestPolicyOwnerAtomicGeneration(t *testing.T) {
	initial := RequestPolicySnapshot{Generation: 4, Policy: testRequestPolicy()}
	o, err := NewRequestPolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	next := initial
	next.Generation++
	next.Policy.Scan.ScanDefaultLimit = 3
	next.Policy.Validation.MaxKeyLen = 10
	var wg sync.WaitGroup
	wg.Go(func() {
		for range 1000 {
			got := o.RequestPolicySnapshot()
			if got != initial && got != next {
				t.Errorf("torn snapshot: %+v", got)
			}
		}
	})
	if err := o.Apply(next); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	if err := o.Apply(next); err != nil {
		t.Fatalf("idempotent callback: %v", err)
	}
	conflict := next
	conflict.Policy.Validation.MaxKeyLen++
	invalid := next
	invalid.Generation++
	invalid.Policy.Scan.ScanMaxLimit = 0
	for _, rejected := range []RequestPolicySnapshot{initial, conflict, invalid} {
		if err := o.Apply(rejected); err == nil {
			t.Fatalf("accepted conflicting/invalid snapshot: %+v", rejected)
		}
		if got := o.RequestPolicySnapshot(); got != next {
			t.Fatalf("failed update changed installed snapshot: %+v", got)
		}
	}
}

func TestRequestPolicyCapturedBeforeUpdate(t *testing.T) {
	initial := RequestPolicySnapshot{Generation: 7, Policy: testRequestPolicy()}
	o, err := NewRequestPolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	ctx := CaptureRequestPolicy(context.Background(), o)
	next := initial
	next.Generation++
	next.Policy.Scan.ScanDefaultLimit = 1
	next.Policy.Scan.ScanMaxLimit = 1
	next.Policy.Validation.MaxKeyLen = 1
	if err := o.Apply(next); err != nil {
		t.Fatal(err)
	}
	if got, _ := RequestPolicyFromContext(CaptureRequestPolicy(ctx, o)); got != initial {
		t.Fatalf("recaptured request: %+v", got)
	}
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(key string) string { return key })
	for _, key := range []string{"a", "b", "c", "d"} {
		if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	s := NewExecutor(cache).WithRequestPolicySource(o)
	oldPage, err := s.Public().ScanVertices(ctx, &pb.ScanVerticesRequest{})
	if err != nil || len(oldPage.GetVertices()) != 2 {
		t.Fatalf("captured page = (%+v, %v)", oldPage, err)
	}
	newPage, err := s.Public().ScanVertices(context.Background(), &pb.ScanVerticesRequest{})
	if err != nil || len(newPage.GetVertices()) != 1 {
		t.Fatalf("new page = (%+v, %v)", newPage, err)
	}
	oldStatus, err := s.GetServerStatus(ctx, &pb.GetServerStatusRequest{})
	if err != nil || oldStatus.GetScanDefaultLimit() != 2 || oldStatus.GetMaxKeyBytes() != 256 {
		t.Fatalf("captured status = (%+v, %v)", oldStatus, err)
	}
	newStatus, err := s.GetServerStatus(context.Background(), &pb.GetServerStatusRequest{})
	if err != nil || newStatus.GetScanDefaultLimit() != 1 || newStatus.GetMaxKeyBytes() != 1 {
		t.Fatalf("new status = (%+v, %v)", newStatus, err)
	}
}

type policyTraversalBackend struct {
	Backend
	observe func(context.Context, int, graphcache.PPRWorkBudget)
}

func (b policyTraversalBackend) PersonalizedPageRankWithWorkBudgetContext(ctx context.Context, _ string, topN int, _, _ float64, _ graphcache.EdgeWeighting, _ func(string) bool, budget graphcache.PPRWorkBudget) (*coregraph.Graph[string, *pb.Vertex], error) {
	b.observe(ctx, topN, budget)
	return coregraph.NewGraph[string, *pb.Vertex](), nil
}

func TestRequestPolicyTraversalSnapshotAndClientDeadline(t *testing.T) {
	initial := RequestPolicySnapshot{Generation: 1, Policy: testRequestPolicy()}
	o, err := NewRequestPolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	ctx := CaptureRequestPolicy(context.Background(), o)
	next := initial
	next.Generation++
	next.Policy.Traversal.MaxResults = 1
	next.Policy.Traversal.WorkBudget = graphcache.PPRWorkBudget{MaxPushes: 1, MaxTouchedEdges: 1}
	next.Policy.TraversalTimeout = 0
	if err := o.Apply(next); err != nil {
		t.Fatal(err)
	}
	for _, captured := range []bool{true, false} {
		t.Run(map[bool]string{true: "old request", false: "new request"}[captured], func(t *testing.T) {
			callCtx := context.Background()
			want := next
			if captured {
				callCtx, want = ctx, initial
			}
			deadline := time.Now().Add(time.Minute)
			if captured {
				deadline = time.Now().Add(100 * time.Millisecond)
			}
			callCtx, cancel := context.WithDeadline(callCtx, deadline)
			defer cancel()
			observed := false
			s := NewExecutor(policyTraversalBackend{Backend: newFakeBackend(), observe: func(gotCtx context.Context, topN int, budget graphcache.PPRWorkBudget) {
				observed = true
				if snapshot, _ := RequestPolicyFromContext(gotCtx); snapshot != want {
					t.Errorf("snapshot = %+v, want %+v", snapshot, want)
				}
				if topN != want.Policy.Traversal.MaxResults || budget != want.Policy.Traversal.WorkBudget {
					t.Errorf("traversal topN/budget = %d/%+v", topN, budget)
				}
				if got, ok := gotCtx.Deadline(); !ok || !got.Equal(deadline) {
					t.Errorf("client deadline changed: %v/%v", got, ok)
				}
			}}).WithRequestPolicySource(o)
			_, err := s.Illuminate(callCtx, &pb.IlluminateRequest{Seed: "a", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}}})
			if err != nil || !observed {
				t.Fatalf("Illuminate = %v, observed=%v", err, observed)
			}
		})
	}
}

func TestRequestPolicyEveryPaginationConsumerUsesCapturedGroup(t *testing.T) {
	initial := RequestPolicySnapshot{Generation: 2, Policy: testRequestPolicy()}
	owner, err := NewRequestPolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	old := CaptureRequestPolicy(context.Background(), owner)
	next := initial
	next.Generation++
	next.Policy.Scan = ScanLimits{ScanDefaultLimit: 1, ScanMaxLimit: 1, DeleteByPrefixDefaultLimit: 1, DeleteByPrefixMaxLimit: 1}
	if err := owner.Apply(next); err != nil {
		t.Fatal(err)
	}
	for _, consumer := range []struct {
		name string
		call func(context.Context, *Executor) (int, error)
	}{
		{"vertices", func(ctx context.Context, s *Executor) (int, error) {
			r, err := s.ScanVertices(ctx, &pb.ScanVerticesRequest{Prefix: "p:"})
			return len(r.GetVertices()), err
		}},
		{"keys", func(ctx context.Context, s *Executor) (int, error) {
			r, err := s.ScanVertexKeys(ctx, &pb.ScanVertexKeysRequest{Prefix: "p:"})
			return len(r.GetKeys()), err
		}},
		{"edges", func(ctx context.Context, s *Executor) (int, error) {
			r, err := s.ScanEdges(ctx, &pb.ScanEdgesRequest{TailPrefix: "p:"})
			return len(r.GetEdges()), err
		}},
		{"degree", func(ctx context.Context, s *Executor) (int, error) {
			r, err := s.TopVerticesByDegree(ctx, &pb.TopVerticesByDegreeRequest{Prefix: "p:"})
			return len(r.GetEntries()), err
		}},
		{"delete vertices", func(ctx context.Context, s *Executor) (int, error) {
			r, err := s.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{Prefix: "p:"})
			return int(r.GetDeleted()), err
		}},
		{"delete edges", func(ctx context.Context, s *Executor) (int, error) {
			r, err := s.DeleteEdgesByPrefix(ctx, &pb.DeleteEdgesByPrefixRequest{TailPrefix: "p:"})
			return int(r.GetDeleted()), err
		}},
	} {
		t.Run(consumer.name, func(t *testing.T) {
			for _, captured := range []bool{true, false} {
				cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
				cache.EnablePrefixIndex(func(key string) string { return key })
				for _, key := range []string{"p:a", "p:b", "p:c"} {
					if err := cache.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
						t.Fatal(err)
					}
					cache.AddEdge(key, "target", 1)
				}
				ctx, want := context.Background(), 1
				if captured {
					ctx, want = old, 2
				}
				got, err := consumer.call(ctx, NewExecutor(cache).WithRequestPolicySource(owner))
				if err != nil || got != want {
					t.Fatalf("captured=%v: got %d/%v, want %d", captured, got, err, want)
				}
			}
		})
	}
}
