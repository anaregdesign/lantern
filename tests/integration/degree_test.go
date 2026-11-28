package integration_test

import (
	"cmp"
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// TestTopVerticesByDegree_E2E drives the #900 aggregate RPC full-stack against
// a real GraphCache: seed a prefixed subgraph via the SDK, then rank it through
// the raw Connect client (there is no SDK wrapper — the RPC is server-side
// only). It also pins the empty-prefix guard and the live-visibility rule.
func TestTopVerticesByDegree_E2E(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	svc := service.NewLanternService(cache)
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	sdk := newConnectClientFor(t, srv.url)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, k := range []string{"n:a", "n:b", "n:c", "n:d", "x:1", "x:2"} {
		if _, err := sdk.PutVertex(ctx, k, k, time.Minute); err != nil {
			t.Fatalf("PutVertex %s: %v", k, err)
		}
	}
	edges := []struct {
		tail, head string
		w          float32
	}{
		{"n:a", "n:b", 1}, {"n:a", "n:c", 2}, {"n:a", "x:1", 4},
		{"n:b", "n:c", 1}, {"x:2", "n:c", 8}, {"x:2", "n:a", 1},
	}
	for _, e := range edges {
		if _, err := sdk.AddEdge(ctx, e.tail, e.head, e.w, time.Minute); err != nil {
			t.Fatalf("AddEdge %s->%s: %v", e.tail, e.head, err)
		}
	}

	top := func(t *testing.T, req *pb.TopVerticesByDegreeRequest) []*pb.TopVerticesByDegreeResponse_Entry {
		t.Helper()
		resp, err := raw.TopVerticesByDegree(ctx, connect.NewRequest(req))
		if err != nil {
			t.Fatalf("TopVerticesByDegree(%+v): %v", req, err)
		}
		return resp.Msg.GetEntries()
	}

	t.Run("OutByCount", func(t *testing.T) {
		got := top(t, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 2, Direction: pb.TopVerticesByDegreeRequest_DIRECTION_OUT,
		})
		if len(got) != 2 {
			t.Fatalf("entries = %d, want 2: %v", len(got), got)
		}
		if got[0].GetKey() != "n:a" || got[0].GetDegree() != 3 || got[0].GetWeightedDegree() != 7 {
			t.Errorf("entry[0] = %+v, want n:a 3/7", got[0])
		}
		if got[1].GetKey() != "n:b" || got[1].GetDegree() != 1 || got[1].GetWeightedDegree() != 1 {
			t.Errorf("entry[1] = %+v, want n:b 1/1", got[1])
		}
	})

	t.Run("InByWeight", func(t *testing.T) {
		got := top(t, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 1, Direction: pb.TopVerticesByDegreeRequest_DIRECTION_IN, Weighted: true,
		})
		if len(got) != 1 || got[0].GetKey() != "n:c" || got[0].GetWeightedDegree() != 11 || got[0].GetDegree() != 3 {
			t.Fatalf("in-by-weight top1 = %v, want n:c 3/11", got)
		}
	})

	t.Run("BothByCount", func(t *testing.T) {
		got := top(t, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 3, Direction: pb.TopVerticesByDegreeRequest_DIRECTION_BOTH,
		})
		if len(got) != 3 {
			t.Fatalf("entries = %d, want 3: %v", len(got), got)
		}
		wantKeys := []string{"n:a", "n:c", "n:b"}
		wantDeg := []uint64{4, 3, 2}
		for i := range wantKeys {
			if got[i].GetKey() != wantKeys[i] || got[i].GetDegree() != wantDeg[i] {
				t.Errorf("entry[%d] = %+v, want %s degree %d", i, got[i], wantKeys[i], wantDeg[i])
			}
		}
	})

	t.Run("EmptyPrefixRejected", func(t *testing.T) {
		_, err := raw.TopVerticesByDegree(ctx, connect.NewRequest(&pb.TopVerticesByDegreeRequest{K: 5}))
		if err == nil {
			t.Fatal("empty prefix accepted; want InvalidArgument")
		}
		if connect.CodeOf(err) != connect.CodeInvalidArgument {
			t.Errorf("empty-prefix code = %v, want InvalidArgument", connect.CodeOf(err))
		}
	})

	t.Run("LiveVisibilityAfterDelete", func(t *testing.T) {
		if _, err := sdk.DeleteVertex(ctx, "n:b"); err != nil {
			t.Fatalf("DeleteVertex n:b: %v", err)
		}
		got := top(t, &pb.TopVerticesByDegreeRequest{
			Prefix: "n:", K: 5, Direction: pb.TopVerticesByDegreeRequest_DIRECTION_OUT,
		})
		// n:b is gone as a candidate, and n:a's edge into the now-dead n:b no
		// longer counts, so n:a's out-degree drops 3->2 (n:c + x:1) / weight 7->6.
		for _, e := range got {
			if e.GetKey() == "n:b" {
				t.Errorf("deleted vertex n:b still ranked: %+v", e)
			}
			if e.GetKey() == "n:a" && (e.GetDegree() != 2 || e.GetWeightedDegree() != 6) {
				t.Errorf("n:a after delete = %+v, want degree 2 / weight 6", e)
			}
		}
	})
}

