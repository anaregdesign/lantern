package provider

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/peerauth"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestPeerIdentityConfigurationFailsClosed(t *testing.T) {
	for _, config := range []PeerIdentityConfig{
		{},
		{SelfIdentity: "spiffe://lantern.test/a", StateMode: "fresh", CAFile: "relative"},
		{SelfIdentity: "spiffe://lantern.test/a", StateMode: "automatic"},
	} {
		if runtime, cleanup, err := NewPeerIdentityRuntime(config); err == nil || runtime != nil || cleanup != nil {
			t.Fatalf("invalid peer configuration accepted: %v", err)
		}
	}
}

func TestGeneratedPeerRejectsPreInitTrustSubstitution(t *testing.T) {
	for _, test := range []struct {
		name  string
		count int
	}{
		{"singleton_same_host_new_port", 1},
		{"remote_workload_id", 3},
		{"remote_public_key", 3},
		{"replacement_verification_key", 1},
		{"root_bundle_digest", 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			config, manifest, key := generatedPeerIdentityFixture(t, test.count)
			signingKey := key
			switch test.name {
			case "singleton_same_host_new_port":
				manifest.Members[0].Origin = "https://127.0.0.1:29999"
			case "remote_workload_id":
				manifest.Members[1].ID[0] ^= 0x80
			case "remote_public_key":
				manifest.Members[1].SPKI[0] ^= 0x80
			case "replacement_verification_key":
				public, replacement, err := ed25519.GenerateKey(rand.Reader)
				if err != nil {
					t.Fatal(err)
				}
				defer clear(replacement)
				signingKey = replacement
				der, err := x509.MarshalPKIXPublicKey(public)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(config.OperatorKeyFile, pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der}), 0600); err != nil {
					t.Fatal(err)
				}
			case "root_bundle_digest":
				roots, err := os.ReadFile(config.CAFile)
				if err != nil {
					t.Fatal(err)
				}
				roots = append(roots, '\n') // The same CA/certificate chain remains valid.
				if err := os.WriteFile(config.CAFile, roots, 0600); err != nil {
					t.Fatal(err)
				}
				manifest.Domain.TrustDigest = sha256.Sum256(roots)
			}
			manifest.Version++
			raw, err := peerauth.SignManifest(manifest, signingKey)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := peerauth.VerifyManifest(raw, signingKey.Public().(ed25519.PublicKey), manifest.Domain); err != nil {
				t.Fatal("test substitution is not a valid signed manifest", err)
			}
			if err := os.WriteFile(config.ManifestFile, raw, 0600); err != nil {
				t.Fatal(err)
			}
			peer, cleanup, err := NewPeerIdentityRuntime(config)
			if !errors.Is(err, peerauth.ErrMembership) || peer != nil || cleanup != nil {
				if cleanup != nil {
					cleanup()
				}
				t.Fatal("pre-init substitution replaced generated trust", err)
			}
			for _, path := range []string{config.StateFile, config.StateFile + ".lease"} {
				if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("substituted trust reached mutable peer custody", err)
				}
			}
		})
	}
}

