package service

import (
	"github.com/anaregdesign/lantern/server/internal/security"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestDataQueryContextCompilesLogicalActionsBeforeExecution(t *testing.T) {
	rules := []security.PermissionRule{
		dataAccessRule("read", security.Allow, security.VertexRead, "users:"),
		dataAccessRule("other-read", security.Allow, security.VertexRead, "other:"),
		dataAccessRule("query", security.Allow, security.Query, "users:"),
		dataAccessRule("private", security.Deny, security.VertexRead, "users:private:"),
		dataAccessRule("noquery", security.Deny, security.Query, "users:noquery:"),
	}
	clock, contexts := dataAccessFixture(t, rules)
	now := clock()
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	for _, key := range []string{"data:users:1", "data:users:2", "data:users:private:1", "data:users:noquery:1", "data:other:1", "sys:internal"} {
		if err := c.PutVertex(key, &pb.Vertex{Key: key}); err != nil {
			t.Fatal(err)
		}
	}
	svc := NewLanternService(c).WithDataNamespace().WithDataAuthorization(clock)
	ctx := contexts("reader", now)
	admission, err := svc.dataAdmission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []proto.Message{&pb.SearchVerticesRequest{Prefix: "users:"}, &pb.IlluminateRequest{Seed: "users:1"}} {
		queryCtx, err := svc.dataQueryContext(ctx, request, admission)
		if err != nil || c.CountByPrefixContext(queryCtx, "data:") != 2 || c.CountByPrefixContext(queryCtx, "sys:") != 0 {
			t.Fatal("logical actions did not become disjoint physical ranges", request, err)
		}
	}
	for _, request := range []proto.Message{&pb.ScanVerticesRequest{Prefix: "users:"}, &pb.ScanVertexKeysRequest{Prefix: "users:"}, &pb.CountVerticesByPrefixRequest{Prefix: "users:"}} {
		readCtx, err := svc.dataQueryContext(ctx, request, admission)
		if err != nil || c.CountByPrefixContext(readCtx, "data:") != 4 || c.CountByPrefixContext(readCtx, "sys:") != 0 {
			t.Fatal("collection reads inherited Query scope", request, err)
		}
	}
	for _, prefix := range []string{"users:private:", "users:noquery:", "other:"} {
		if _, err := svc.dataQueryContext(ctx, &pb.SearchVerticesRequest{Prefix: prefix}, admission); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("empty authorized scope inspected data", prefix, err)
		}
	}
	if _, err := svc.authorizeData(ctx, &pb.SearchVerticesRequest{Prefix: "users:"}); err != nil {
		t.Fatal("shared statistics require whole-domain access", err)
	}
}

func TestDataQueryContextHeadOwnershipSeparatesReadsAndModifications(t *testing.T) {
	rules := []security.PermissionRule{
		dataAccessRule("read-tails", security.Allow, security.VertexRead, "users:"),
		dataAccessRule("read-heads", security.Allow, security.VertexRead, "targets:"),
		dataAccessRule("private-read", security.Deny, security.VertexRead, "targets:private:"),
		dataAccessRule("hidden-read", security.Deny, security.VertexRead, "targets:hidden:"),
		dataAccessRule("write-heads", security.Allow, security.VertexWrite, "targets:"),
		dataAccessRule("protected-write", security.Deny, security.VertexWrite, "targets:protected:"),
		dataAccessRule("query", security.Allow, security.Query, ""),
	}
	clock, contexts := dataAccessFixture(t, rules)
	c := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
	c.EnablePrefixIndex(func(key string) string { return key })
	pairs := [][2]string{{"users:1", "targets:1"}, {"targets:1", "users:1"}, {"users:1", "users:2"}, {"users:1", "targets:private:1"}, {"targets:1", "outside:1"}, {"users:1", "targets:hidden:1"}, {"users:1", "targets:protected:1"}}
	for _, pair := range pairs {
		c.AddEdge("data:"+pair[0], "data:"+pair[1], 1)
	}
	c.AddEdge("data:users:1", "sys:internal", 1)
	svc := NewLanternService(c).WithDataNamespace().WithDataAuthorization(clock)
	ctx := contexts("reader", clock())
	admission, err := svc.dataAdmission(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, request := range []proto.Message{&pb.ScanEdgesRequest{}, &pb.IlluminateRequest{Seed: "users:1"}, &pb.TopVerticesByDegreeRequest{Prefix: "users:"}} {
		queryCtx, err := svc.dataQueryContext(ctx, request, admission)
		if err != nil {
			t.Fatal(request, err)
		}
		var got [][2]string
		if !c.ScanEdgesByPrefix(queryCtx, "data:", "data:", func(_, tail, _, head string, _ float32, _ time.Time) bool {
			got = append(got, [2]string{tail, head})
			return true
		}) || !reflect.DeepEqual(got, [][2]string{{"data:targets:1", "data:users:1"}, {"data:users:1", "data:targets:1"}, {"data:users:1", "data:targets:protected:1"}, {"data:users:1", "data:users:2"}}) {
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
	mutationCtx, err := svc.dataQueryContext(ctx, &pb.DeleteEdgesByPrefixRequest{}, admission)
	if err != nil {
		t.Fatal(err)
	}
	var got [][2]string
	c.ScanEdgesByPrefix(mutationCtx, "data:", "data:", func(_, tail, _, head string, _ float32, _ time.Time) bool {
		got = append(got, [2]string{tail, head})
		return true
	})
	if !reflect.DeepEqual(got, [][2]string{{"data:users:1", "data:targets:1"}, {"data:users:1", "data:targets:hidden:1"}, {"data:users:1", "data:targets:private:1"}}) {
		t.Fatal("head write/read Deny confused modification authority", got)
	}
	if _, err = svc.dataQueryContext(ctx, &pb.DeleteEdgesByPrefixRequest{HeadPrefix: "users:"}, admission); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("reverse head authority depended on stored state", err)
	}
}
