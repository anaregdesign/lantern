package provider

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCurrentSecurityConfigRefusesLegacyAndTimeAssertions(t *testing.T) {
	base := SecurityConfig{Mode: "oidc", Profile: "current-v2", CurrentConfigFile: "/operator/node.json", StoreMode: "fresh", BrowserOrigin: "https://admin.example"}
	if err := validateSecurityConfig(base); err != nil {
		t.Fatal(err)
	}
	for name, change := range map[string]func(*SecurityConfig){
		"missing profile": func(c *SecurityConfig) { c.Profile = "" },
		"unknown profile": func(c *SecurityConfig) { c.Profile = "current-v3" },
		"legacy store":    func(c *SecurityConfig) { c.StorePath = "/operator/old.wal" },
		"writer":          func(c *SecurityConfig) { c.NodeRole = "writer" },
		"writer key":      func(c *SecurityConfig) { c.WriterPublicKeyFile = "/operator/writer.pub" },
		"clock assertion": func(c *SecurityConfig) { c.ClockQualified = true },
		"arbitrary clock": func(c *SecurityConfig) { c.Clock = time.Now },
		"old bootstrap":   func(c *SecurityConfig) { c.Bootstrap = security.Bootstrap{Revision: 1} },
		"old generation":  func(c *SecurityConfig) { c.Generation = [16]byte{1} },
		"relative config": func(c *SecurityConfig) { c.CurrentConfigFile = "node.json" },
		"legacy restart":  func(c *SecurityConfig) { c.StoreMode = "restart" },
	} {
		t.Run(name, func(t *testing.T) {
			c := base
			change(&c)
			if validateSecurityConfig(c) == nil {
				t.Fatal("mixed profile accepted")
			}
		})
	}
	for _, setting := range os.Environ() {
		name, _, _ := strings.Cut(setting, "=")
		if strings.HasPrefix(name, "LANTERN_SECURITY_") || strings.HasPrefix(name, "LANTERN_OIDC_") {
			t.Setenv(name, "")
			if err := os.Unsetenv(name); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, value := range map[string]string{"LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_PROFILE": "current-v2", "LANTERN_SECURITY_CURRENT_CONFIG_FILE": "/operator/node.json", "LANTERN_SECURITY_STORE_MODE": "fresh", "LANTERN_OIDC_BROWSER_ORIGIN": "https://admin.example"} {
		t.Setenv(name, value)
	}
	c, err := loadSecurityConfig()
	if err != nil || c.Profile != "current-v2" || c.Clock != nil || c.ClockQualified || c.WriterPublicKeyFile != "" || c.Bootstrap.Revision != 0 {
		t.Fatal("current env fallback", err)
	}
	t.Setenv("LANTERN_SECURITY_CLOCK_QUALIFIED", "true")
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("ignored clock assertion")
	}
}
