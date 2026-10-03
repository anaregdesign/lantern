package security

import (
	"errors"
	"fmt"
	"testing"
)

func TestAccessDenyAndIsolation(t *testing.T) {
	roles := []Role{
		{ID: "reader", Rules: []PermissionRule{dataRule(Allow, VertexRead, ""), dataRule(Allow, VertexRead, "orders:private:allowed:")}},
		{ID: "restricted", Rules: []PermissionRule{dataRule(Deny, VertexRead, "orders:private:")}},
		{ID: "admin", Rules: []PermissionRule{globalRule(Allow, SecurityManage)}},
		{ID: "block_admin", Rules: []PermissionRule{globalRule(Deny, SecurityManage)}},
		{ID: "literal", Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:*"), dataRule(Allow, VertexRead, "é:")}},
	}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][]string{{"reader", "restricted"}, {"restricted", "reader"}} {
		access, err := policy.ForRoles(ids)
		if err != nil {
			t.Fatal(err)
		}
		for _, key := range []string{"orders:1", "sys:1", "data:1"} {
			if !access.Allows(VertexRead, key) {
				t.Fatalf("read unexpectedly denied: %q", key)
			}
		}
		for _, key := range []string{"orders:private:1", "orders:private:allowed:1"} {
			if access.Allows(VertexRead, key) {
				t.Fatalf("Deny bypass: %q", key)
			}
		}
		if access.Allows(VertexWrite, "orders:1") || access.Allows(CDCIdentity, "orders:1") || access.Allows(Export, "orders:1") || access.AllowsGlobal(OperationsRead) {
			t.Fatal("implicit permission hierarchy")
		}
	}
	admin, _ := policy.ForRoles([]string{"admin"})
	if !admin.AllowsGlobal(SecurityManage) || admin.Allows(VertexRead, "any") || admin.Allows(SecurityManage, "any") || admin.AllowsGlobal(VertexRead) {
		t.Fatal("global/data capability confusion")
	}
	blocked, _ := policy.ForRoles([]string{"admin", "block_admin"})
	if blocked.AllowsGlobal(SecurityManage) {
		t.Fatal("global Deny bypass")
	}
	literal, _ := policy.ForRoles([]string{"literal"})
	if !literal.Allows(VertexRead, "orders:*123") || literal.Allows(VertexRead, "orders:123") || literal.Allows(VertexRead, "e\u0301:1") || !literal.Allows(VertexRead, "é:1") {
		t.Fatal("prefix normalized or treated as wildcard")
	}
	for _, ids := range [][]string{{"missing"}, {"reader", "missing"}} {
		if _, err := policy.ForRoles(ids); !errors.Is(err, ErrUnknownRole) {
			t.Fatalf("unknown Role: %v", err)
		}
	}
	if _, err := policy.ForRoles([]string{"reader", "reader"}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal("duplicate assignment admitted")
	}
	empty, _ := policy.ForRoles(nil)
	for _, access := range []*Access{nil, empty, admin} {
		if access.Allows("unknown", "a") || access.Allows(VertexRead, "") || access.Allows(VertexRead, "\xff") || access.AllowsGlobal("cluster.replicate") {
			t.Fatal("invalid access admitted")
		}
	}
}

func TestAccessEdgeEndpoints(t *testing.T) {
	rules := []PermissionRule{}
	for _, action := range []Action{VertexRead, VertexWrite, EdgeRead, EdgeAdd, EdgeWrite, EdgeDelete} {
		rules = append(rules, dataRule(Allow, action, "orders:"))
	}
	roles := []Role{{ID: "editor", Rules: rules}, {ID: "restriction", Rules: []PermissionRule{dataRule(Deny, VertexWrite, "orders:protected:")}}}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, _ := policy.ForRoles([]string{"editor", "restriction"})
	for _, action := range []Action{EdgeRead, EdgeAdd, EdgeWrite, EdgeDelete} {
		if !access.AllowsEdge(action, "orders:a", "orders:b") {
			t.Fatalf("expected %s", action)
		}
		if access.AllowsEdge(action, "orders:a", "private:b") || access.AllowsEdge(action, "private:a", "orders:b") {
			t.Fatal("cross-prefix endpoint bypass")
		}
	}
	if access.AllowsEdge(EdgeAdd, "orders:a", "orders:protected:b") || access.AllowsEdge(EdgeWrite, "orders:protected:a", "orders:b") {
		t.Fatal("endpoint creation bypass")
	}
	if !access.AllowsEdge(EdgeRead, "orders:a", "orders:protected:b") || access.AllowsEdge(Query, "orders:a", "orders:b") {
		t.Fatal("edge action confused")
	}
}

func BenchmarkAccess(b *testing.B) {
	for _, roleCount := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("roles=%d", roleCount), func(b *testing.B) {
			roles := make([]Role, roleCount)
			ids := make([]string, roleCount)
			for i := range roles {
				ids[i] = fmt.Sprintf("r%d", i)
				roles[i] = Role{ID: ids[i], Rules: []PermissionRule{dataRule(Allow, VertexRead, "orders:"), dataRule(Deny, VertexRead, "orders:private:")}}
			}
			policy, err := CompileRoles(roles, DefaultPolicyLimits())
			if err != nil {
				b.Fatal(err)
			}
			access, err := policy.ForRoles(ids)
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if !access.Allows(VertexRead, "orders:123") {
					b.Fatal("denied")
				}
			}
		})
	}
}
