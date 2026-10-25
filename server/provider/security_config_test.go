package provider

import (
	"os"
	"testing"
)

func authorityConfigEnvironment(t *testing.T) {
	t.Helper()
	clearSecurityEnvironment(t)
	t.Setenv("LANTERN_AUTH_MODE", "oidc")
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", inlineSecurityExample())
}

func TestSecurityConfigRequiresCurrentJSONOnEveryInvocation(t *testing.T) {
	clearSecurityEnvironment(t)
	t.Setenv("LANTERN_AUTH_MODE", "oidc")
	for _, raw := range []string{"", "null", `{}`, `{"provisioned_file":"/old/config.json"}`, `{"version":1}`} {
		t.Setenv("LANTERN_SECURITY_CONFIG_JSON", raw)
		if _, err := loadSecurityConfig(); err == nil {
			t.Fatal("missing,malformed or obsolete configuration accepted")
		}
	}
	if err := os.Unsetenv("LANTERN_SECURITY_CONFIG_JSON"); err != nil {
		t.Fatal(err)
	}
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("missing configuration acquired persisted fallback")
	}
}

func TestSecurityConfigRetiredSettingsRejectedByPresence(t *testing.T) {
	retired := []string{
		"LANTERN_SECURITY_CONFIG_FILE", "LANTERN_SECURITY_STORE_MODE", "LANTERN_OIDC_BROWSER_ORIGIN", "LANTERN_OIDC_SECRET_BINDINGS", "LANTERN_OIDC_PRIVATE_ORIGINS", "LANTERN_OIDC_ROOT_CA_FILE", "LANTERN_OIDC_TRUSTED_PROXY_IPS",
		"LANTERN_SECURITY_PROFILE", "LANTERN_SECURITY_CURRENT_CONFIG_FILE",
		"LANTERN_SECURITY_STORE_PATH", "LANTERN_SECURITY_GENERATION", "LANTERN_SECURITY_NODE_ROLE",
		"LANTERN_SECURITY_WRITER_KEY_FILE", "LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE", "LANTERN_SECURITY_WRITER_ENDPOINT",
		"LANTERN_SECURITY_BOOTSTRAP_REVISION", "LANTERN_SECURITY_BOOTSTRAP_ROLES", "LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE",
		"LANTERN_SECURITY_MAX_JOURNAL_BYTES", "LANTERN_SECURITY_CLOCK_QUALIFIED",
		"LANTERN_OIDC_ADMIN_ISSUER", "LANTERN_OIDC_ADMIN_SUBJECTS", "LANTERN_OIDC_CLIENT_ID", "LANTERN_OIDC_API_AUDIENCE",
		"LANTERN_OIDC_REDIRECT_URI", "LANTERN_OIDC_ALGORITHMS", "LANTERN_OIDC_SECRET_REF", "LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED",
	}
	for _, mode := range []string{"off", "oidc"} {
		for _, name := range retired {
			for _, value := range []string{"", "retired-value"} {
				t.Run(mode+"/"+name+"/"+value, func(t *testing.T) {
					clearSecurityEnvironment(t)
					if mode == "oidc" {
						authorityConfigEnvironment(t)
					} else {
						t.Setenv("LANTERN_AUTH_MODE", mode)
					}
					t.Setenv(name, value)
					if _, err := loadSecurityConfig(); err == nil {
						t.Fatal("retired input was ignored")
					}
				})
			}
		}
	}
}

func TestSecurityConfigDefaultOffHasNoPrerequisites(t *testing.T) {
	clearSecurityEnvironment(t)
	for _, explicit := range []bool{false, true} {
		if explicit {
			t.Setenv("LANTERN_AUTH_MODE", "off")
		}
		c, err := loadSecurityConfig()
		if err != nil || c.Mode != "off" || c.MaterialFile != "" {
			t.Fatal("OFF prerequisites", err)
		}
	}
	t.Setenv("LANTERN_SECURITY_CONFIG_FILE", "")
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("OFF ignored a present authority setting")
	}
	if validateSecurityConfig(SecurityConfig{Mode: "off", MaterialFile: "/operator/ignored"}) == nil {
		t.Fatal("direct OFF composition ignored authority input")
	}
}
