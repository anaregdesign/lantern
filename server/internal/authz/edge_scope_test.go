package authz

import "testing"

func TestEdgeReadCandidatesUseReadUnion(t *testing.T) {
	p, err := CompileRoles([]Role{{ID: "read", Rules: []PermissionRule{dataRule(Read, "tails:"), dataRule(Read, "heads:visible:")}}, {ID: "write", Rules: []PermissionRule{dataRule(Write, "heads:")}}}, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := p.ForRoles([]string{"read", "write"})
	scope := a.EdgeCandidateScope(EdgeRead)
	if !scope.Contains("heads:visible:1") || scope.Contains("heads:hidden:1") || scope.Contains("other:1") {
		t.Fatal("read candidate inherited write")
	}
	for _, op := range []Action{CDCIdentity, CDCValue, Export, ReceiptRead} {
		if !a.AllowsEdgeAction(op, "tails:1", "heads:visible:1") || a.AllowsEdgeAction(op, "tails:1", "heads:hidden:1") {
			t.Fatal("disclosure mapping", op)
		}
	}
	for _, op := range []Action{"unknown", OperationsRead} {
		if !a.EdgeCandidateScope(op).Empty() || a.AllowsEdgeAction(op, "tails:1", "heads:1") {
			t.Fatal("unknown Edge authority")
		}
	}
}
