package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authz"
	"github.com/anaregdesign/lantern/server/internal/security"
)

// addFixtureHeadEdge creates a second Role-bound machine; no production auth
// bypass or direct Principal permissions are introduced. Paths, never tokens,
// are included in the supervisor's readiness output.
func addFixtureHeadEdge(result *fixture, directory string) error {
	if result == nil || len(result.Nodes) != 1 || result.Nodes[0].PeerOrigin != "" || result.Nodes[0].Environment["LANTERN_AUTH_MODE"] != "oidc" {
		return errors.New("standalone OIDC Head fixture required")
	}
	tail, head := "physical-head:tails:", "physical-head:heads:allowed:"
	role := security.Role{ID: "fixture_head_writer", Name: "Local Head write-only conformance", Rules: []security.PermissionRule{
		{ID: "tail_read", Permission: security.Read, Resource: security.DataResource, Prefix: &tail},
		{ID: "head_write", Permission: security.Write, Resource: security.DataResource, Prefix: &head},
	}}
	if result.security == nil {
		return errors.New("original OIDC input required")
	}
	result.security.Roles = append(fixtureRoles(), role)
	result.security.EdgePolicy = &security.EdgePolicy{Version: security.EdgePolicyVersion, Mode: authz.EdgeReadWrite}
	var random [32]byte
	if _, err := rand.Read(random[:]); err != nil {
		return err
	}
	token := "lnt_m1_" + base64.RawURLEncoding.EncodeToString(random[:])
	tokenFile, err := writeFile(directory, "head-writer-token", []byte(token))
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	result.security.Machines = append(result.security.Machines, map[string]any{"name": "fixture-head-writer", "role_ids": []string{role.ID}, "credentials": []any{map[string]any{"token": token, "created_at": now.Add(-time.Minute), "expires_at": now.Add(time.Hour)}}})
	result.HeadWriterTokenFile = tokenFile
	return nil
}
