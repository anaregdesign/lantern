package security

import (
	"github.com/anaregdesign/lantern/server/internal/authz"
	"testing"
)

func TestExplainEffectiveUnionAndStableSources(t *testing.T) {
	image := testImage()
	prefix := "orders:public:"
	image.Roles = append(image.Roles, Role{ID: "data", Rules: []PermissionRule{{ID: "read", Permission: Read, Resource: DataResource, Prefix: &prefix}}})
	image.Principals[0].Assignments = append(image.Principals[0].Assignments, RoleAssignment{RoleID: "data"})
	s, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	key := "orders:private:1"
	allowed, matches, err := s.Explain(testIdentity(), VertexRead, &key)
	if err != nil || allowed || len(matches) != 0 {
		t.Fatal(allowed, matches, err)
	}
	key = "orders:public:1"
	allowed, matches, err = s.Explain(testIdentity(), Query, &key)
	if err != nil || !allowed || len(matches) != 1 || matches[0].RuleID != "read" || matches[0].Permission != Read {
		t.Fatal(allowed, matches, err)
	}
	if _, _, err = s.Explain(testIdentity(), SecurityManage, &key); err == nil {
		t.Fatal("global treated as data")
	}
}
func TestExplainDerivedEndpointSources(t *testing.T) {
	image := testImage()
	image.EdgePolicy = &EdgePolicy{Version: 1, Mode: authz.EdgeReadWrite}
	image.Roles = append(image.Roles, Role{ID: "tails", Rules: []PermissionRule{dataRule(VertexRead, "users:")}}, Role{ID: "heads", Rules: []PermissionRule{dataRule(VertexWrite, "profiles:public:")}})
	image.Principals[0].Assignments = append(image.Principals[0].Assignments, RoleAssignment{RoleID: "tails"}, RoleAssignment{RoleID: "heads"})
	s, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		tail, head string
		want       bool
		matches    int
	}{{"users:a", "profiles:public:a", true, 2}, {"profiles:a", "users:a", false, 0}, {"users:a", "profiles:private:a", false, 1}} {
		got, matches, err := s.ExplainEdge(testIdentity(), EdgeCreate, tc.tail, tc.head)
		if err != nil || got != tc.want || len(matches) != tc.matches {
			t.Fatal(tc, got, matches, err)
		}
		for _, m := range matches {
			if m.Endpoint == "tail" && m.Permission != Read || m.Endpoint == "head" && m.Permission != Write {
				t.Fatal(m)
			}
		}
	}
	if got, _, err := s.ExplainEdge(testIdentity(), EdgeRead, "users:a", "profiles:public:a"); err != nil || got {
		t.Fatal("write inferred read", err)
	}
}
