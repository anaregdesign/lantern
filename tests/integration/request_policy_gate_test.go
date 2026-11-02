package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	coregraph "github.com/anaregdesign/lantern/core/graph"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

func wireRequestPolicy() service.RequestPolicy {
	return service.RequestPolicy{
		Validation:       service.RequestValidationLimits{MaxKeyLen: 64, MaxBatchSize: 100, IlluminateMaxStep: 16, IlluminateMaxK: 20},
		Scan:             service.ScanLimits{ScanDefaultLimit: 2, ScanMaxLimit: 3, DeleteByPrefixDefaultLimit: 2, DeleteByPrefixMaxLimit: 3},
		Traversal:        service.TraversalLimits{WorkBudget: graphcache.PPRWorkBudget{MaxPushes: 1000, MaxTouchedEdges: 10000}, MaxResults: 20},
		TraversalTimeout: time.Second,
	}
}

type requestPolicyWireObservation struct {
	snapshot    service.RequestPolicySnapshot
	topN        int
	budget      graphcache.PPRWorkBudget
	hasDeadline bool
}

type requestPolicyWireBackend struct {
	service.Backend
	observations chan requestPolicyWireObservation
}

func (b requestPolicyWireBackend) PersonalizedPageRankWithWorkBudgetContext(ctx context.Context, seed string, topN int, _, _ float64, _ graphcache.EdgeWeighting, _ func(string) bool, budget graphcache.PPRWorkBudget) (*coregraph.Graph[string, *pb.Vertex], error) {
	snapshot, _ := service.RequestPolicyFromContext(ctx)
	_, hasDeadline := ctx.Deadline()
	b.observations <- requestPolicyWireObservation{snapshot, topN, budget, hasDeadline}
	g := coregraph.NewGraph[string, *pb.Vertex]()
	g.Vertices[seed] = &pb.Vertex{Key: seed}
	return g, nil
}

func TestRequestPolicyRealConnectCapturesBeforeValidationThroughTraversal(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	initial := service.RequestPolicySnapshot{Generation: 40, Policy: wireRequestPolicy()}
	owner, err := service.NewRequestPolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	observations := make(chan requestPolicyWireObservation, 2)
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(requestPolicyWireBackend{cache, observations}).WithRequestPolicySource(owner)
	validator := provider.NewValidationInterceptor(provider.ValidationLimits{}).WithRequestPolicySource(owner)
	entered, proceed := make(chan struct{}), make(chan struct{})
	var first sync.Once
	block := connect.UnaryInterceptorFunc(func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			if _, ok := req.Any().(*pb.IlluminateRequest); ok {
				wait := false
				first.Do(func() { wait = true; close(entered) })
				if wait {
					select {
					case <-proceed:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
			}
			return next(ctx, req)
		}
	})
	srv := newConnectTestServer(t, svc, nil, validator.ConnectInterceptor(), block)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	result := make(chan error, 1)
	go func() {
		_, err := raw.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{Seed: "root", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}}}))
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	next := initial
	next.Generation++
	next.Policy.Validation.MaxKeyLen = 1
	next.Policy.Traversal.MaxResults = 1
	next.Policy.Traversal.WorkBudget = graphcache.PPRWorkBudget{MaxPushes: 1, MaxTouchedEdges: 2}
	next.Policy.TraversalTimeout = 0
	if err := owner.Apply(next); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-result; err != nil {
		t.Fatalf("admitted old request: %v", err)
	}
	old := <-observations
	if old.snapshot != initial || old.topN != initial.Policy.Traversal.MaxResults || old.budget != initial.Policy.Traversal.WorkBudget || !old.hasDeadline {
		t.Fatalf("old request mixed generations: %+v", old)
	}
	if _, err := raw.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{Seed: "root", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("new request escaped new key validation: %v", err)
	}
	// No client deadline is sent, so zero explicitly keeps traversal unbounded
	// by a server timeout. The outer test still owns cancellation of the call.
	noDeadline, cancelCall := context.WithCancel(context.WithoutCancel(ctx))
	defer cancelCall()
	stop := context.AfterFunc(ctx, cancelCall)
	defer stop()
	if _, err := raw.Illuminate(noDeadline, connect.NewRequest(&pb.IlluminateRequest{Seed: "r", Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{}}})); err != nil {
		t.Fatal(err)
	}
	current := <-observations
	if current.snapshot != next || current.topN != 1 || current.budget != next.Policy.Traversal.WorkBudget || current.hasDeadline {
		t.Fatalf("new traversal = %+v", current)
	}
	status, err := raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil || status.Msg.GetMaxKeyBytes() != 1 || status.Msg.GetMaxBatchSize() != 100 {
		t.Fatalf("effective status = (%+v, %v)", status, err)
	}
}

func TestRequestPolicyRealConnectCursorSurvivesClampAndKeepsSecurityBinding(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	initial := service.RequestPolicySnapshot{Generation: 1, Policy: wireRequestPolicy()}
	owner, err := service.NewRequestPolicyOwner(initial)
	if err != nil {
		t.Fatal(err)
	}
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithDataNamespace().WithRequestPolicySource(owner)
	cache.EnablePrefixIndex(func(key string) string { return key })
	validator := provider.NewValidationInterceptor(provider.ValidationLimits{}).WithRequestPolicySource(owner)
	srv := newConnectTestServer(t, svc, nil, validator.ConnectInterceptor())
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	for _, key := range []string{"p:a", "p:b", "p:c", "p:d"} {
		if _, err := raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}})); err != nil {
			t.Fatal(err)
		}
	}
	page, err := raw.ScanVertices(ctx, connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "p:"}))
	if err != nil || len(page.Msg.GetVertices()) != 2 || len(page.Msg.GetNextCursor()) == 0 {
		t.Fatalf("first page = (%+v, %v)", page, err)
	}
	cursor := page.Msg.GetNextCursor()
	next := initial
	next.Generation++
	next.Policy.Scan.ScanDefaultLimit, next.Policy.Scan.ScanMaxLimit = 1, 1
	if err := owner.Apply(next); err != nil {
		t.Fatal(err)
	}
	resumed, err := raw.ScanVertices(ctx, connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "p:", Limit: 999, Cursor: cursor}))
	if err != nil || len(resumed.Msg.GetVertices()) != 1 || resumed.Msg.GetVertices()[0].GetKey() != "p:c" {
		t.Fatalf("new clamp resume = (%+v, %v)", resumed, err)
	}
	for _, req := range []*pb.ScanVerticesRequest{
		{Prefix: "other:", Cursor: cursor},
		{Prefix: "p:", Cursor: cursor, Order: pb.ScanOrder_SCAN_ORDER_DESC},
		{Prefix: "p:", Cursor: append(append([]byte(nil), cursor[:len(cursor)-1]...), cursor[len(cursor)-1]^1)},
	} {
		if _, err := raw.ScanVertices(ctx, connect.NewRequest(req)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("cursor substitution accepted: %v", err)
		}
	}
	if _, err := raw.ScanVertexKeys(ctx, connect.NewRequest(&pb.ScanVertexKeysRequest{Prefix: "p:", Cursor: cursor})); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("cross-RPC cursor accepted: %v", err)
	}
	if got := cache.VertexCount(); got != 4 {
		t.Fatalf("cursor checks changed graph: %d vertices", got)
	}
}