func TestTarpGraphTraversalAndOutgoingDegreeAfterReopen(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "graph")
	open := func(t *testing.T) *service.ServingRuntime {
		t.Helper()
		cache := provider.NewGraphCache(provider.CacheConfig{CacheSettings: provider.CacheSettings{TTL: time.Hour}}, provider.SearchConfig{})
		runtime, err := service.OpenGraphDatabase(t.Context(), dir, hlc.NodeID{1}, cache, mutationlog.Options{Capacity: 16}, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		return runtime
	}
	t.Run("seed", func(t *testing.T) {
		srv := newConnectTestServer(t, open(t).NewExecutor(nil), nil)
		rpc := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
		if _, err := rpc.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
			{Key: "doc:seed", Value: &pb.Vertex_String_{String_: "seed"}},
			{Key: "doc:a", Value: &pb.Vertex_String_{String_: "a"}},
			{Key: "doc:b", Value: &pb.Vertex_String_{String_: "b"}},
			{Key: "out:c", Value: &pb.Vertex_String_{String_: "c"}},
		}})); err != nil {
			t.Fatal(err)
		}
		if _, err := rpc.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{
			{Tail: "doc:seed", Head: "doc:a", Weight: 3},
			{Tail: "doc:seed", Head: "doc:b", Weight: 1},
			{Tail: "doc:seed", Head: "out:c", Weight: 10},
		}})); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("reopen", func(t *testing.T) {
		srv := newConnectTestServer(t, open(t).NewExecutor(nil), nil)
		rpc := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
		walk, err := rpc.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{
			Seed: "doc:seed", VertexPrefix: "doc:",
			Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}},
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got := walk.Msg.GetGraph().GetEdges(); len(got) != 1 || got[0].GetHead() != "doc:a" || got[0].GetWeight() != 3 {
			t.Fatalf("unexpected scoped BFS edges: %v", got)
		}
		if _, err := rpc.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "doc:a"})); err != nil {
			t.Fatal(err)
		}
		degree, err := rpc.TopVerticesByDegree(ctx, connect.NewRequest(&pb.TopVerticesByDegreeRequest{
			Prefix: "doc:", K: 1, Direction: pb.TopVerticesByDegreeRequest_DIRECTION_OUT, Weighted: true,
		}))
		if err != nil {
			t.Fatal(err)
		}
		if got := degree.Msg.GetEntries(); len(got) != 1 || got[0].GetKey() != "doc:seed" || got[0].GetDegree() != 2 || got[0].GetWeightedDegree() != 11 {
			t.Fatalf("unexpected outgoing degree after endpoint deletion: %v", got)
		}
	})
}

