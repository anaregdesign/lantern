package security

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
