package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestChangeConfigRequiresDurableSharedKeysForProtectedOrHA(t *testing.T) {
	t.Setenv("LANTERN_CDC_ENABLED", "true")
	t.Setenv("LANTERN_CDC_CURSOR_KEY_RING_FILE", "")
	off := SecurityConfig{Mode: "off"}
	first, err := loadChangeConfig(off, PeerPlaneConfig{})
	if err != nil || !first.Enabled || len(first.Options.CursorKeys) != 1 {
		t.Fatal("standalone OFF bootstrap", err)
	}
	second, err := loadChangeConfig(off, PeerPlaneConfig{})
	if err != nil || first.Options.CursorKeys[0] == second.Options.CursorKeys[0] {
		t.Fatal("standalone restart reused ephemeral key", err)
	}
	for _, mode := range []string{"off", "oidc"} {
		plane := PeerPlaneConfig{ListenAddress: "127.0.0.1:6391"}
		if _, err := loadChangeConfig(SecurityConfig{Mode: mode}, plane); err == nil {
			t.Fatal("HA cursor key was inferred", mode)
		}
	}
	path := filepath.Join(t.TempDir(), "keys.json")
	t.Setenv("LANTERN_CDC_CURSOR_KEY_RING_FILE", path)
	for _, raw := range []string{
		`{"current_version":1,"keys":[{"version":1,"key":"` + strings.Repeat("00", 32) + `"}]}`,
		`{"current_version":2,"keys":[{"version":1,"key":"` + strings.Repeat("ab", 32) + `"}]}`,
		`{"current_version":1,"current_version":1,"keys":[]}`,
		`{"current_version":1,"keys":[{"version":1,"key":"` + strings.Repeat("AB", 32) + `"}]}`,
	} {
		if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := loadChangeConfig(SecurityConfig{Mode: "oidc"}, PeerPlaneConfig{}); err == nil {
			t.Fatal("invalid ring accepted")
		}
	}
	raw := `{"current_version":2,"keys":[{"version":1,"key":"` + strings.Repeat("ab", 32) + `"},{"version":2,"key":"` + strings.Repeat("cd", 32) + `"}]}`
	if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	accepted, err := loadChangeConfig(SecurityConfig{Mode: "oidc"}, PeerPlaneConfig{})
	if err != nil || accepted.Options.CurrentKeyVersion != 2 || len(accepted.Options.CursorKeys) != 2 {
		t.Fatal("rotation ring", err)
	}
	t.Setenv("LANTERN_CDC_ENABLED", "false")
	if _, err := loadChangeConfig(off, PeerPlaneConfig{}); err == nil {
		t.Fatal("disabled CDC ignored key material")
	}
	t.Setenv("LANTERN_CDC_CURSOR_KEY_RING_FILE", "")
	if cfg, err := loadChangeConfig(off, PeerPlaneConfig{}); err != nil || cfg.Enabled {
		t.Fatal("disabled CDC", err)
	}
	t.Setenv("LANTERN_CDC_ENABLE", "false")
	if _, err := loadChangeConfig(off, PeerPlaneConfig{}); err == nil {
		t.Fatal("typo ignored")
	}
}
