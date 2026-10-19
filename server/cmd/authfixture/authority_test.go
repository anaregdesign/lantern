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
					for name, value := range node.Environment {
						t.Setenv(name, value)
					}
					config, err := provider.NewConfig()
					if err != nil {
						t.Fatal(err)
					}
					if config.Security.Mode != mode || config.PeerPlane.Identity.SelfIdentity == "" || config.Changes.Options.CurrentKeyVersion != 1 {
						t.Fatal("incomplete trust composition")
					}
					if mode == "oidc" {
						p, err := security.LoadCurrentProvisioning(config.Security.ConfigFile)
						if err != nil || config.PeerPlane.Identity.CurrentProfile != p.Profile().Binding() || config.PeerPlane.Identity.SecurityGeneration != p.Profile().Generation || config.PeerPlane.Identity.WriterPublicKey != [32]byte{} {
							t.Fatal("data membership lost its original authority binding", err)
						}
						if peer, _, err := provider.NewConfiguredPeerIdentity(config.PeerPlane); err == nil || peer != nil {
							t.Fatal("OIDC workload started without the actual current time owner")
						}
						var input struct {
							Membership struct {
								KeyFile, ManifestFile string
								Profile               peerauth.ControlProfile
							}
						}
						raw, err := os.ReadFile(config.Security.ConfigFile)
						if err != nil || json.Unmarshal(raw, &input) != nil {
							t.Fatal("original fixture membership input missing", err)
						}
						key, keyErr := os.ReadFile(input.Membership.KeyFile)
						signed, signedErr := os.ReadFile(input.Membership.ManifestFile)
						manifest, verifyErr := peerauth.VerifyControlManifest(signed, ed25519.PublicKey(key), input.Membership.Profile.Lineage)
						if keyErr != nil || signedErr != nil || verifyErr != nil || manifest.ExpiresAt.Sub(manifest.IssuedAt) != peerauth.MaxMembershipLifetime {
							t.Fatal("original SDK campaign membership does not cover its declared bounded lifetime", keyErr, signedErr, verifyErr)
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
