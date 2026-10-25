package provider

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestPeerPlaneCannotFallbackToPublicOrIgnoreTrust(t *testing.T) {
	off := SecurityConfig{Mode: "off"}
	if cfg, err := loadPeerPlaneConfig(off, PeerConfig{Discovery: "static"}); err != nil || cfg.ListenAddress != "" {
		t.Fatal(err)
	}
	for _, legacy := range []PeerConfig{
		{Discovery: "static", Peers: []string{"localhost:6380"}},
		{Discovery: "dns", DNSName: "cluster"},
		{Discovery: "static", CAFile: "/tmp/ca"},
		{Discovery: "static", ClientKeyFile: "/tmp/key"},
	} {
		if _, err := loadPeerPlaneConfig(off, legacy); err == nil {
			t.Fatal("legacy anonymous/public peer configuration accepted")
		}
	}
	t.Setenv("LANTERN_PEER_LISTEN_ADDR", "")
	if _, err := loadPeerPlaneConfig(off, PeerConfig{Discovery: "static"}); err == nil {
		t.Fatal("explicit incomplete private plane ignored")
	}
	if _, _, err := NewConfiguredPeerIdentity(PeerPlaneConfig{Identity: PeerIdentityConfig{CAFile: "/tmp/ignored"}}); err == nil {
		t.Fatal("direct disabled plane ignored trust")
	}
}

func TestProvisionedPeerRejectsCompetingInputs(t *testing.T) {
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(strings.ToUpper(name), "LANTERN_PEER_") || strings.ToUpper(name) == "LANTERN_PEERS" {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	config := SecurityConfig{Mode: "oidc", ProvisionedPeer: &PeerPlaneConfig{ListenAddress: "127.0.0.1:17380"}}
	for _, name := range []string{"LANTERN_PEER_LISTEN_ADDR", "LANTERN_PEER_MEMBERSHIP_MODE", "LANTERN_PEER_CERT_FILE", "LANTERN_PEER_DISCOVERY", "LANTERN_PEERS", "lantern_peer_key_file"} {
		t.Run(name, func(t *testing.T) {
			t.Setenv(name, "")
			if _, err := loadPeerPlaneConfig(config, PeerConfig{Discovery: "static"}); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
				t.Fatal("provisioned trust ignored an explicitly present peer setting", err)
			}
		})
	}
	for _, legacy := range []PeerConfig{{Discovery: "static", Peers: []string{"https://peer.invalid"}}, {Discovery: "dns"}, {Discovery: "static", DNSName: "ignored"}, {Discovery: "static", CAFile: "/ignored"}} {
		if _, err := loadPeerPlaneConfig(config, legacy); err == nil || !strings.Contains(err.Error(), "cannot be combined") {
			t.Fatal("provisioned trust ignored legacy peer configuration", err)
		}
	}
}

func TestProvisionedPeerRequiresOwnedAuthorityAndUninjectedTime(t *testing.T) {
	for _, config := range []SecurityConfig{
		{Mode: "off", ProvisionedPeer: &PeerPlaneConfig{ListenAddress: "127.0.0.1:17380"}},
		{Mode: "oidc", ProvisionedPeer: &PeerPlaneConfig{}},
		{Mode: "oidc", ProvisionedPeer: &PeerPlaneConfig{ListenAddress: "127.0.0.1:17380", Identity: PeerIdentityConfig{Now: time.Now}}},
		{Mode: "oidc", AuthorityJSON: []byte(`{}`), ProvisionedPeer: &PeerPlaneConfig{ListenAddress: "127.0.0.1:17380"}},
	} {
		if _, err := loadPeerPlaneConfig(config, PeerConfig{Discovery: "static"}); err == nil {
			t.Fatal("incomplete or clock-injected provisioned peer accepted")
		}
	}
}

