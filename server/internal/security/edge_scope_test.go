package security

import "testing"

func TestDirectedPairRolesDoNotComposeHalvesOrActions(t *testing.T) {
	rule := func(action Action, effect Effect, tail, head string) PermissionRule {
		return PermissionRule{Effect: effect, Action: action, Resource: DataResource, Pair: &PrefixPair{Tail: tail, Head: head}}
	}
	read := PermissionRule{Effect: Allow, Action: VertexRead, Resource: DataResource, Prefix: new("")}
	roles := []Role{{ID: "first", Rules: []PermissionRule{read, rule(EdgeCreate, Allow, "users:a:", "topics:")}}, {ID: "second", Rules: []PermissionRule{rule(EdgeCreate, Allow, "users:b:", "assets:")}}, {ID: "deny", Rules: []PermissionRule{rule(EdgeCreate, Deny, "users:a:private:", "topics:")}}}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := policy.ForRoles([]string{"first", "second", "deny"})
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		action     Action
		tail, head string
		want       bool
	}{
		{EdgeCreate, "users:a:1", "topics:1", true}, {EdgeCreate, "topics:1", "users:a:1", false},
		{EdgeCreate, "users:a:1", "assets:1", false}, {EdgeCreate, "users:b:1", "assets:1", true},
		{EdgeCreate, "users:a:private:1", "topics:1", false}, {EdgeDelete, "users:a:1", "topics:1", false},
		{EdgeAdd, "users:a:1", "topics:1", false}, {EdgeWrite, "users:a:1", "topics:1", false},
	} {
		if got := access.AllowsEdge(tc.action, tc.tail, tc.head); got != tc.want {
			t.Fatalf("%+v got %t", tc, got)
		}
	}
	if access.Allows(VertexWrite, "users:a:1") || access.Allows(EdgeCreate, "topics:1") {
		t.Fatal("pair granted Vertex/one-dimensional rights")
	}
	candidate := access.EdgeCandidateScope(VertexRead, EdgeCreate)
	if !candidate.Contains("users:a:1") || !candidate.Contains("topics:1") || candidate.Contains("users:a:private:1") != true {
		t.Fatal("unsafe endpoint candidate superset")
	}
	roles[0].Rules[1].Pair.Tail = ""
	if access.AllowsEdge(EdgeCreate, "unassigned:1", "topics:1") {
		t.Fatal("compiled policy retained mutable pair")
	}
}
func TestPairDenyAndVertexPermissionAreIndependent(t *testing.T) {
	all := ""
	roles := []Role{{ID: "edges", Rules: []PermissionRule{
		{Effect: Allow, Action: EdgeRead, Resource: DataResource, Prefix: &all},
		{Effect: Deny, Action: EdgeRead, Resource: DataResource, Pair: &PrefixPair{Tail: "a:", Head: "secret:"}},
		{Effect: Allow, Action: ReceiptRead, Resource: DataResource, Pair: &PrefixPair{}},
	}}}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, _ := policy.ForRoles([]string{"edges"})
	if access.AllowsEdgeAction(EdgeRead, "a:1", "secret:1") || !access.AllowsEdgeAction(EdgeRead, "secret:1", "a:1") {
		t.Fatal("directed Deny drift")
	}
	if access.AllowsEdge(EdgeRead, "public:1", "a:1") || access.AllowsAll(EdgeRead) || access.AllowsAll(ReceiptRead) || access.Allows(ReceiptRead, "public:1") {
		t.Fatal("pair granted endpoint/whole-data privileges")
	}
}
func TestCompilePairSelectorRejectsWrongActionOrAmbiguousSelectors(t *testing.T) {
	all := ""
	for _, rule := range []PermissionRule{
		{Effect: Allow, Action: VertexWrite, Resource: DataResource, Pair: &PrefixPair{}},
		{Effect: Allow, Action: SecurityManage, Resource: GlobalResource, Pair: &PrefixPair{}},
		{Effect: Allow, Action: EdgeCreate, Resource: DataResource, Prefix: &all},
		{Effect: Allow, Action: EdgeRead, Resource: DataResource, Prefix: &all, Pair: &PrefixPair{}},
		{Effect: Allow, Action: EdgeCreate, Resource: DataResource, Pair: &PrefixPair{Tail: string(make([]byte, 1025))}},
	} {
		if _, err := CompileRoles([]Role{{ID: "invalid", Rules: []PermissionRule{rule}}}, DefaultPolicyLimits()); err == nil {
			t.Fatal("invalid pair admitted")
		}
	}
}
