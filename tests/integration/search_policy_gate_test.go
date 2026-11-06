package integration_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/service"
)

type searchPolicyWireAuthority struct {
	mu      sync.Mutex
	version service.SearchPolicyVersion
}

func (a *searchPolicyWireAuthority) DesiredSearchPolicy(context.Context) (service.SearchPolicyVersion, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.version, nil
}

func (a *searchPolicyWireAuthority) set(version service.SearchPolicyVersion) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.version = version
}

func TestSearchPolicyRealConnectPendingRestartCursorAndIndexPreservation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	limits := productionSearchLimits(true, true)
	cache := newProductionSearchCache(time.Minute, true, true, limits.AnalysisLimits)
	svc := service.NewLanternService(cache).WithDataNamespace().WithSearchLimits(limits)
	baseline := svc.SearchPolicyBaseline()
	policy := service.SearchRequestPolicyFromLimits(baseline)
	policy.DefaultLimit, policy.MaxLimit = 1, 2
	initial, err := service.ResolveSearchPolicy(baseline, 10, policy)
	if err != nil {
		t.Fatal(err)
	}
	authority := &searchPolicyWireAuthority{version: initial.Version}
	owner, err := service.NewSearchPolicyOwner(baseline, initial, authority)
	if err != nil {
		t.Fatal(err)
	}
	svc.WithSearchPolicyOwner(owner)
	srv := newConnectTestServer(t, svc, nil)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	for _, key := range []string{"doc:a", "doc:b", "doc:c"} {
		if _, err := raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "needle phrase"}}})); err != nil {
			t.Fatal(err)
		}
	}
	request := &pb.SearchVerticesRequest{Query: "needle", Prefix: "doc:", Limit: 1}
	page, err := raw.SearchVertices(ctx, connect.NewRequest(request))
	if err != nil || len(page.Msg.GetHits()) != 1 || len(page.Msg.GetNextCursor()) == 0 {
		t.Fatalf("initial page = %+v/%v", page, err)
	}
	cursor := page.Msg.GetNextCursor()
	indexBefore := cache.SearchIndexMemoryStats()
	nextPolicy := policy
	nextPolicy.DefaultMode = search.MatchAll
	next, err := service.ResolveSearchPolicy(baseline, 11, nextPolicy)
	if err != nil {
		t.Fatal(err)
	}
	// Native desired publication can precede the owner's asynchronous callback.
	// Direct callers ignoring readiness still cannot use the old engine.
	authority.set(next.Version)
	if _, err := raw.SearchVertices(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("callback-gap direct Search accepted: %v", err)
	}
	if err := owner.StageDesired(next); err != nil {
		t.Fatal(err)
	}
	if !owner.Status().PendingRestart {
		t.Fatal("staged policy claimed effective")
	}
	if _, err := raw.SearchVertices(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("pending direct Search accepted: %v", err)
	}
	status, err := raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil || status.Msg.GetSearch().GetConfigFingerprint() != initial.Version.Fingerprint {
		t.Fatalf("pending effective status = %+v/%v", status, err)
	}
	// Status and ordinary non-search data operations remain reachable while the
	// query engine is pending. No process or graph restart is needed.
	if _, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "doc:a"})); err != nil {
		t.Fatal(err)
	}
	if err := owner.Restart(next); err != nil {
		t.Fatal(err)
	}
	resumed := &pb.SearchVerticesRequest{Query: "needle", Prefix: "doc:", Limit: 1, Cursor: cursor}
	if _, err := raw.SearchVertices(ctx, connect.NewRequest(resumed)); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("old engine cursor retained: %v", err)
	}
	fresh, err := raw.SearchVertices(ctx, connect.NewRequest(request))
	if err != nil || len(fresh.Msg.GetHits()) != 1 || len(fresh.Msg.GetNextCursor()) == 0 {
		t.Fatalf("new engine page = %+v/%v", fresh, err)
	}
	resumed.Cursor = fresh.Msg.GetNextCursor()
	if _, err := raw.SearchVertices(ctx, connect.NewRequest(resumed)); err != nil {
		t.Fatalf("new engine cursor refused: %v", err)
	}
	for _, mutate := range []func(*pb.SearchVerticesRequest){
		func(r *pb.SearchVerticesRequest) { r.Prefix = "other:" },
		func(r *pb.SearchVerticesRequest) {
			r.Cursor = append(append([]byte(nil), r.Cursor[:len(r.Cursor)-1]...), r.Cursor[len(r.Cursor)-1]^1)
		},
	} {
		bad := &pb.SearchVerticesRequest{Query: request.Query, Prefix: request.Prefix, Limit: 1, Cursor: append([]byte(nil), fresh.Msg.GetNextCursor()...)}
		mutate(bad)
		if _, err := raw.SearchVertices(ctx, connect.NewRequest(bad)); connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Fatalf("cursor security binding weakened: %v", err)
		}
	}
	indexAfter := cache.SearchIndexMemoryStats()
	if cache.VertexCount() != 3 || indexAfter.Generation != indexBefore.Generation || indexAfter.RebuildCount != indexBefore.RebuildCount || indexAfter.Documents != indexBefore.Documents {
		t.Fatalf("engine replacement changed graph/index: before=%+v after=%+v", indexBefore, indexAfter)
	}
	if svc.SearchConfigFingerprint() != next.Version.Fingerprint || owner.Status().PendingRestart {
		t.Fatal("effective generation not activated")
	}
}