func TestProvisionedPeerBindsJSONAuthorityAndAlwaysResumes(t *testing.T) {
	dir := t.TempDir()
	state, renewable := filepath.Join(dir, "state"), filepath.Join(dir, "renewable")
	for _, path := range []string{state, renewable} {
		if err := os.Mkdir(path, 0700); err != nil {
			t.Fatal(err)
		}
	}
	now := time.Date(2026, time.October, 25, 0, 0, 0, 0, time.UTC)
	issuer := security.Issuer{URL: "https://issuer.example", ConfigRevision: 1, EnvOwned: true, Enabled: true, APIAudience: "lantern-api", Algorithms: []string{"EdDSA"}}
	image := security.Image{Version: security.ImageVersion, BootstrapRevision: 1, Issuers: []security.Issuer{issuer},
		Roles:      []security.Role{{ID: "security_admin", Name: "Security administrator", Rules: []security.PermissionRule{{ID: "manage", Effect: security.Allow, Action: security.SecurityManage, Resource: security.GlobalResource}}}},
		Principals: []security.Principal{{Identity: security.Identity{Kind: security.OIDCPrincipal, Issuer: issuer.URL, Subject: "admin"}, State: security.Active, HumanIssuerConfigRevision: 1, Assignments: []security.RoleAssignment{{RoleID: "security_admin", EnvOwned: true}}}},
	}
	bundle, err := security.GenerateProvisioning(security.ProvisioningOptions{Target: runtime.GOOS, Now: now, End: now.Add(time.Hour), CleanupBy: now.Add(2 * time.Hour), Image: image,
		Nodes: []security.ProvisioningNode{{Name: "one", ControlOrigin: "https://127.0.0.1:17381", DataOrigin: "https://127.0.0.1:17382", ControlListen: "127.0.0.1:17381", StateDir: state, RenewableDir: renewable}},
	})
	if err != nil {
		t.Fatal("generate coherent peer trust", err)
	}
	node := bundle.Nodes[0]
	if err := os.WriteFile(filepath.Join(renewable, "control.json"), bundle.Operator.ControlManifest, 0600); err != nil {
		t.Fatal(err)
	}
	peer := &PeerPlaneConfig{ListenAddress: "127.0.0.1:17382", Identity: PeerIdentityConfig{StateMode: "fresh", Deployment: node.DataDomain.Deployment, SelfIdentity: node.Identity,
		StateFile: filepath.Join(state, "data-membership"), ManifestFile: filepath.Join(renewable, "data-membership.signed"), OperatorKeyFile: filepath.Join(dir, "operator.pub"),
		CertFile: filepath.Join(dir, "cert.pem"), KeyFile: filepath.Join(dir, "key.pem"), CAFile: filepath.Join(dir, "roots.pem"),
	}}
	config := SecurityConfig{Mode: "oidc", AuthorityJSON: node.AuthorityJSON, ProvisionedPeer: peer}
	cfg, err := loadPeerPlaneConfig(config, PeerConfig{Discovery: "static"})
	if err != nil {
		t.Fatal("load JSON-backed peer authority", err)
	}
	if cfg.Identity.StateMode != "resume" || cfg.Identity.AuthMode != "oidc" || cfg.Identity.CurrentProfile != node.CurrentProfile.Binding() || cfg.Identity.SecurityGeneration != node.CurrentProfile.Generation {
		t.Fatal("generated peer did not select exact authority or attempted fresh state")
	}
	if peer.Identity.StateMode != "fresh" || peer.Identity.CurrentProfile != "" || peer.Identity.SecurityGeneration != [16]byte{} {
		t.Fatal("load changed immutable provisioning input")
	}
	for _, mutate := range []func(*PeerIdentityConfig){
		func(i *PeerIdentityConfig) { i.CurrentProfile = "foreign" },
		func(i *PeerIdentityConfig) { i.SecurityGeneration = [16]byte{1} },
		func(i *PeerIdentityConfig) { i.WriterPublicKey = [32]byte{1} },
	} {
		copy := *peer
		mutate(&copy.Identity)
		config.ProvisionedPeer = &copy
		if _, err := loadPeerPlaneConfig(config, PeerConfig{Discovery: "static"}); err == nil || !strings.Contains(err.Error(), "different current cohort") {
			t.Fatal("mismatched generated data cohort accepted", err)
		}
	}
}
