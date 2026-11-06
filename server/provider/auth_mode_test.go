package provider

import (
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

func TestAuthModePreflight(t *testing.T) {
	type authModeTest struct {
		name          string
		env           []string
		wantError     bool
		wantLoadError bool
	}
	tests := []authModeTest{
		{name: "unset"},
		{name: "explicit off", env: []string{"LANTERN_AUTH_MODE=off"}},
		{name: "explicit OIDC requires complete configuration", env: []string{"LANTERN_AUTH_MODE=oidc"}, wantLoadError: true},
		{name: "empty mode", env: []string{"LANTERN_AUTH_MODE="}, wantError: true, wantLoadError: true},
		{name: "unknown mode", env: []string{"LANTERN_AUTH_MODE=legacy"}, wantError: true, wantLoadError: true},
		{name: "mode value case variant", env: []string{"LANTERN_AUTH_MODE=OIDC"}, wantError: true, wantLoadError: true},
		{name: "retired token in implicit off", env: []string{"LANTERN_AUTH_TOKENS="}, wantError: true, wantLoadError: true},
		{name: "partial trust in implicit off", env: []string{"LANTERN_OIDC_ADMIN_ISSUER="}, wantError: true, wantLoadError: true},
		{name: "security JSON in implicit off", env: []string{"LANTERN_SECURITY_CONFIG_JSON={}"}, wantError: true, wantLoadError: true},
	}
	for _, mode := range []string{"off", "oidc"} {
		for _, setting := range []string{
			"LANTERN_AUTH_TOKENS=legacy-token",
			"LANTERN_AUTH_TOKENS=",
			"LANTERN_AUTH_EXEMPT_REFLECTION=true",
			"LANTERN_AUTH_EXEMPT_REFLECTION=false",
			"LANTERN_AUTH_EXEMPT_REFLECTION=",
			"LANTERN_AUTH_TOKEN=token",
			"LANTERN_AUTH_UNKNOWN=",
			"lantern_auth_tokens=",
			"LANTERN_Auth_EXEMPT_REFLECTION=false",
			"lantern_auth_mode=off",
			"LANTERN_AUTH_Mode=oidc",
		} {
			tests = append(tests, authModeTest{
				name:          mode + " rejects " + setting,
				env:           []string{"LANTERN_AUTH_MODE=" + mode, "LANTERN_STRICT_CONFIG=false", setting},
				wantError:     true,
				wantLoadError: true,
			})
		}
		for _, setting := range []string{
			"LANTERN_OIDC_ADMIN_ISSUER=https://idp.example",
			"LANTERN_OIDC_ADMIN_ISSUER=",
			"LANTERN_OIDC_UNKNOWN=",
			"LANTERN_OIdC_ADMIN_ISSUER=https://idp.example",
			"LANTERN_SECURITY_STORE_DIR=/tmp/sys",
			"LANTERN_SECURITY_STORE_DIR=",
			"LANTERN_SECURITY_UNKNOWN=",
			"LANTERN_SECURITY_CONFIG_JSON=",
			"LANTERN_SECURITY_CONFIG_JSON={}",
			"lantern_security_config_json={}",
		} {
			tests = append(tests, authModeTest{
				name:          mode + " rejects partial or unknown trust " + setting,
				env:           []string{"LANTERN_AUTH_MODE=" + mode, "LANTERN_STRICT_CONFIG=false", setting},
				wantError:     mode == "off",
				wantLoadError: true,
			})
		}
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			// Use captured inputs so ambient settings and earlier loads cannot
			// hide a present-empty, unknown, partial or case-variant setting.
			err := validateAuthMode(envconfig.NewReader(test.env))
			if (err != nil) != test.wantError {
				t.Fatalf("preflight = %v, want error %v", err, test.wantError)
			}
			cfg, err := LoadConfig(envconfig.NewReader(test.env))
			if (err != nil) != test.wantLoadError {
				t.Fatalf("LoadConfig = %v, want error %v", err, test.wantLoadError)
			}
			if test.wantLoadError && cfg != nil {
				t.Fatal("invalid auth reached configuration publication")
			}
			if !test.wantLoadError && (cfg == nil || cfg.Security.Mode != "off") {
				t.Fatal("valid OFF configuration was not preserved")
			}
		})
	}
}
