package service

import (
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
)

func TestDataAuthorizationExactMatrixAndProtectedCollections(t *testing.T) {
	var rules []security.PermissionRule
	for i, action := range []security.Action{security.VertexRead, security.VertexWrite, security.VertexDelete} {
		rules = append(rules, dataAccessRule(string(rune('a'+i)), security.Allow, action, "orders:"))
	}
	rules = append(rules, dataAccessRule("private", security.Deny, security.VertexRead, "orders:private:"))
	access := dataAccessFixture(t, rules)
	for name, message := range map[string]proto.Message{
		"get": &pb.GetVertexRequest{Key: "orders:1"}, "put": &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:1"}}, "delete": &pb.DeleteVertexRequest{Key: "orders:1"},
		"get edge": &pb.GetEdgeRequest{Tail: "orders:a", Head: "orders:b"}, "put edge": &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "orders:a", Head: "orders:b"}}, "add edge": &pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "orders:a", Head: "orders:b"}}, "delete edge": &pb.DeleteEdgeRequest{Tail: "orders:a", Head: "orders:b"},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := planDataPermissions(access, message); err != nil {
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
			if _, err := planDataPermissions(access, message); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal(err)
			}
		})
	}
	// A management-only Role has no implicit data or operations grants.
	admin := dataAccessFixture(t, []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}})
	if _, err := planDataPermissions(admin, &pb.GetVertexRequest{Key: "orders:1"}); connect.CodeOf(err) != connect.CodePermissionDenied {
		t.Fatal("admin inherited data", err)
	}
	for name := range publicDataRequests {
		messageType, err := protoregistry.GlobalTypes.FindMessageByName(protoreflect.FullName("graph.v1." + name))
		if err != nil {
			t.Fatal(err)
		}
		if _, err := planDataPermissions(access, messageType.New().Interface()); connect.CodeOf(err) == connect.CodeUnimplemented {
			t.Fatal("missing action matrix entry", name)
		}
	}
	unknown := &pb.GetVertexRequest{Key: "orders:1"}
	unknown.ProtoReflect().SetUnknown([]byte{0xa0, 0x06, 1})
	if _, err := planDataPermissions(access, unknown); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatal("unknown request field ignored", err)
	}
}

func TestDataExportDoesNotInventInteractiveAuthentication(t *testing.T) {
	access := dataAccessFixture(t, []security.PermissionRule{
		dataAccessRule("read", security.Allow, security.VertexRead, "orders:"),
		dataAccessRule("export", security.Allow, security.Export, "orders:"),
	})
	if _, err := planDataPermissions(access, &pb.BackupSnapshotRequest{VertexPrefix: "orders:"}); err != nil {
		t.Fatal("scoped export demanded interactive auth", err)
	}
}
