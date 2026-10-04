package main

import (
	"encoding/json"
	"errors"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// A narrow opt-in grant used only by standalone Create qualification. Default
// SDK/HA fixtures retain their original families; no peer guard is overridden.
func addFixtureEdgeCreate(result *fixture) error {
	if result == nil || len(result.Nodes) != 1 || result.Nodes[0].PeerOrigin != "" || result.Nodes[0].Environment["LANTERN_AUTH_MODE"] != "oidc" {
		return errors.New("Create fixture requires standalone OIDC")
	}
	roles := fixtureRoles()
	roles[0].Rules = append(roles[0].Rules, security.PermissionRule{
		ID: "edge_create", Action: security.EdgeCreate, Effect: security.Allow, Resource: security.DataResource,
		Pair: &security.PrefixPair{Tail: "bench:source:", Head: "bench:target:"},
	})
	raw, err := json.Marshal(roles)
	if err != nil {
		return err
	}
	result.Nodes[0].Environment["LANTERN_SECURITY_BOOTSTRAP_ROLES"] = string(raw)
	if result.Nodes[0].Environment["LANTERN_RECEIPT_WAL_MODE"] != "" {
		result.Nodes[0].Environment["LANTERN_RECEIPT_MAX_ENTRIES"] = "32768"
		result.Nodes[0].Environment["LANTERN_RECEIPT_MAX_BYTES"] = "16777216"
	}
	return nil
}