type searchPolicyBlockingBackend struct {
	service.Backend
	entered chan struct{}
	proceed <-chan struct{}
	once    sync.Once
}

func (b *searchPolicyBlockingBackend) SearchVerticesMatchContext(ctx context.Context, query string, limit int, prefix string, options search.MatchOptions, phrase bool, budget search.Budget) ([]search.Result[string], search.Stats, error) {
	b.once.Do(func() { close(b.entered) })
	select {
	case <-b.proceed:
	case <-ctx.Done():
		return nil, search.Stats{}, ctx.Err()
	}
	return b.Backend.SearchVerticesMatchContext(ctx, query, limit, prefix, options, phrase, budget)
}

func TestSearchPolicyRealConnectRefusesOutputAcrossTransition(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	limits := productionSearchLimits(true, true)
	cache := newProductionSearchCache(time.Minute, true, true, limits.AnalysisLimits)
	if err := cache.PutVertex("doc", &pb.Vertex{Key: "doc", Value: &pb.Vertex_String_{String_: "needle"}}); err != nil {
		t.Fatal(err)
	}
	entered, proceed := make(chan struct{}), make(chan struct{})
	backend := &searchPolicyBlockingBackend{Backend: cache, entered: entered, proceed: proceed}
	svc := service.NewLanternService(backend).WithSearchLimits(limits)
	baseline := svc.SearchPolicyBaseline()
	initial, err := service.ResolveSearchPolicy(baseline, 1, service.SearchRequestPolicyFromLimits(baseline))
	if err != nil {
		t.Fatal(err)
	}
	owner, err := service.NewSearchPolicyOwner(baseline, initial, nil)
	if err != nil {
		t.Fatal(err)
	}
	svc.WithSearchPolicyOwner(owner)
	srv := newConnectTestServer(t, svc, nil)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	result := make(chan error, 1)
	go func() {
		_, err := raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "needle"}))
		result <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	nextPolicy := initial.Policy
	nextPolicy.MaxQueryBytes--
	next, err := service.ResolveSearchPolicy(baseline, 2, nextPolicy)
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.StageDesired(next); err != nil {
		t.Fatal(err)
	}
	if err := owner.Restart(next); err != nil {
		t.Fatal(err)
	}
	close(proceed)
	if err := <-result; connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("old generation output escaped: %v", err)
	}
	if _, err := raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "needle"})); err != nil {
		t.Fatalf("new generation request: %v", err)
	}
}
