package provider

import "testing"

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
	if _, err := loadPeerPlaneConfig(SecurityConfig{Mode: "oidc", NodeRole: "replica"}, PeerConfig{Discovery: "static"}); err == nil {
		t.Fatal("replica without independent workload boundary")
	}
	t.Setenv("LANTERN_PEER_LISTEN_ADDR", "")
	if _, err := loadPeerPlaneConfig(off, PeerConfig{Discovery: "static"}); err == nil {
		t.Fatal("explicit incomplete private plane ignored")
	}
	if _, _, err := NewConfiguredPeerIdentity(PeerPlaneConfig{Identity: PeerIdentityConfig{CAFile: "/tmp/ignored"}}); err == nil {
		t.Fatal("direct disabled plane ignored trust")
	}
}
