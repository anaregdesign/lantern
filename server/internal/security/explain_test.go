package security

import "testing"

func TestExplainMatchesEffectiveDenyAndStableSources(t *testing.T) {
	image := testImage()
	prefix := "orders:"
	deny := "orders:private:"
	image.Roles = append(image.Roles, Role{ID: "data", Rules: []PermissionRule{{ID: "read", Effect: Allow, Action: VertexRead, Resource: DataResource, Prefix: &prefix}, {ID: "private", Effect: Deny, Action: VertexRead, Resource: DataResource, Prefix: &deny}}})
	image.Principals[0].Assignments = append(image.Principals[0].Assignments, RoleAssignment{RoleID: "data"})
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := "orders:private:1"
	allowed, matches, err := snapshot.Explain(testIdentity(), VertexRead, &key)
	if err != nil || allowed || len(matches) != 2 || matches[0].RuleID != "read" || matches[1].RuleID != "private" {
		t.Fatal(allowed, matches, err)
	}
	key = "orders:public:1"
	allowed, matches, err = snapshot.Explain(testIdentity(), VertexRead, &key)
	if err != nil || !allowed || len(matches) != 1 {
		t.Fatal(allowed, matches, err)
	}
	if _, _, err = snapshot.Explain(testIdentity(), SecurityManage, &key); err == nil {
		t.Fatal("global capability treated as data")
	}
}

func TestExplainDirectedPairMatchesSourcesAndDeny(t *testing.T) {
	image := testImage()
	image.Roles = append(image.Roles, Role{ID: "connections", Rules: []PermissionRule{
		{ID: "create", Effect: Allow, Action: EdgeCreate, Resource: DataResource, Pair: &PrefixPair{Tail: "users:", Head: "profiles:"}},
		{ID: "private", Effect: Deny, Action: EdgeCreate, Resource: DataResource, Pair: &PrefixPair{Tail: "users:", Head: "profiles:private:"}},
	}})
	image.Principals[0].Assignments = append(image.Principals[0].Assignments, RoleAssignment{RoleID: "connections"})
	snapshot, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tail, head string
		allowed    bool
		matches    int
	}{{"users:a", "profiles:a", true, 1}, {"profiles:a", "users:a", false, 0}, {"users:a", "profiles:private:a", false, 2}} {
		allowed, matches, err := snapshot.ExplainEdge(testIdentity(), EdgeCreate, tc.tail, tc.head)
		if err != nil || allowed != tc.allowed || len(matches) != tc.matches {
			t.Fatal(tc, allowed, matches, err)
		}
	}
	if _, _, err := snapshot.ExplainEdge(testIdentity(), VertexRead, "users:a", "profiles:a"); err == nil {
		t.Fatal("pair explanation broadened to Vertex action")
	}
}