func TestGeneratedPeerRequiresCompleteOwnedExpectations(t *testing.T) {
	config, manifest, key := generatedPeerIdentityFixture(t, 1)
	for _, mutate := range []func(*PeerIdentityConfig){
		func(c *PeerIdentityConfig) { c.ExpectedDomain = peerauth.Domain{} },
		func(c *PeerIdentityConfig) { c.ExpectedOperatorKey = [32]byte{} },
		func(c *PeerIdentityConfig) { c.FixedMembers = nil },
		func(c *PeerIdentityConfig) { c.AuthMode = "off" },
	} {
		copy := config
		mutate(&copy)
		if peer, cleanup, err := NewPeerIdentityRuntime(copy); !errors.Is(err, peerauth.ErrMembership) || peer != nil || cleanup != nil {
			if cleanup != nil {
				cleanup()
			}
			t.Fatal("partial generated expectation accepted", err)
		}
	}
	peer, cleanup, err := NewPeerIdentityRuntime(config)
	if err != nil {
		t.Fatal("original generated expectation rejected", err)
	}
	defer cleanup()
	config.FixedMembers[0].Origin = "https://127.0.0.1:29999"
	if !slices.Equal(peer.config.FixedMembers, manifest.Members) {
		t.Fatal("caller changed composed generated topology")
	}
	manifest.Version++
	raw, err := peerauth.SignManifest(manifest, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(config.ManifestFile, raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := peer.ReloadMembership(); err != nil {
		t.Fatal("caller mutation replaced fixed renewal trust", err)
	}
	if err := peer.CheckWorkload(t.Context()); err != nil {
		t.Fatal("valid fixed renewal lost peer authority", err)
	}
}

func generatedPeerIdentityFixture(t *testing.T, count int) (PeerIdentityConfig, peerauth.Manifest, ed25519.PrivateKey) {
	t.Helper()
	dir := t.TempDir()
	now := time.Date(2026, time.October, 25, 0, 0, 0, 0, time.UTC)
	issuer := security.Issuer{URL: "https://issuer.example", ConfigRevision: 1, EnvOwned: true, Enabled: true, ClientID: "admin", RedirectURI: "https://admin.example/callback", Algorithms: []string{"EdDSA"}}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1, Issuers: []security.Issuer{issuer},
		Roles:      []security.Role{{ID: "security_admin", Name: "Security administrator", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
		Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer.URL, Subject: "admin"}, State: security.Active, HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}}}
	var nodes []security.ProvisioningNode
	for i := 1; i <= count; i++ {
		nodes = append(nodes, security.ProvisioningNode{Name: fmt.Sprintf("node-%d", i), ControlOrigin: fmt.Sprintf("https://127.0.0.1:%d", 17000+2*i), DataOrigin: fmt.Sprintf("https://127.0.0.1:%d", 17001+2*i), ControlListen: fmt.Sprintf("127.0.0.1:%d", 17000+2*i), StateDir: filepath.Join(dir, fmt.Sprint(i), "state"), RenewableDir: filepath.Join(dir, fmt.Sprint(i), "membership")})
	}
	bundle, err := security.GenerateProvisioning(security.ProvisioningOptions{Target: runtime.GOOS, Now: now, End: now.Add(time.Hour), CleanupBy: now.Add(2 * time.Hour), Image: image, Nodes: nodes})
	if err != nil {
		t.Fatal(err)
	}
	node := bundle.Nodes[0]
	for name, raw := range map[string][]byte{"data.json": node.DataManifest, "operator.pem": node.DataOperatorPublicKey, "roots.pem": node.RootsPEM, "certificate.pem": node.CertificatePEM, "key.pem": node.TLSKeyPEM} {
		if err := os.WriteFile(filepath.Join(dir, name), raw, 0600); err != nil {
			t.Fatal(err)
		}
	}
	public, err := loadOperatorPublicKey(filepath.Join(dir, "operator.pem"))
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := peerauth.VerifyManifest(node.DataManifest, public, node.DataDomain)
	if err != nil {
		t.Fatal(err)
	}
	block, _ := pem.Decode(bundle.Operator.DataKeyPEM)
	if block == nil {
		t.Fatal("missing synthetic operator key")
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		t.Fatal(err)
	}
	key, ok := parsed.(ed25519.PrivateKey)
	if !ok {
		t.Fatal("unexpected synthetic operator key")
	}
	t.Cleanup(func() { clear(key) })
	config := PeerIdentityConfig{AuthMode: "oidc", Deployment: node.DataDomain.Deployment, SecurityGeneration: node.CurrentProfile.Generation, CurrentProfile: node.CurrentProfile.Binding(), ExpectedDomain: node.DataDomain, ExpectedOperatorKey: [32]byte(public), FixedMembers: slices.Clone(manifest.Members), StateMode: "fresh", StateFile: filepath.Join(dir, "data-membership"), ManifestFile: filepath.Join(dir, "data.json"), OperatorKeyFile: filepath.Join(dir, "operator.pem"), CAFile: filepath.Join(dir, "roots.pem"), CertFile: filepath.Join(dir, "certificate.pem"), KeyFile: filepath.Join(dir, "key.pem"), SelfIdentity: node.Identity, Now: func() time.Time { return now }, TimeBounds: func() (time.Time, time.Time, error) { return now, now, nil }}
	return config, manifest, key
}
