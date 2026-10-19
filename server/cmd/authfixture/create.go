package main

import (
	"errors"
)

// Standalone Create qualification reuses the fixture's explicit Vertex Roles.
// It never introduces an independent Edge grant or overrides the HA guard.
func addFixtureEdgeCreate(result *fixture) error {
	if result == nil || len(result.Nodes) != 1 || result.Nodes[0].PeerOrigin != "" || result.Nodes[0].Environment["LANTERN_AUTH_MODE"] != "oidc" {
		return errors.New("Create fixture requires standalone OIDC")
	}
	if result.security == nil {
		return errors.New("original OIDC input required")
	}
	result.security.Roles = fixtureRoles()
	if result.Nodes[0].Environment["LANTERN_RECEIPT_WAL_MODE"] != "" {
		result.Nodes[0].Environment["LANTERN_RECEIPT_MAX_ENTRIES"] = "32768"
		result.Nodes[0].Environment["LANTERN_RECEIPT_MAX_BYTES"] = "16777216"
	}
	return nil
}
