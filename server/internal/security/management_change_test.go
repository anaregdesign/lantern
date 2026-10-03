package security

import "testing"

func TestManagementChangeRejectsAmbiguousAndDirectGrantShapes(t *testing.T) {
	identity := testIdentity()
	for _, change := range []Change{
		{Kind: "direct_permission", Identity: &identity},
		{Kind: PutPrincipal, Identity: &identity, State: Active, Role: &Role{ID: "escape"}},
		{Kind: PutPrincipal, Identity: &identity, State: Deleted},
		{Kind: PutAssignment, Identity: &identity, RoleID: "role", State: Active},
		{Kind: PutIssuer, Issuer: &Issuer{URL: "https://issuer.example", ClientID: "a", APIAudience: "b", Algorithms: []string{"RS256"}, EnvOwned: true}},
		{Kind: PutRole, Role: &Role{ID: "role", Rules: []PermissionRule{dataRule(Allow, VertexRead, "")}}},
	} {
		if err := change.validate(); err == nil {
			t.Fatal("invalid typed shape accepted", change.Kind)
		}
	}
}
