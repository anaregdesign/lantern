package security

import "testing"

func TestHeadCandidateRangesAndCapabilityIntersection(t *testing.T) {
	roles := []Role{
		{ID: "read", Rules: []PermissionRule{dataRule(Allow, VertexRead, "tails:"), dataRule(Allow, VertexRead, "heads:visible:"), dataRule(Deny, VertexRead, "tails:private:")}},
		{ID: "write", Rules: []PermissionRule{dataRule(Allow, VertexWrite, "heads:"), dataRule(Deny, VertexWrite, "heads:protected:")}},
		{ID: "receipts", Rules: []PermissionRule{dataRule(Allow, ReceiptRead, ""), dataRule(Deny, ReceiptRead, "heads:sealed:")}},
	}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := policy.ForRoles([]string{"read", "write", "receipts"})
	if err != nil {
		t.Fatal(err)
	}
	for _, action := range []Action{EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete} {
		candidate := access.EdgeCandidateScope(action)
		for _, key := range []string{"tails:1", "heads:hidden:1", "heads:visible:1"} {
			if !candidate.Contains(key) {
				t.Fatal("candidate omitted an authorized endpoint", action, key)
			}
		}
		if candidate.Contains("tails:private:1") || candidate.Contains("heads:protected:1") || candidate.Contains("outside:1") {
			t.Fatal("candidate broadened endpoint authority")
		}
		if access.AllowsEdge(action, "heads:hidden:1", "tails:1") {
			t.Fatal("candidate union was mistaken for final oriented authority")
		}
	}
	read := access.EdgeCandidateScope(EdgeRead)
	if !read.Contains("heads:visible:1") || read.Contains("heads:hidden:1") {
		t.Fatal("read candidate inherited write visibility")
	}
	if !access.AllowsEdgeAction(ReceiptRead, "tails:1", "heads:hidden:1") || access.AllowsEdgeAction(ReceiptRead, "tails:1", "heads:sealed:1") {
		t.Fatal("independent endpoint capability or cross-Role Deny was lost")
	}
	restricted := access.EdgeCandidateScope(EdgeCreate, ReceiptRead)
	if restricted.Contains("heads:sealed:1") || !restricted.Contains("heads:hidden:1") {
		t.Fatal("capability candidate did not intersect")
	}
	for _, action := range []Action{"unknown", OperationsRead} {
		if !access.EdgeCandidateScope(action).Empty() || access.AllowsEdgeAction(action, "tails:1", "heads:1") {
			t.Fatal("unknown/global action acquired Edge authority")
		}
	}
	empty, err := policy.ForRoles([]string{"read"})
	if err != nil {
		t.Fatal(err)
	}
	if !empty.EdgeCandidateScope(EdgeDelete).Empty() {
		t.Fatal("read-only policy acquired modification candidates")
	}
}

func TestRoleRejectsDerivedEdgeActionsAndObsoleteSelectors(t *testing.T) {
	for _, action := range []Action{EdgeRead, EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete} {
		for _, effect := range []Effect{Allow, Deny} {
			if _, err := CompileRoles([]Role{{ID: "invalid", Rules: []PermissionRule{dataRule(effect, action, "")}}}, DefaultPolicyLimits()); err == nil {
				t.Fatal("derived operation admitted as a Role grant", action, effect)
			}
		}
	}
	// Older signed images must fail closed instead of silently dropping a pair
	// Deny and interpreting the remaining Vertex grants under a broader model.
	for _, action := range []Action{VertexRead, ReceiptRead, EdgeCreate} {
		if _, err := CompileRoles([]Role{{ID: "invalid", Rules: []PermissionRule{{Effect: Allow, Action: action, Resource: DataResource, Pair: &PrefixPair{}}}}}, DefaultPolicyLimits()); err == nil {
			t.Fatal("obsolete selector admitted")
		}
	}
}
