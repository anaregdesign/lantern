package dataruntime

import (
	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authz"
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
)

func TestReceiptVertexWriteAndDisclosureAreIndependent(t *testing.T) {
	for _, readable := range []bool{false, true} {
		for _, writable := range []bool{false, true} {
			var rules []security.PermissionRule
			if readable {
				rules = append(rules, dataAccessRule("read", security.VertexRead, "orders:public:"))
			}
			if writable {
				rules = append(rules, dataAccessRule("write", security.VertexWrite, "orders:public:"))
			}
			access := dataAccessFixture(t, rules)
			for _, kind := range []mutationreceipt.Kind{mutationreceipt.PutVertex, mutationreceipt.DeleteVertex} {
				for _, reduced := range []bool{false, true} {
					row := mutationreceipt.Receipt{Intent: mutationreceipt.Intent{Kind: kind, Resource: mutationreceipt.ResourceIdentity{Key: "orders:public:1"}}, LifecycleReduction: reduced}
					if allowsReceiptMutationResource(access, row) != writable || allowsReceiptResource(access, row) != (readable && writable) {
						t.Fatal(kind, reduced, readable, writable)
					}
					row.Resource.Key = "orders:private:1"
					if allowsReceiptMutationResource(access, row) || allowsReceiptResource(access, row) {
						t.Fatal("ungranted key accepted")
					}
					row.Resource = mutationreceipt.ResourceIdentity{}
					if allowsReceiptMutationResource(access, row) || allowsReceiptResource(access, row) || wholeReceiptAbsence(access) {
						t.Fatal("scoped grant disclosed unknown history")
					}
				}
			}
			request := &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "orders:public:1"}, ReceiptContext: &pb.MutationReceiptContext{}}
			if err := authorizeReceiptRequest(access, request); (err == nil) != writable {
				t.Fatal("receipt context changed mutation authority", err)
			}
			if _, err := planDataPermissions(access, request); (err == nil) != writable {
				t.Fatal("receipt mutation ingress added read", err)
			}
			if _, err := planDataPermissions(access, &pb.GetReceiptCapabilityRequest{}); (err == nil) != (readable || writable) {
				t.Fatal("static receipt capability requires data grant", err)
			}
			if _, err := planDataPermissions(access, &pb.GetReceiptStatusRequest{}); (err == nil) != readable {
				t.Fatal("status ingress lost separate read", err)
			}
		}
	}
}

func TestReceiptGlobalEdgeAuthorityAndDisclosureAreIndependent(t *testing.T) {
	for _, mode := range []authz.EdgeEndpointMode{authz.EdgeReadRead, authz.EdgeWriteWrite, authz.EdgeReadWrite, authz.EdgeWriteRead} {
		for _, headReadable := range []bool{false, true} {
			rules := []security.PermissionRule{dataAccessRule("tail-read", security.VertexRead, "users:"), dataAccessRule("tail-write", security.VertexWrite, "users:"), dataAccessRule("head-write", security.VertexWrite, "targets:public:")}
			if headReadable {
				rules = append(rules, dataAccessRule("head-read", security.VertexRead, "targets:public:"))
			}
			access := dataAccessFixtureWithMode(t, rules, mode)
			for _, kind := range []mutationreceipt.Kind{mutationreceipt.CreateEdge, mutationreceipt.AddEdge, mutationreceipt.PutEdge, mutationreceipt.DeleteEdge, mutationreceipt.DeleteEdgeContribution} {
				for _, pair := range [][2]string{{"users:1", "targets:public:1"}, {"targets:public:1", "users:1"}, {"users:1", "users:2"}, {"users:1", "targets:private:1"}} {
					row := mutationreceipt.Receipt{Intent: mutationreceipt.Intent{Kind: kind, Resource: mutationreceipt.ResourceIdentity{Key: pair[0], Head: pair[1]}}}
					handled := access.AllowsEdge(security.EdgeAdd, pair[0], pair[1])
					disclosed := handled && access.AllowsEdge(security.EdgeRead, pair[0], pair[1])
					if allowsReceiptMutationResource(access, row) != handled || allowsReceiptResource(access, row) != disclosed {
						t.Fatal(mode, kind, headReadable, pair)
					}
				}
			}
			if err := authorizeReceiptRequest(access, &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "users:1", Head: "outside:1"}}}); connect.CodeOf(err) != connect.CodePermissionDenied {
				t.Fatal("unknown endpoint admitted", err)
			}
		}
	}
}
