package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/anaregdesign/lantern/core/privatefile"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestHeadFixturePreservesNativeRoleAndPrivateCredentialBoundaries(t *testing.T) {
	for _, input := range []*fixture{nil, {}, {Nodes: []fixtureNode{{Environment: map[string]string{"LANTERN_AUTH_MODE": "off"}}}}, {Nodes: []fixtureNode{{PeerOrigin: "https://peer", Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}}}} {
		if err := addFixtureHeadEdge(input, t.TempDir()); err == nil {
			t.Fatal("invalid Head fixture accepted")
		}
	}
	directory := filepath.Join(t.TempDir(), "head")
	result, err := generate(directory, []int{16380}, nil, "oidc", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := addFixtureHeadEdge(&result, directory); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(result.HeadWriterTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	if err := privatefile.Check(file); err != nil {
		t.Fatal(err)
	}
	token, err := os.ReadFile(result.HeadWriterTokenFile)
	if err != nil {
		t.Fatal(err)
	}
	output, err := json.Marshal(result)
	if err != nil || bytes.Contains(output, token) {
		t.Fatal("Head credential leaked in metadata", err)
	}
	var roles []security.Role
	if err := json.Unmarshal([]byte(result.Nodes[0].Environment["LANTERN_SECURITY_BOOTSTRAP_ROLES"]), &roles); err != nil {
		t.Fatal(err)
	}
	compiled, err := security.CompileRoles(roles, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	access, err := compiled.ForRoles([]string{"fixture_head_writer"})
	if err != nil || !access.AllowsEdge(security.EdgeWrite, "physical-head:tails:1", "physical-head:heads:1") || access.AllowsEdge(security.EdgeRead, "physical-head:tails:1", "physical-head:heads:1") || access.AllowsEdge(security.EdgeWrite, "physical-head:tails:1", "physical-head:heads:denied:1") || access.Allows(security.VertexDelete, "physical-head:heads:1") {
		t.Fatal("Head Role boundary invalid", err)
	}
}