func TestTarpWeightedGraphQueriesMatchRAMAfterReopen(t *testing.T) {
	ctx := t.Context()
	dir := filepath.Join(t.TempDir(), "graph")
	newCache := func() *graphcache.GraphCache[string, *pb.Vertex] {
		return provider.NewGraphCache(provider.CacheConfig{CacheSettings: provider.CacheSettings{TTL: time.Hour}}, provider.SearchConfig{})
	}
	open := func(t *testing.T) *service.ServingRuntime {
		t.Helper()
		runtime, err := service.OpenGraphDatabase(ctx, dir, hlc.NodeID{1}, newCache(), mutationlog.Options{Capacity: 16}, "")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		return runtime
	}
	rpc := func(t *testing.T, svc *service.LanternService) graphv1connect.LanternServiceClient {
		t.Helper()
		srv := newConnectTestServer(t, svc, nil)
		return graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	}
	ram := rpc(t, service.NewLanternService(newCache()))
	expiry := timestamppb.New(time.Now().Add(time.Hour))
	seed := func(t *testing.T, client graphv1connect.LanternServiceClient) {
		t.Helper()
		vertices := &pb.PutVerticesRequest{}
		for _, key := range []string{"doc:seed", "doc:猫", "doc:b\x00c", "other:z"} {
			vertices.Vertices = append(vertices.Vertices, &pb.Vertex{
				Key: key, Value: &pb.Vertex_String_{String_: key}, Expiration: expiry,
			})
		}
		if _, err := client.PutVertices(ctx, connect.NewRequest(vertices)); err != nil {
			t.Fatal(err)
		}
		edges := &pb.PutEdgesRequest{Edges: []*pb.Edge{
			{Tail: "doc:seed", Head: "doc:猫", Weight: 3, Expiration: expiry},
			{Tail: "doc:seed", Head: "doc:b\x00c", Weight: 2, Expiration: expiry},
			{Tail: "doc:b\x00c", Head: "doc:猫", Weight: 5, Expiration: expiry},
			{Tail: "doc:猫", Head: "doc:seed", Weight: 1, Expiration: expiry},
			{Tail: "doc:猫", Head: "doc:猫", Weight: 4, Expiration: expiry},
			{Tail: "other:z", Head: "doc:猫", Weight: 7, Expiration: expiry},
		}}
		if _, err := client.PutEdges(ctx, connect.NewRequest(edges)); err != nil {
			t.Fatal(err)
		}
	}
	seed(t, ram)
	t.Run("seed", func(t *testing.T) {
		seed(t, rpc(t, open(t).NewExecutor(nil)))
	})
	t.Run("reopen", func(t *testing.T) {
		durable := rpc(t, open(t).NewExecutor(nil))
		for _, weighting := range []pb.Weighting{pb.Weighting_WEIGHTING_RAW, pb.Weighting_WEIGHTING_TFIDF, pb.Weighting_WEIGHTING_BM25} {
			for _, tc := range []struct {
				name string
				set  func(*pb.IlluminateRequest)
			}{
				{"BFS", func(r *pb.IlluminateRequest) {
					r.Params = &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 2, FanOut: 2}}
				}},
				{"PPR", func(r *pb.IlluminateRequest) { r.Params = &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 3}} }},
				{"community", func(r *pb.IlluminateRequest) {
					r.Params = &pb.IlluminateRequest_Community{Community: &pb.LocalCommunityParams{MaxSize: 3}}
				}},
			} {
				t.Run(weighting.String()+"/"+tc.name, func(t *testing.T) {
					req := &pb.IlluminateRequest{Seed: "doc:seed", VertexPrefix: "doc:", Weighting: weighting}
					tc.set(req)
					got, err := durable.Illuminate(ctx, connect.NewRequest(req))
					if err != nil {
						t.Fatal(err)
					}
					want, err := ram.Illuminate(ctx, connect.NewRequest(req))
					if err != nil {
						t.Fatal(err)
					}
					sortIlluminateGraph(got.Msg.GetGraph())
					sortIlluminateGraph(want.Msg.GetGraph())
					if !proto.Equal(got.Msg, want.Msg) {
						t.Fatalf("Tarp response = %v; RAM response = %v", got.Msg, want.Msg)
					}
				})
			}
		}
		for _, direction := range []pb.TopVerticesByDegreeRequest_Direction{
			pb.TopVerticesByDegreeRequest_DIRECTION_IN, pb.TopVerticesByDegreeRequest_DIRECTION_BOTH,
		} {
			req := &pb.TopVerticesByDegreeRequest{Prefix: "doc:", K: 3, Direction: direction, Weighted: true}
			got, err := durable.TopVerticesByDegree(ctx, connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			want, err := ram.TopVerticesByDegree(ctx, connect.NewRequest(req))
			if err != nil {
				t.Fatal(err)
			}
			if !proto.Equal(got.Msg, want.Msg) {
				t.Fatalf("%v Tarp response = %v; RAM response = %v", direction, got.Msg, want.Msg)
			}
		}
	})
}

func sortIlluminateGraph(g *pb.Graph) {
	slices.SortFunc(g.GetVertices(), func(a, b *pb.Vertex) int { return cmp.Compare(a.GetKey(), b.GetKey()) })
	slices.SortFunc(g.GetEdges(), func(a, b *pb.Edge) int {
		if byTail := cmp.Compare(a.GetTail(), b.GetTail()); byTail != 0 {
			return byTail
		}
		return cmp.Compare(a.GetHead(), b.GetHead())
	})
}
