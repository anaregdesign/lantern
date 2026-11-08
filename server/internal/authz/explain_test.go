package authz

import "testing"

func TestExplainStableBoundSources(t *testing.T) {
	a := uidRule(Read, "users:", ":")
	a.ID = "own"
	b := uidRule(Read, "users:", ":private:")
	b.ID = "narrow"
	p, err := CompileRoles([]Role{{ID: "z", Rules: []PermissionRule{a}}, {ID: "a", Rules: []PermissionRule{b}}}, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, _ := p.BindRoles([]string{"a", "z"}, SubjectBindings{testUIDA})
	key := "users:" + testUIDA + ":private:entry"
	allowed, matches, err := access.ExplainResource(DataResource, Read, &key)
	if err != nil || !allowed || len(matches) != 2 || matches[0].RoleID != "z" || matches[0].RuleID != "own" || matches[1].RuleID != "narrow" || matches[0].Permission != Read || matches[0].Resource != DataResource {
		t.Fatal(allowed, matches, err)
	}
	for _, key := range []string{"", "\xff"} {
		if _, _, err := access.ExplainResource(DataResource, Read, &key); err == nil {
			t.Fatal("invalid key admitted")
		}
	}
	var absent *Access
	if got, _, err := absent.ExplainResource(DataResource, Read, &key); err != nil || got {
		t.Fatal("nil granted", err)
	}
}
