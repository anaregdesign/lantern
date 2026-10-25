package provider

import (
	"encoding/json"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"
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
	options := provisioningTestOptions(t)
	var input securityConfigJSON
	if err := json.Unmarshal([]byte(os.Getenv("LANTERN_SECURITY_CONFIG_JSON")), &input); err != nil {
		t.Fatal(err)
	}
	input.TargetPlatform = runtime.GOOS
	raw, _ := json.Marshal(input)
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", string(raw))
	if err := ProvisionSecurity(t.Context(), options); err != nil {
		t.Fatal(err)
	}
	config, err := loadSecurityConfigJSON(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(config.StateDirectory, 0700); err != nil {
		t.Fatal(err)
	}
	peer := config.ProvisionedPeer
	original := peer.Identity
	cfg, err := loadPeerPlaneConfig(config, PeerConfig{Discovery: "static"})
	if err != nil {
		t.Fatal("load JSON-backed peer authority", err)
	}
	if cfg.Identity.StateMode != "resume" || cfg.Identity.AuthMode != "oidc" || cfg.Identity.CurrentProfile != original.CurrentProfile || cfg.Identity.SecurityGeneration != original.SecurityGeneration {
		t.Fatal("generated peer did not select exact authority or attempted fresh state")
	}
	if peer.Identity.StateMode != original.StateMode || peer.Identity.CurrentProfile != original.CurrentProfile || peer.Identity.SecurityGeneration != original.SecurityGeneration {
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
