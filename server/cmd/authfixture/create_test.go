package main

import (
	"encoding/json"
	"github.com/anaregdesign/lantern/server/internal/security"
	"testing"
)

func TestCreateFixtureGrantsOnlyExplicitStandaloneDirection(t *testing.T) {
	for _, input := range []*fixture{nil, {}, {Nodes: []fixtureNode{{Environment: map[string]string{"LANTERN_AUTH_MODE": "off"}}}}, {Nodes: []fixtureNode{{PeerOrigin: "https://peer", Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}}}} {
		if err := addFixtureEdgeCreate(input); err == nil {
			t.Fatal("invalid Create fixture accepted")
		}
	}
	result := fixture{Nodes: []fixtureNode{{Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}}}
	if err := addFixtureEdgeCreate(&result); err != nil {
		t.Fatal(err)
	}
	var roles []security.Role
	if err := json.Unmarshal([]byte(result.Nodes[0].Environment["LANTERN_SECURITY_BOOTSTRAP_ROLES"]), &roles); err != nil {
		t.Fatal(err)
	}
	if len(roles) != 1 || len(roles[0].Rules) != len(fixtureRoles()[0].Rules)+1 {
		t.Fatal("unexpected fixture grants")
	}
	rule := roles[0].Rules[len(roles[0].Rules)-1]
	if rule.Action != security.EdgeCreate || rule.Pair == nil || rule.Pair.Tail != "bench:source:" || rule.Pair.Head != "bench:target:" {
		t.Fatal(rule)
	}
}
