package integration_test

import (
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
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
		if err := sdk.PutVertex(ctx, k, k, time.Minute); err != nil {
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
