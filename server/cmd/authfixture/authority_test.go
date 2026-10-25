package main

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
	"github.com/anaregdesign/lantern/server/provider"
)

func TestOriginalProvisioningRequiresToolchainOrPinnedExporter(t *testing.T) {
	t.Setenv("PATH", "")
	err := runOriginalProvisioner(t.Context(), "", filepath.Join(t.TempDir(), "authority"), "https://fixture.invalid", "original.json", 4)
	if !errors.Is(err, exec.ErrNotFound) {
		t.Fatal("isolated fixture lost the missing original provisioner cause", err)
	}
}

func TestLocalFixtureCreatesOwnedHomogeneousWorkloadConfig(t *testing.T) {
	for _, mode := range []string{"off", "oidc"} {
		t.Run(mode, func(t *testing.T) {
			for _, entry := range os.Environ() {
				for index, ch := range entry {
					if ch == '=' {
						name := entry[:index]
						if len(name) >= 8 && name[:8] == "LANTERN_" {
							t.Setenv(name, "")
							_ = os.Unsetenv(name)
						}
						break
					}
				}
			}
			dir := filepath.Join(t.TempDir(), "new-fixture")
			result, err := generate(dir, []int{16380, 16381}, []int{17380, 17381}, mode, "")
			if err != nil {
				t.Fatal(err)
			}
			if mode == "oidc" {
				result.Nodes[0].Environment["LANTERN_TLS_CLIENT_CA_FILE"] = result.CAFile
				if err := provisionFixtureAuthority(t.Context(), &result, dir, ""); err != nil {
					t.Fatal(err)
				}
			}
			if len(result.Nodes) != 2 {
				t.Fatal("node count")
			}
			output, err := json.Marshal(result)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "oidc" {
				raw, err := os.ReadFile(result.TokenFile)
				if err != nil {
					t.Fatal(err)
				}
				var tokens []string
				if json.Unmarshal(raw, &tokens) != nil || len(tokens) != 1 {
					t.Fatal("machine token file")
				}
				if bytes.Contains(output, []byte(tokens[0])) {
					t.Fatal("fixture output contains credential material")
				}
			}
			for _, node := range result.Nodes {
				t.Run(node.Name, func(t *testing.T) {
					environment, err := fixtureEnvironment(node, nil)
					if err != nil {
						t.Fatal(err)
					}
					for _, entry := range environment {
						name, value, ok := strings.Cut(entry, "=")
						if ok && strings.HasPrefix(name, "LANTERN_") {
							t.Setenv(name, value)
						}
					}
					if mode == "oidc" {
						current, err := provider.LoadSecurityConfig()
						if err != nil {
							t.Fatal(err)
						}
						if err := os.MkdirAll(current.ProvisionedSnapshotSpool.Directory, 0700); err != nil {
							t.Fatal(err)
						}
					}
					config, err := provider.NewConfig()
					if err != nil {
						t.Fatal(err)
					}
					if config.Security.Mode != mode || config.PeerPlane.Identity.SelfIdentity == "" || config.Changes.Options.CurrentKeyVersion != 1 {
						t.Fatal("incomplete trust composition")
					}
					if mode == "oidc" {
						peerMaterial, err := fixturePeerMaterial(node)
						if err != nil || peerMaterial.SelfIdentity != config.PeerPlane.Identity.SelfIdentity {
							t.Fatal("private fixture relay did not consume current material", err)
						}
						if node.originalMember == 1 && config.Security.TLS.ClientCAPEM == "" {
							t.Fatal("public mTLS selection lost from current startup")
						}
						if bytes.Contains(output, []byte(config.Security.TLS.KeyPEM)) {
							t.Fatal("fixture metadata disclosed inline TLS key")
						}
						p, err := security.LoadCurrentProvisioningJSON(config.Security.AuthorityJSON)
						if err != nil || config.PeerPlane.Identity.CurrentProfile != p.Profile().Binding() || config.PeerPlane.Identity.SecurityGeneration != p.Profile().Generation || config.PeerPlane.Identity.WriterPublicKey != [32]byte{} {
							t.Fatal("data membership lost its original authority binding", err)
						}
						if peer, _, err := provider.NewConfiguredPeerIdentity(config.PeerPlane); err == nil || peer != nil {
							t.Fatal("OIDC workload started without the actual current time owner")
						}
						var input struct {
							Membership struct {
								PublicKey    []byte `json:"public_key"`
								ManifestFile string `json:"manifest_file"`
								Profile      peerauth.ControlProfile
							}
						}
						raw := config.Security.AuthorityJSON
						if json.Unmarshal(raw, &input) != nil {
							t.Fatal("original fixture membership input missing", err)
						}
						key := input.Membership.PublicKey
						signed, signedErr := os.ReadFile(input.Membership.ManifestFile)
						manifest, verifyErr := peerauth.VerifyControlManifest(signed, ed25519.PublicKey(key), input.Membership.Profile.Lineage)
						if signedErr != nil || verifyErr != nil || manifest.ExpiresAt.Sub(manifest.IssuedAt) != peerauth.MaxMembershipLifetime {
							t.Fatal("original SDK campaign membership does not cover its declared bounded lifetime", signedErr, verifyErr)
						}
						return
					}
					peer, cleanup, err := provider.NewConfiguredPeerIdentity(config.PeerPlane)
					if err != nil {
						t.Fatal(err)
					}
					defer cleanup()
					if peer.CheckWorkload(context.Background()) != nil || peer.TLSConfig().MinVersion != tls.VersionTLS13 {
						t.Fatal("unqualified workload fixture")
					}
				})
			}
			if _, err := generate(dir, []int{16380}, nil, mode, ""); err == nil {
				t.Fatal("existing fixture directory overwritten")
			}
		})
	}
}
