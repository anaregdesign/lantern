package service

import (
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestDataAuthorizationExactMatrixAndProtectedCollections(t *testing.T) {
	var rules []security.PermissionRule
	for i, action := range []security.Action{security.VertexRead, security.VertexWrite, security.VertexDelete, security.EdgeRead, security.EdgeWrite, security.EdgeAdd, security.EdgeDelete} {
		rules = append(rules, dataAccessRule(string(rune('a'+i)), security.Allow, action, "orders:"))
	}
	rules = append(rules, dataAccessRule("private", security.Deny, security.VertexRead, "orders:private:"))
	clock, contexts := dataAccessFixture(t, rules)
	now := clock()
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace().WithDataAuthorization(clock)
	ctx := contexts("reader", now)
	for name, message := range map[string]proto.Message{
		"get": &pb.GetVertexRequest{Key: "orders:1"}, "put": &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:1"}}, "delete": &pb.DeleteVertexRequest{Key: "orders:1"},
		"get edge": &pb.GetEdgeRequest{Tail: "orders:a", Head: "orders:b"}, "put edge": &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "orders:a", Head: "orders:b"}}, "add edge": &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "orders:a", Head: "orders:b"}}, "delete edge": &pb.DeleteEdgeRequest{Tail: "orders:a", Head: "orders:b"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.authorizeData(ctx, message); err != nil {
				t.Fatal(err)
			}
		})
	}
	for name, message := range map[string]proto.Message{
		"mixed get":              &pb.GetVerticesRequest{Keys: []string{"orders:1", "orders:private:1"}},
		"mixed put":              &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:1"}, {Key: "other:1"}}},
		"cross-prefix edge":      &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "orders:a", Head: "other:b"}},
		"read Deny beats delete": &pb.DeleteVertexRequest{Key: "orders:private:1"},
		"ops":                    &pb.GetServerStatusRequest{},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := svc.authorizeData(ctx, message); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal(err)
			}
		})
	}
	// Security administrators receive no implicit data or operations grants.
	if _, err := svc.authorizeData(contexts("admin", now), &pb.GetVertexRequest{Key: "orders:1"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("admin inherited data", err)
	}
	if svc.denyVertexLifecycle(ctx, "data:orders:1") || !svc.denyVertexLifecycle(ctx, "data:orders:private:1") || !svc.denyEdgeLifecycle(ctx, "data:orders:1", "data:other:1") {
		t.Fatal("effect guard ignored current logical permissions")
	}
	for name := range publicDataRequests {
		messageType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName("graph.v1." + name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := svc.authorizeData(ctx, messageType.New().Interface()); connect.CodeOf(err) == connect.CodeUnimplemented {
			t.Fatal("missing action matrix entry", name)
		}
	}
	unknown := &pb.GetVertexRequest{Key: "orders:1"}
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
	if _, err := svc.authorizeData(ctx, unknown); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown request field ignored", err)
	}
}

func TestDataExportDoesNotInventInteractiveAuthentication(t *testing.T) {
	clock, contexts := dataAccessFixture(t, []security.PermissionRule{
		dataAccessRule("read", security.Allow, security.VertexRead, "orders:"),
		dataAccessRule("export", security.Allow, security.Export, "orders:"),
	})
	svc := NewLanternService(graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)).WithDataNamespace().WithDataAuthorization(clock)
	if _, err := svc.authorizeData(contexts("reader", time.Time{}), &pb.BackupSnapshotRequest{VertexPrefix: "orders:"}); err != nil {
		t.Fatal("scoped export demanded interactive auth", err)
	}
}
