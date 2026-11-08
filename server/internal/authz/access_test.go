package authz

import (
	"errors"
	"fmt"
	"testing"
)

func TestAccessUnionDefaultRefusalAndResourceIsolation(t *testing.T) {
	roles := []Role{
		{ID: "read", Rules: []PermissionRule{dataRule(Read, "orders:"), dataRule(Read, "orders:public:")}},
		{ID: "overlap", Rules: []PermissionRule{dataRule(Read, "orders:public:")}},
		{ID: "write", Rules: []PermissionRule{dataRule(Write, "orders:public:")}},
		{ID: "security", Rules: []PermissionRule{globalRule(SecurityResource, Write)}},
		{ID: "ops", Rules: []PermissionRule{globalRule(OperationsResource, Read)}},
		{ID: "literal", Rules: []PermissionRule{dataRule(Read, "orders:*"), dataRule(Read, "é:")}},
	}
	p, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := p.ForRoles([]string{"read", "overlap", "write"})
	for _, operation := range []Action{VertexRead, Query, CDCIdentity, CDCValue, Export, ReceiptRead} {
		if !a.Allows(operation, "orders:private:1") || a.Allows(operation, "another:1") {
			t.Fatal("read mapping", operation)
		}
	}
	for _, operation := range []Action{VertexWrite, VertexDelete} {
		if !a.Allows(operation, "orders:public:1") || a.Allows(operation, "orders:private:1") {
			t.Fatal("write mapping", operation)
		}
	}
	for _, op := range []Action{SecurityRead, SecurityManage, SettingsRead, SettingsWrite, OperationsRead, SchemaRead} {
		if a.AllowsGlobal(op) {
			t.Fatal("data crossed resource", op)
		}
	}
	remaining, _ := p.ForRoles([]string{"overlap"})
	if !remaining.Allows(VertexRead, "orders:public:1") || remaining.Allows(VertexRead, "orders:private:1") {
		t.Fatal("membership removal lost union semantics")
	}
	write, _ := p.ForRoles([]string{"write"})
	if write.Allows(VertexRead, "orders:public:1") || !write.Allows(VertexDelete, "orders:public:1") {
		t.Fatal("write implied read")
	}
	security, _ := p.ForRoles([]string{"security"})
	if !security.AllowsGlobal(SecurityManage) || security.AllowsGlobal(SecurityRead) || security.AllowsGlobal(SettingsWrite) || security.AllowsGlobal(OperationsRead) {
		t.Fatal("typed global boundary")
	}
	literal, _ := p.ForRoles([]string{"literal"})
	if !literal.Allows(VertexRead, "orders:*1") || literal.Allows(VertexRead, "orders:1") || literal.Allows(VertexRead, "e\u0301:1") || !literal.Allows(VertexRead, "é:1") {
		t.Fatal("literal prefix changed")
	}
	empty, _ := p.ForRoles(nil)
	for _, access := range []*Access{nil, empty, security} {
		if access.Allows("unknown", "a") || access.Allows(VertexRead, "") || access.Allows(VertexRead, "\xff") || access.AllowsGlobal("cluster.replicate") {
			t.Fatal("default refusal")
		}
	}
	if _, err := p.ForRoles([]string{"missing"}); !errors.Is(err, ErrUnknownRole) {
		t.Fatal(err)
	}
	if _, err := p.ForRoles([]string{"read", "read"}); !errors.Is(err, ErrInvalidPolicy) {
		t.Fatal(err)
	}
}

func TestAccessWholeDomainProof(t *testing.T) {
	p, err := CompileRoles([]Role{{ID: "all", Rules: []PermissionRule{dataRule(Read, "")}}, {ID: "scoped", Rules: []PermissionRule{dataRule(Read, "orders:")}}}, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	a, _ := p.ForRoles([]string{"all"})
	s, _ := p.ForRoles([]string{"scoped"})
	if !a.AllowsAll(VertexRead) || !a.AllowsAll(Query) || a.AllowsAll(VertexWrite) || s.AllowsAll(VertexRead) || a.AllowsAll(SecurityRead) {
		t.Fatal("whole-domain proof")
	}
}

func TestAccessUIDBinding(t *testing.T) {
	p, err := CompileRoles([]Role{{ID: "personal", Rules: []PermissionRule{uidRule(Read, "users:", ":"), uidRule(Write, "users:", ":")}}}, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, uid := range []string{"", "bad", "00000000000000000000000000000000"} {
		if _, err := p.BindRoles([]string{"personal"}, SubjectBindings{uid}); err == nil {
			t.Fatal("invalid UID admitted")
		}
	}
	a, _ := p.BindRoles([]string{"personal"}, SubjectBindings{testUIDA})
	b, _ := p.BindRoles([]string{"personal"}, SubjectBindings{testUIDB})
	if a.setKey == b.setKey || a.BindingDigest() == b.BindingDigest() {
		t.Fatal("UID binding collapsed")
	}
	for _, entry := range []struct {
		key  string
		want bool
	}{{"users:" + testUIDA + ":private:1", true}, {"users:" + testUIDB + ":1", false}, {"users:" + testUIDA + "x:1", false}, {"users:" + testUIDA, false}} {
		if a.Allows(VertexRead, entry.key) != entry.want || a.Scope(Query, CDCValue).Contains(entry.key) != entry.want {
			t.Fatal(entry)
		}
		got, _, err := a.Explain(VertexRead, &entry.key)
		if err != nil || got != entry.want {
			t.Fatal(entry, err)
		}
	}
	if a.AllowsAll(VertexRead) || a.Scope(VertexRead).IsSubsetOf(b.Scope(VertexRead)) {
		t.Fatal("UID scope broadened")
	}
}
func BenchmarkAccess(b *testing.B) {
	for _, count := range []int{1, 8, 32} {
		b.Run(fmt.Sprintf("roles=%d", count), func(b *testing.B) {
			roles := make([]Role, count)
			ids := make([]string, count)
			for i := range roles {
				ids[i] = fmt.Sprintf("r%d", i)
				roles[i] = Role{ID: ids[i], Rules: []PermissionRule{dataRule(Read, "orders:")}}
			}
			p, err := CompileRoles(roles, DefaultPolicyLimits())
			if err != nil {
				b.Fatal(err)
			}
			a, _ := p.ForRoles(ids)
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if !a.Allows(VertexRead, "orders:1") {
					b.Fatal("refused")
				}
			}
		})
	}
}
func BenchmarkAccessUIDBinding(b *testing.B) {
	p, err := CompileRoles([]Role{{ID: "personal", Rules: []PermissionRule{uidRule(Read, "users:", ":")}}}, DefaultPolicyLimits())
	if err != nil {
		b.Fatal(err)
	}
	a, _ := p.BindRoles([]string{"personal"}, SubjectBindings{testUIDA})
	key := "users:" + testUIDA + ":1"
	b.ReportAllocs()
	for b.Loop() {
		if !a.Allows(VertexRead, key) {
			b.Fatal("refused")
		}
	}
}
