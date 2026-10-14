package authz

import "testing"

func TestExplainStableBoundSources(t *testing.T) {
	allow := uidRule(Allow, VertexRead, "users:", ":")
	allow.ID = "own"
	deny := uidRule(Deny, VertexRead, "users:", ":private:")
	deny.ID = "secret"
	policy, err := CompileRoles([]Role{{ID: "z", Rules: []PermissionRule{allow}}, {ID: "a", Rules: []PermissionRule{deny}}}, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := policy.BindRoles([]string{"a", "z"}, SubjectBindings{testUIDA})
	if err != nil {
		t.Fatal(err)
	}
	key := "users:" + testUIDA + ":private:entry"
	allowed, matches, err := access.Explain(VertexRead, &key)
	if err != nil || allowed || len(matches) != 2 || matches[0].RoleID != "z" || matches[0].RuleID != "own" || matches[1].RuleID != "secret" {
		t.Fatal(allowed, matches, err)
	}
	for _, key := range []string{"", "\xff"} {
		if _, _, err := access.Explain(VertexRead, &key); err == nil {
			t.Fatal("invalid data key admitted")
		}
	}
	var absent *Access
	key = "users:" + testUIDA + ":entry"
	if allowed, matches, err := absent.Explain(VertexRead, &key); err != nil || allowed || len(matches) != 0 {
		t.Fatal("nil access explained an Allow")
	}
}
