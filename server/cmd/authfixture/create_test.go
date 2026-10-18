package main

import (
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCreateFixturePreservesVertexRolesAndStandaloneGuard(t *testing.T) {
	for _, input := range []*fixture{nil, {}, {Nodes: []fixtureNode{{Environment: map[string]string{"LANTERN_AUTH_MODE": "off"}}}}, {Nodes: []fixtureNode{{PeerOrigin: "https://peer", Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}}}} {
		if err := addFixtureEdgeCreate(input); err == nil {
			t.Fatal("invalid Create fixture accepted")
		}
	}
	result := fixture{security: &fixtureAuthorityInput{}, Nodes: []fixtureNode{{Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}}}
	if err := addFixtureEdgeCreate(&result); err != nil {
		t.Fatal(err)
	}
	roles := result.security.Roles
	if len(roles) != 1 || len(roles[0].Rules) != len(fixtureRoles()[0].Rules) {
		t.Fatal("unexpected fixture grants")
	}
	policy, err := security.CompileRoles(roles, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := policy.ForRoles([]string{roles[0].ID})
	if err != nil || !access.AllowsEdge(security.EdgeCreate, "bench:source:1", "bench:target:1") {
		t.Fatal("fixture did not derive Create from Vertex Roles", err)
	}
}
