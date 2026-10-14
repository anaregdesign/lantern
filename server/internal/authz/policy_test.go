package authz

import (
	"errors"
	"strings"
	"testing"
)

func TestCompileRolesValidation(t *testing.T) {
	valid := Role{ID: "reader", Rules: []PermissionRule{dataRule(Allow, VertexRead, "")}}
	tests := []struct {
		name   string
		roles  []Role
		limits PolicyLimits
	}{
		{"duplicate Role", []Role{valid, valid}, DefaultPolicyLimits()},
		{"invalid ID", []Role{{ID: "Admin"}}, DefaultPolicyLimits()},
		{"protected peer Role", []Role{{ID: "cluster_replica"}}, DefaultPolicyLimits()},
		{"unknown action", []Role{{ID: "r", Rules: []PermissionRule{dataRule(Allow, "read.*", "")}}}, DefaultPolicyLimits()},
		{"peer action", []Role{{ID: "r", Rules: []PermissionRule{globalRule(Allow, "cluster.replicate")}}}, DefaultPolicyLimits()},
		{"unknown effect", []Role{{ID: "r", Rules: []PermissionRule{dataRule("ALLOW", VertexRead, "")}}}, DefaultPolicyLimits()},
		{"wrong resource", []Role{{ID: "r", Rules: []PermissionRule{globalRule(Allow, VertexRead)}}}, DefaultPolicyLimits()},
		{"missing prefix", []Role{{ID: "r", Rules: []PermissionRule{{Effect: Allow, Action: VertexRead, Resource: DataResource}}}}, DefaultPolicyLimits()},
		{"global prefix", []Role{{ID: "r", Rules: []PermissionRule{dataRule(Allow, SecurityManage, "")}}}, DefaultPolicyLimits()},
		{"invalid UTF8", []Role{{ID: "r", Rules: []PermissionRule{dataRule(Allow, VertexRead, "\xff")}}}, DefaultPolicyLimits()},
		{"oversize prefix", []Role{{ID: "r", Rules: []PermissionRule{dataRule(Allow, VertexRead, strings.Repeat("a", 1025))}}}, DefaultPolicyLimits()},
		{"oversize name", []Role{{ID: "r", Name: strings.Repeat("a", 257)}}, DefaultPolicyLimits()},
		{"invalid limits", []Role{valid}, PolicyLimits{}},
		{"too many Roles", []Role{valid, {ID: "second"}}, PolicyLimits{MaxRoles: 1, MaxRules: 1, MaxPrefixBytes: 1, MaxTotalBytes: 1, MaxAssignments: 1}},
		{"too many rules", []Role{valid}, PolicyLimits{MaxRoles: 1, MaxPrefixBytes: 1, MaxTotalBytes: 1, MaxAssignments: 1}},
		{"total bytes", []Role{{ID: "r", Rules: []PermissionRule{dataRule(Allow, VertexRead, "aa"), dataRule(Deny, VertexRead, "bb")}}}, PolicyLimits{MaxRoles: 1, MaxRules: 2, MaxPrefixBytes: 3, MaxTotalBytes: 3, MaxAssignments: 1}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if _, err := CompileRoles(test.roles, test.limits); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatalf("admitted invalid policy: %v", err)
			}
		})
	}
	if _, err := CompileRoles([]Role{valid}, DefaultPolicyLimits()); err != nil {
		t.Fatal(err)
	}
}

func TestCompiledPolicyOwnsInput(t *testing.T) {
	rules := []PermissionRule{dataRule(Allow, VertexRead, "orders:")}
	roles := []Role{{ID: "reader", Rules: rules}}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := policy.ForRoles([]string{"reader"})
	if err != nil {
		t.Fatal(err)
	}
	*rules[0].Prefix = "private:"
	rules[0].Effect = Deny
	roles[0].ID = "other"
	if !access.Allows(VertexRead, "orders:1") || access.Allows(VertexRead, "private:1") {
		t.Fatal("caller mutation changed compiled policy")
	}
}

