package security

import (
	"github.com/anaregdesign/lantern/server/internal/authz"
	"testing"
)

func TestEdgePolicyAtomicValidationAndModeIdentity(t *testing.T) {
	if !(*EdgePolicy)(nil).valid() || (*EdgePolicy)(nil).mode() != "rr" {
		t.Fatal("fresh default")
	}
	for _, mode := range []string{"rr", "ww", "rw", "wr"} {
		p := &EdgePolicy{Version: 1}
		p.Mode = authz.EdgeEndpointMode(mode)
		c := Change{Kind: PutEdgePolicy, EdgePolicy: p}
		if err := c.validate(); err != nil {
			t.Fatal(mode, err)
		}
		image := Image{}
		if err := applyManagementChanges(&image, []Change{c}); err != nil || image.EdgePolicy == p || image.EdgePolicy.Mode != p.Mode {
			t.Fatal("policy detached apply", err)
		}
	}
	for _, c := range []Change{{Kind: PutEdgePolicy}, {Kind: PutEdgePolicy, EdgePolicy: &EdgePolicy{Version: 1, Mode: "unknown"}}, {Kind: PutEdgePolicy, EdgePolicy: &EdgePolicy{Version: 1, Mode: "rr"}, RoleID: "other"}, {Kind: DeleteRole, RoleID: "other", EdgePolicy: &EdgePolicy{Version: 1, Mode: "rr"}}} {
		if c.validate() == nil {
			t.Fatal("invalid policy operation accepted")
		}
	}
}

func TestEdgePolicyChangeInvalidatesRoleCacheAndAdmissionSubset(t *testing.T) {
	image := testImage()
	prefix := ""
	image.Roles = append(image.Roles, Role{ID: "graph-reader", Rules: []PermissionRule{{ID: "read", Permission: Read, Resource: DataResource, Prefix: &prefix}}})
	image.Principals[0].Assignments = append(image.Principals[0].Assignments, RoleAssignment{RoleID: "graph-reader"})
	before, err := CompileImage(image, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	image.EdgePolicy = &EdgePolicy{Version: 1, Mode: authz.EdgeWriteWrite}
	after, err := compileImage(image, DefaultPolicyLimits(), before)
	if err != nil {
		t.Fatal(err)
	}
	a, _ := before.AccessFor(testIdentity())
	b, _ := after.AccessFor(testIdentity())
	if !a.AllowsEdge(EdgeWrite, "a", "b") || b.AllowsEdge(EdgeWrite, "a", "b") {
		t.Fatal("old Role cache retained endpoint policy")
	}
	if before.policy == after.policy || s1PermissionsSubset(before, after) || s1PermissionsSubset(after, before) {
		t.Fatal("changed global predicate carried old cut")
	}
	if before.EdgePolicy().Mode != authz.EdgeReadRead || after.EdgePolicy().Mode != authz.EdgeWriteWrite {
		t.Fatal("snapshot policy mutated")
	}
}
