package provider

import (
	"strings"
	"testing"

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

func TestCurrentDataPeerFactoryRequiresMatchingAuthority(t *testing.T) {
	cfg := PeerPlaneConfig{ListenAddress: "127.0.0.1:17380", Identity: PeerIdentityConfig{AuthMode: "oidc", CurrentProfile: "current-v2:abababababababababababababababababababababababababababababababab", SecurityGeneration: [16]byte{1}}}
	for _, runtime := range []*SecurityRuntime{nil, {current: &security.CurrentAuthority{}}} {
		if peer, cleanup, err := NewCurrentConfiguredPeerIdentity(cfg, runtime); err == nil || peer != nil || cleanup != nil || runtime != nil && !strings.Contains(err.Error(), "different current cohort") {
			t.Fatal("unowned or foreign current authority reached data workload", err)
		}
	}
}
