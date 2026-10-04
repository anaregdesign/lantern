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

func TestReceiptDirectedPairCannotComposeOrReverseOriginalActions(t *testing.T) {
	rules := []security.PermissionRule{dataAccessRule("read", security.Allow, security.VertexRead, "")}
	for i, action := range []security.Action{security.ReceiptRead, security.EdgeRead, security.EdgeDelete} {
		rules = append(rules, dataAccessPairRule(string(rune('a'+i)), security.Allow, action, "users:", "targets:"))
	}
	rules = append(rules, dataAccessPairRule("private", security.Deny, security.ReceiptRead, "users:", "targets:private:"))
	policy, err := security.CompileRoles([]security.Role{{ID: "delete", Rules: rules}}, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, _ := policy.ForRoles([]string{"delete"})
	for _, test := range []struct {
		tail, head string
		allowed    bool
	}{{"users:1", "targets:1", true}, {"targets:1", "users:1", false}, {"users:1", "users:2", false}, {"users:1", "targets:private:1", false}} {
		row := mutationreceipt.Receipt{Intent: mutationreceipt.Intent{Kind: mutationreceipt.DeleteEdge, Resource: mutationreceipt.ResourceIdentity{Key: test.tail, Head: test.head}}}
		if allowsReceiptResource(access, row) != test.allowed || (authorizeReceiptRequest(access, &pb.DeleteEdgeRequest{Tail: test.tail, Head: test.head}) == nil) != test.allowed {
			t.Fatal("receipt pair broadened original resource/action", test)
		}
		row.Kind = mutationreceipt.AddEdge
		if allowsReceiptResource(access, row) {
			t.Fatal("Delete pair disclosed Add result")
		}
	}
	if wholeReceiptAbsence(access) {
		t.Fatal("pair-only receipt grant disclosed missing IDs")
	}
}