// Differential coverage compares compiled disjoint ranges with literal rule
// semantics across overlapping, duplicate, empty and Unicode prefixes.
func FuzzPrefixPolicy(f *testing.F) {
	f.Add([]byte{0, 1, 2, 3, 4, 5}, "orders:private:1")
	f.Add([]byte{2, 2, 2, 2}, "日本語:é")
	f.Fuzz(func(t *testing.T, choices []byte, key string) {
		if len(choices) > 64 || len(key) > 1024 {
			return
		}
		prefixes := []string{"", "orders:", "orders:private:", "orders:a", "sys:", "data:", "日本語:", "*"}
		roles := []Role{{ID: "a"}, {ID: "b"}}
		for i, choice := range choices {
			effect := Allow
			if choice&8 != 0 {
				effect = Deny
			}
			roles[i%2].Rules = append(roles[i%2].Rules, dataRule(effect, VertexRead, prefixes[int(choice)%len(prefixes)]))
		}
		policy, err := CompileRoles(roles, DefaultPolicyLimits())
		if err != nil {
			t.Fatal(err)
		}
		access, err := policy.ForRoles([]string{"a", "b"})
		if err != nil {
			t.Fatal(err)
		}
		want := referenceAllows(roles, VertexRead, key)
		if got := access.Allows(VertexRead, key); got != want {
			t.Fatalf("key %q: compiled=%v literal=%v", key, got, want)
		}
	})
}

func TestCompileRolesUIDSelectorValidation(t *testing.T) {
	for _, tc := range []struct {
		name  string
		rule  PermissionRule
		limit func(*PolicyLimits)
	}{
		{"empty before", uidRule(Allow, VertexRead, "", ":"), nil},
		{"empty after", uidRule(Allow, VertexRead, "users:", ""), nil},
		{"before delimiter", uidRule(Allow, VertexRead, "users", ":"), nil},
		{"after delimiter", uidRule(Allow, VertexRead, "users:", "suffix"), nil},
		{"before UTF8", uidRule(Allow, VertexRead, "\xff:", ":"), nil},
		{"after UTF8", uidRule(Allow, VertexRead, "users:", ":\xff"), nil},
		{"expanded length", uidRule(Allow, VertexRead, strings.Repeat("x", 991)+":", ":"), nil},
		{"operator prefix cap", uidRule(Allow, VertexRead, "users:", ":"), func(l *PolicyLimits) { l.MaxPrefixBytes = 38 }},
		{"operator total cap", uidRule(Allow, VertexRead, "users:", ":"), func(l *PolicyLimits) { l.MaxTotalBytes = 38 }},
		{"global UID selector", func() PermissionRule {
			r := uidRule(Allow, SecurityManage, "users:", ":")
			r.Resource = GlobalResource
			return r
		}(), nil},
		{"competing literal", func() PermissionRule {
			r := uidRule(Allow, VertexRead, "users:", ":")
			s := ""
			r.Prefix = &s
			return r
		}(), nil},
		{"retired pair", func() PermissionRule {
			r := uidRule(Allow, VertexRead, "users:", ":")
			r.Pair = &PrefixPair{}
			return r
		}(), nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			limits := DefaultPolicyLimits()
			if tc.limit != nil {
				tc.limit(&limits)
			}
			if _, err := CompileRoles([]Role{{ID: "r", Rules: []PermissionRule{tc.rule}}}, limits); !errors.Is(err, ErrInvalidPolicy) {
				t.Fatal("invalid selector admitted", err)
			}
		})
	}
	t.Run("expanded total across rules", func(t *testing.T) {
		limits := DefaultPolicyLimits()
		limits.MaxTotalBytes = 77
		if _, err := CompileRoles([]Role{{ID: "r", Rules: []PermissionRule{uidRule(Allow, VertexRead, "users:", ":"), uidRule(Allow, VertexWrite, "users:", ":")}}}, limits); !errors.Is(err, ErrInvalidPolicy) {
			t.Fatal(err)
		}
	})
	t.Run("exact byte bound and detached input", func(t *testing.T) {
		rule := uidRule(Allow, VertexRead, strings.Repeat("x", 990)+":", ":")
		policy, err := CompileRoles([]Role{{ID: "r", Rules: []PermissionRule{rule}}}, DefaultPolicyLimits())
		if err != nil {
			t.Fatal(err)
		}
		access, err := policy.BindRoles([]string{"r"}, SubjectBindings{testUIDA})
		if err != nil {
			t.Fatal(err)
		}
		key := rule.PrincipalUIDPrefix.bind(SubjectBindings{testUIDA}) + "a"
		rule.PrincipalUIDPrefix.LiteralBefore = "evil:"
		if !access.Allows(VertexRead, key) || !access.Scope(VertexRead).Contains(key) {
			t.Fatal("caller selector mutation escaped")
		}
		allowed, matches, err := access.Explain(VertexRead, &key)
		if err != nil || !allowed || len(matches) != 1 {
			t.Fatal("explain retained caller input", err)
		}
	})
}
