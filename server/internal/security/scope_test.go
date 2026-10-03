package security

import (
	"math/rand"
	"strings"
	"sync"
	"testing"
)

func TestScope(t *testing.T) {
	prefixes := []string{"", "a", "aa", "a:", "a:private:", "b", "b:", "z", "日本:", "日本:秘密:", "é", "*"}
	keys := append(append([]string(nil), prefixes...), "a:public:1", "a:private:1", "日本:秘密:1", "日本:公開:1", "é:1", "*:literal", "sys:1", "data:1")
	random := rand.New(rand.NewSource(12))
	for iteration := 0; iteration < 150; iteration++ {
		roles := []Role{{ID: "one"}, {ID: "two"}}
		for i := range roles {
			for j := 0; j < 12; j++ {
				effect, action := Allow, VertexRead
				if random.Intn(2) == 0 {
					effect = Deny
				}
				if random.Intn(2) == 0 {
					action = Query
				}
				roles[i].Rules = append(roles[i].Rules, dataRule(effect, action, prefixes[random.Intn(len(prefixes))]))
			}
		}
		policy, err := CompileRoles(roles, DefaultPolicyLimits())
		if err != nil {
			t.Fatal(err)
		}
		access, err := policy.ForRoles([]string{"two", "one"})
		if err != nil {
			t.Fatal(err)
		}
		read := access.Scope(VertexRead)
		both := access.Scope(VertexRead, Query)
		for _, key := range keys {
			if got, want := read.Contains(key), referenceAllows(roles, VertexRead, key); got != want {
				t.Fatalf("iteration %d key %q: scope=%v rules=%v ranges=%v", iteration, key, got, roles, read.Ranges())
			}
			if got, want := both.Contains(key), access.Allows(VertexRead, key) && access.Allows(Query, key); got != want {
				t.Fatalf("intersection %q: %v want %v", key, got, want)
			}
			for _, prefix := range prefixes {
				if got, want := read.Within(prefix).Contains(key), read.Contains(key) && strings.HasPrefix(key, prefix); got != want {
					t.Fatalf("Within(%q) key %q", prefix, key)
				}
			}
		}
		for _, invalid := range []Action{"unknown", OperationsRead} {
			if !access.Scope(invalid).Empty() {
				t.Fatal("unknown/global action became a data scope")
			}
		}
		if ranges := read.Ranges(); len(ranges) > 0 {
			ranges[0].Lower = "mutated"
			if read.Ranges()[0].Lower == "mutated" {
				t.Fatal("mutable scope escaped")
			}
		}
	}
}

func TestScopeConcurrentSharedAccess(t *testing.T) {
	policy, err := CompileRoles([]Role{{ID: "one", Rules: []PermissionRule{dataRule(Allow, VertexRead, ""), dataRule(Deny, VertexRead, "private:")}}}, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, _ := policy.ForRoles([]string{"one"})
	var wait sync.WaitGroup
	for range 8 {
		wait.Go(func() {
			for range 100 {
				if access.Scope(VertexRead).Contains("private:1") || !access.Scope(VertexRead).Contains("public:1") {
					t.Error("shared scope changed")
				}
			}
		})
	}
	wait.Wait()
}

func TestScopeMembershipReuse(t *testing.T) {
	image := testImage()
	image.Principals = append(image.Principals, Principal{Identity: Identity{Kind: MachinePrincipal, MachineName: "other_admin"}, State: Active, Assignments: []RoleAssignment{{RoleID: "security_admin"}}})
	first, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	admin, _ := first.AccessFor(testIdentity())
	other, _ := first.AccessFor(image.Principals[1].Identity)
	if admin != other {
		t.Fatal("identical Role sets did not share a view")
	}
	image.Principals[1].State = Suspended
	next, err := compileImage(image, DefaultPolicyLimits(), first)
	if err != nil {
		t.Fatal(err)
	}
	current, _ := next.AccessFor(testIdentity())
	if current != admin {
		t.Fatal("account-only revision rebuilt the Role view")
	}
	image.Roles[0].Rules = append(image.Roles[0].Rules, dataRule(Allow, VertexRead, "public:"))
	changed, err := compileImage(image, DefaultPolicyLimits(), next)
	if err != nil {
		t.Fatal(err)
	}
	current, _ = changed.AccessFor(testIdentity())
	if current == admin || !current.Scope(VertexRead).Contains("public:1") || admin.Scope(VertexRead).Contains("public:1") {
		t.Fatal("Role revision reused stale permission")
	}
}
