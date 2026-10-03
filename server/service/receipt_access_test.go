package service

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestReceiptResourceRequiresOriginalActionsAndDenyWins(t *testing.T) {
	prefix, private := "orders:", "orders:private:"
	actions := []security.Action{security.VertexRead, security.VertexWrite, security.VertexDelete, security.EdgeRead, security.EdgeAdd, security.EdgeWrite, security.EdgeDelete, security.ReceiptRead}
	for _, removed := range append([]security.Action{""}, actions...) {
		var rules []security.PermissionRule
		for _, action := range actions {
			if action != removed {
				rules = append(rules, security.PermissionRule{Effect: security.Allow, Action: action, Resource: security.DataResource, Prefix: &prefix})
			}
		}
		rules = append(rules, security.PermissionRule{Effect: security.Deny, Action: security.VertexRead, Resource: security.DataResource, Prefix: &private})
		policy, err := security.CompileRoles([]security.Role{{ID: "writer", Rules: rules}}, security.DefaultPolicyLimits())
		if err != nil {
			t.Fatal(err)
		}
		access, err := policy.ForRoles([]string{"writer"})
		if err != nil {
			t.Fatal(err)
		}
		for _, test := range []struct {
			kind     mutationreceipt.Kind
			head     string
			reduced  bool
			required []security.Action
		}{
			{mutationreceipt.PutVertex, "", false, []security.Action{security.VertexRead, security.VertexWrite, security.ReceiptRead}},
			{mutationreceipt.PutVertex, "", true, []security.Action{security.VertexRead, security.VertexWrite, security.VertexDelete, security.ReceiptRead}},
			{mutationreceipt.DeleteVertex, "", false, []security.Action{security.VertexRead, security.VertexDelete, security.ReceiptRead}},
			{mutationreceipt.AddEdge, "orders:b", false, []security.Action{security.VertexRead, security.VertexWrite, security.EdgeRead, security.EdgeAdd, security.ReceiptRead}},
			{mutationreceipt.DeleteEdge, "orders:b", false, []security.Action{security.VertexRead, security.EdgeRead, security.EdgeDelete, security.ReceiptRead}},
			{mutationreceipt.DeleteEdgeContribution, "orders:b", false, []security.Action{security.VertexRead, security.EdgeRead, security.EdgeDelete, security.ReceiptRead}},
		} {
			row := mutationreceipt.Receipt{Intent: mutationreceipt.Intent{Kind: test.kind, Resource: mutationreceipt.ResourceIdentity{Key: "orders:a", Head: test.head}}, LifecycleReduction: test.reduced}
			want := true
			for _, required := range test.required {
				want = want && required != removed
			}
			if got := allowsReceiptResource(access, row); got != want {
				t.Fatal(test.kind, test.reduced, removed, got, want)
			}
			row.Resource.Key = "orders:private:a"
			if allowsReceiptResource(access, row) {
				t.Fatal("hidden resource admitted")
			}
			row.Resource = mutationreceipt.ResourceIdentity{}
			if allowsReceiptResource(access, row) || wholeReceiptAbsence(access) {
				t.Fatal("unknown/legacy evidence admitted for scoped Role")
			}
		}
		if err := authorizeReceiptRequest(access, &pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "orders:a", Head: "outside:b"}}}); connect.CodeOf(err) != connect.CodePermissionDenied {
			t.Fatal("unproven requested endpoint bypassed ReceiptRead", err)
		}
	}
}
