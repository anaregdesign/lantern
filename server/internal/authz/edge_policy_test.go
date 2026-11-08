package authz

import "testing"

func TestGlobalEdgePolicyTruthTable(t *testing.T) {
	for _, mode := range []EdgeEndpointMode{EdgeReadRead, EdgeWriteWrite, EdgeReadWrite, EdgeWriteRead} {
		for bits := 0; bits < 16; bits++ {
			roles := []Role{{ID: "tail-read"}, {ID: "tail-write"}, {ID: "head-read"}, {ID: "head-write"}}
			for i := range roles {
				if bits&(1<<i) != 0 {
					prefix := "tail:"
					if i >= 2 {
						prefix = "head:"
					}
					permission := Read
					if i%2 == 1 {
						permission = Write
					}
					roles[i].Rules = []PermissionRule{{ID: "grant", Permission: permission, Resource: DataResource, Prefix: &prefix}}
				}
			}
			p, err := CompileRolesWithEdgeMode(roles, DefaultPolicyLimits(), mode)
			if err != nil {
				t.Fatal(err)
			}
			a, err := p.ForRoles([]string{"tail-read", "tail-write", "head-read", "head-write"})
			if err != nil {
				t.Fatal(err)
			}
			tail, head := a.EdgeEndpointActions()
			ti, hi := 0, 2
			if tail == VertexWrite {
				ti = 1
			}
			if head == VertexWrite {
				hi = 3
			}
			want := bits&(1<<ti) != 0 && bits&(1<<hi) != 0
			for _, action := range []Action{EdgeCreate, EdgeAdd, EdgeWrite, EdgeDelete} {
				got := a.AllowsEdge(action, "tail:one", "head:two")
				if got != want {
					t.Fatalf("%s bits=%04b action=%s got=%v want=%v", mode, bits, action, got, want)
				}
				explained, matches, err := a.ExplainEdge(action, "tail:one", "head:two")
				if err != nil || explained != want {
					t.Fatalf("explain %s %04b: %v %v", mode, bits, explained, err)
				}
				if want && len(matches) != 2 {
					t.Fatalf("missing independent endpoint sources: %+v", matches)
				}
				if want {
					scope := a.EdgeCandidateScope(action)
					if !scope.Contains("tail:one") || !scope.Contains("head:two") {
						t.Fatal("candidate scope omitted authorized endpoints")
					}
				}
			}
			read := bits&1 != 0 && bits&4 != 0
			if a.AllowsEdge(EdgeRead, "tail:one", "head:two") != read {
				t.Fatal("write disclosed endpoints")
			}
		}
	}
	if _, err := CompileRolesWithEdgeMode(nil, DefaultPolicyLimits(), "unknown"); err == nil {
		t.Fatal("unknown endpoint rule accepted")
	}
}

func TestDefaultEdgePolicyAndCapturedIdentity(t *testing.T) {
	prefix := ""
	roles := []Role{{ID: "reader", Rules: []PermissionRule{{Permission: Read, Resource: DataResource, Prefix: &prefix}}}}
	rr, _ := CompileRoles(roles, DefaultPolicyLimits())
	rw, _ := CompileRolesWithEdgeMode(roles, DefaultPolicyLimits(), EdgeReadWrite)
	a, _ := rr.ForRoles([]string{"reader"})
	b, _ := rw.ForRoles([]string{"reader"})
	if !a.AllowsEdge(EdgeWrite, "a", "b") || !a.AllowsAll(EdgeWrite) || b.AllowsEdge(EdgeWrite, "a", "b") || b.AllowsAll(EdgeWrite) {
		t.Fatal("global rule/fast path mismatch")
	}
	if a.setKey == b.setKey {
		t.Fatal("global policy omitted from access identity")
	}
}
