package provider

import (
	"os"
	"strings"
	"testing"
)

func TestAuthModePreflight(t *testing.T) {
	for _, test := range []struct {
		name      string
		env       map[string]string
		wantError bool
	}{
		{"unset", nil, false},
		{"explicit off", map[string]string{"LANTERN_AUTH_MODE": "off"}, false},
		{"retired token", map[string]string{"LANTERN_AUTH_TOKENS": "legacy-token"}, true},
		{"empty mode", map[string]string{"LANTERN_AUTH_MODE": ""}, true},
		{"unknown mode", map[string]string{"LANTERN_AUTH_MODE": "OIDC"}, true},
		{"OIDC explicit mode", map[string]string{"LANTERN_AUTH_MODE": "oidc"}, false},
		{"implicit Issuer", map[string]string{"LANTERN_OIDC_ADMIN_ISSUER": "https://idp.example"}, true},
		{"empty Issuer", map[string]string{"LANTERN_OIDC_ADMIN_ISSUER": ""}, true},
		{"implicit system setting", map[string]string{"LANTERN_SECURITY_STORE_DIR": "/tmp/sys"}, true},
		{"auth typo", map[string]string{"LANTERN_AUTH_TOKEN": "token"}, true},
		{"Issuer case typo", map[string]string{"LANTERN_OIdC_ADMIN_ISSUER": "https://idp.example"}, true},
		{"off with Issuer", map[string]string{"LANTERN_AUTH_MODE": "off", "LANTERN_OIDC_ADMIN_ISSUER": "https://idp.example"}, true},
		{"off with token", map[string]string{"LANTERN_AUTH_MODE": "off", "LANTERN_AUTH_TOKENS": "legacy-token"}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, setting := range os.Environ() {
				name, _, _ := strings.Cut(setting, "=")
				if strings.HasPrefix(name, "LANTERN_AUTH_") || strings.HasPrefix(name, "LANTERN_OIDC_") || strings.HasPrefix(name, "LANTERN_SECURITY_") {
					t.Setenv(name, "")
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				}
			}
			for name, value := range test.env {
				t.Setenv(name, value)
			}
			err := validateAuthMode()
			if (err != nil) != test.wantError {
				t.Fatalf("preflight = %v", err)
			}
			if test.name == "OIDC explicit mode" {
				if cfg, err := NewConfig(); err == nil || cfg != nil {
					t.Fatal("partial OIDC configuration accepted")
				}
			}
			if test.wantError {
				if cfg, err := NewConfig(); err == nil || cfg != nil {
					t.Fatal("invalid auth reached configuration publication")
				}
			}
		})
	}
}
