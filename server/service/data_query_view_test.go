package service

import (
	"github.com/anaregdesign/lantern/server/internal/security"
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
		dataAccessRule("edge", security.Allow, security.EdgeRead, "users:"),
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
