package main

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

// addFixtureHeadEdge creates a second Role-bound machine; no production auth
// bypass or direct Principal permissions are introduced. Paths, never tokens,
// are included in the supervisor's readiness output.
func addFixtureHeadEdge(result *fixture, directory string) error {
	if result == nil || len(result.Nodes) != 1 || result.Nodes[0].PeerOrigin != "" || result.Nodes[0].Environment["LANTERN_AUTH_MODE"] != "oidc" {
		return errors.New("standalone OIDC Head fixture required")
	}
	tail, head, denied, receipt := "physical-head:tails:", "physical-head:heads:", "physical-head:heads:denied:", "physical-head:"
	role := security.Role{ID: "fixture_head_writer", Name: "Local Head write-only conformance", Rules: []security.PermissionRule{
		{ID: "tail_read", Action: security.VertexRead, Effect: security.Allow, Resource: security.DataResource, Prefix: &tail},
		{ID: "head_write", Action: security.VertexWrite, Effect: security.Allow, Resource: security.DataResource, Prefix: &head},
		{ID: "head_deny", Action: security.VertexWrite, Effect: security.Deny, Resource: security.DataResource, Prefix: &denied},
		{ID: "receipt", Action: security.ReceiptRead, Effect: security.Allow, Resource: security.DataResource, Prefix: &receipt},
	}}
	if result.security == nil {
		return errors.New("original OIDC input required")
	}
	result.security.Roles = append(fixtureRoles(), role)
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
