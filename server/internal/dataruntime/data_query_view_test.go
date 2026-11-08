package dataruntime

import (
	"reflect"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authz"
	"github.com/anaregdesign/lantern/server/internal/security"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestDataQueryContextCompilesTypedReadBeforeExecution(t *testing.T) {
	access := dataAccessFixture(t, []security.PermissionRule{dataAccessRule("read", security.VertexRead, "users:public:")})
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	for _, key := range []string{"data:users:public:1", "data:users:public:2", "data:users:private:1", "data:other:1", "sys:internal"} {
		if err := c.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	for _, request := range []proto.Message{&pb.SearchVerticesRequest{Prefix: "users:"}, &pb.IlluminateRequest{Seed: "users:public:1"}, &pb.ScanVerticesRequest{Prefix: "users:"}, &pb.ScanVertexKeysRequest{Prefix: "users:"}, &pb.CountVerticesByPrefixRequest{Prefix: "users:"}} {
		ctx, err := dataPolicyQueryContext(t.Context(), request, access)
		if err != nil || c.CountByPrefixContext(ctx, "data:") != 2 || c.CountByPrefixContext(ctx, "sys:") != 0 {
			t.Fatal("typed read did not become disjoint physical range", request, err)
		}
	}
	for _, prefix := range []string{"users:private:", "other:"} {
		if _, err := dataPolicyQueryContext(t.Context(), &pb.SearchVerticesRequest{Prefix: prefix}, access); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("empty authorized scope inspected data", prefix, err)
		}
	}
}

func TestDataQueryContextHeadOwnershipSeparatesReadsAndModifications(t *testing.T) {
	rules := []security.PermissionRule{
		dataAccessRule("read-tails", security.VertexRead, "users:"),
		dataAccessRule("read-heads", security.VertexRead, "targets:public:"),
		dataAccessRule("write-heads", security.VertexWrite, "targets:writable:"),
	}
	access := dataAccessFixtureWithMode(t, rules, authz.EdgeReadWrite)
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	pairs := [][2]string{{"users:1", "targets:public:1"}, {"targets:public:1", "users:1"}, {"users:1", "users:2"}, {"users:1", "targets:writable:private:1"}, {"targets:public:1", "outside:1"}, {"users:1", "targets:writable:hidden:1"}, {"users:1", "targets:public:protected:1"}}
	for _, pair := range pairs {
		c.AddEdge("data:"+pair[0], "data:"+pair[1], 1)
	}
	c.AddEdge("data:users:1", "sys:internal", 1)
	ctx := t.Context()
	for _, request := range []proto.Message{&pb.ScanEdgesRequest{}, &pb.IlluminateRequest{Seed: "users:1"}, &pb.TopVerticesByDegreeRequest{Prefix: "users:"}} {
		queryCtx, err := dataPolicyQueryContext(ctx, request, access)
		if err != nil {
			t.Fatal(request, err)
		}
		var got [][2]string
		if !c.ScanEdgesByPrefix(queryCtx, "data:", "data:", func(_, tail, _, head string, _ float32, _ time.Time) bool {
			got = append(got, [2]string{tail, head})
			return true
		}) || !reflect.DeepEqual(got, [][2]string{{"data:targets:public:1", "data:users:1"}, {"data:users:1", "data:targets:public:1"}, {"data:users:1", "data:targets:public:protected:1"}, {"data:users:1", "data:users:2"}}) {
			t.Fatal("write scope became read visibility or read edges required write", request, got)
		}
		graph, _, err := c.NeighborWithExpirationsContext(queryCtx, "data:users:1", 3, 10, graphcache.WeightingBM25, false, nil)
		if err != nil || len(graph.Vertices) != 4 || len(graph.Edges["data:users:1"]) != 3 {
			t.Fatal("hidden endpoint contributed a traversal path", graph, err)
		}
		degree := c.TopVerticesByDegreeContext(queryCtx, "data:users:1", 1, graphcache.DegreeOut, true)
		if len(degree) != 1 || degree[0].Degree != 3 || degree[0].WeightedDegree != 3 {
			t.Fatal("hidden endpoint contributed actual degree", degree)
		}
	}
	mutationCtx, err := dataPolicyQueryContext(ctx, &pb.DeleteEdgesByPrefixRequest{}, access)
	if err != nil {
		t.Fatal(err)
	}
	var got [][2]string
	c.ScanEdgesByPrefix(mutationCtx, "data:", "data:", func(_, tail, _, head string, _ float32, _ time.Time) bool {
		got = append(got, [2]string{tail, head})
		return true
	})
	if !reflect.DeepEqual(got, [][2]string{{"data:users:1", "data:targets:writable:hidden:1"}, {"data:users:1", "data:targets:writable:private:1"}}) {
		t.Fatal("global endpoint mode confused modification authority", got)
	}
	if _, err = dataPolicyQueryContext(ctx, &pb.DeleteEdgesByPrefixRequest{HeadPrefix: "users:"}, access); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reverse head authority depended on stored state", err)
	}
}

func TestPrefixEdgeModificationUsesEveryGlobalMode(t *testing.T) {
	rules := []security.PermissionRule{
		dataAccessRule("read", security.VertexRead, "r:"),
		dataAccessRule("write", security.VertexWrite, "w:"),
		dataAccessRule("both-read", security.VertexRead, "b:"),
		dataAccessRule("both-write", security.VertexWrite, "b:"),
	}
	for _, mode := range []authz.EdgeEndpointMode{authz.EdgeReadRead, authz.EdgeWriteWrite, authz.EdgeReadWrite, authz.EdgeWriteRead} {
		t.Run(string(mode), func(t *testing.T) {
			access := dataAccessFixtureWithMode(t, rules, mode)
			c := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
			c.EnablePrefixIndex(func(key string) string { return key })
			for _, tail := range []string{"r:1", "w:1", "b:1", "hidden:1"} {
				for _, head := range []string{"r:2", "w:2", "b:2", "hidden:2"} {
					c.AddEdge("data:"+tail, "data:"+head, 1)
				}
			}
			ctx, err := dataPolicyQueryContext(t.Context(), &pb.DeleteEdgesByPrefixRequest{DryRun: true}, access)
			if err != nil {
				t.Fatal(err)
			}
			selected := map[[2]string]bool{}
			c.ScanEdgesByPrefix(ctx, "data:", "data:", func(_, tail, _, head string, _ float32, _ time.Time) bool {
				selected[[2]string{tail, head}] = true
				return true
			})
			for _, tail := range []string{"r:1", "w:1", "b:1", "hidden:1"} {
				for _, head := range []string{"r:2", "w:2", "b:2", "hidden:2"} {
					if selected[[2]string{"data:" + tail, "data:" + head}] != access.AllowsEdge(security.EdgeDelete, tail, head) {
						t.Fatal("Core filter disagrees with captured global mode", mode, tail, head)
					}
				}
			}
			for _, prefix := range []string{"r:", "w:", "b:"} {
				req := &pb.DeleteEdgesByPrefixRequest{TailPrefix: prefix, HeadPrefix: prefix}
				tailAction, headAction := access.EdgeEndpointActions()
				wantBlind := !access.Scope(tailAction).Within(prefix).IsSubsetOf(access.Scope(security.VertexRead)) || !access.Scope(headAction).Within(prefix).IsSubsetOf(access.Scope(security.VertexRead))
				if blindMutationRequired(access, req) != wantBlind {
					t.Fatal("prefix disclosure disagrees with endpoint mode", mode, prefix)
				}
			}
		})
	}
}
