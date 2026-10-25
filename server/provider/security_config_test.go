package provider

import "testing"

func authorityConfigEnvironment(t *testing.T) {
	t.Helper()
	clearSecurityEnvironment(t)
	for name, value := range map[string]string{
		"LANTERN_AUTH_MODE": "oidc", "LANTERN_SECURITY_CONFIG_FILE": "/operator/node.json",
		"LANTERN_SECURITY_STORE_MODE": "fresh", "LANTERN_OIDC_BROWSER_ORIGIN": "https://admin.example",
	} {
		t.Setenv(name, value)
	}
}

func TestSecurityConfigStrictOperatorContract(t *testing.T) {
	for _, tc := range []struct {
		name, key, value string
		wantError        bool
	}{
		{name: "complete"},
		{name: "resume", key: "LANTERN_SECURITY_STORE_MODE", value: "resume"},
		{name: "empty mode", key: "LANTERN_AUTH_MODE", wantError: true},
		{name: "unknown mode", key: "LANTERN_AUTH_MODE", value: "OIDC", wantError: true},
		{name: "missing provisioning", key: "LANTERN_SECURITY_CONFIG_FILE", wantError: true},
		{name: "relative provisioning", key: "LANTERN_SECURITY_CONFIG_FILE", value: "node.json", wantError: true},
		{name: "legacy restart", key: "LANTERN_SECURITY_STORE_MODE", value: "restart", wantError: true},
		{name: "plaintext browser", key: "LANTERN_OIDC_BROWSER_ORIGIN", value: "http://admin.example", wantError: true},
		{name: "browser path", key: "LANTERN_OIDC_BROWSER_ORIGIN", value: "https://admin.example/path", wantError: true},
		{name: "relative roots", key: "LANTERN_OIDC_ROOT_CA_FILE", value: "ca.pem", wantError: true},
		{name: "empty typo", key: "LANTERN_SECURITY_CONFIG_FIEL", wantError: true},
		{name: "case typo", key: "lantern_security_config_file", value: "/operator/node.json", wantError: true},
		{name: "wildcard private origin", key: "LANTERN_OIDC_PRIVATE_ORIGINS", value: `{"https://idp.example":["0.0.0.0/0"]}`, wantError: true},
		{name: "private origin path", key: "LANTERN_OIDC_PRIVATE_ORIGINS", value: `{"https://idp.example/path":["127.0.0.1/32"]}`, wantError: true},
		{name: "unknown secret field", key: "LANTERN_OIDC_SECRET_BINDINGS", value: `{"binding":{"secret":"placeholder"}}`, wantError: true},
		{name: "duplicate secret field", key: "LANTERN_OIDC_SECRET_BINDINGS", value: `{"binding":{"issuer":"https://idp.example","issuer":"https://other.example"}}`, wantError: true},
		{name: "proxy CIDR", key: "LANTERN_OIDC_TRUSTED_PROXY_IPS", value: `["127.0.0.0/8"]`, wantError: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			authorityConfigEnvironment(t)
			if tc.key != "" {
				t.Setenv(tc.key, tc.value)
			}
			c, err := loadSecurityConfig()
			if (err != nil) != tc.wantError {
				t.Fatal("operator contract", err)
			}
			if err == nil && (c.Mode != "oidc" || c.ConfigFile != "/operator/node.json") {
				t.Fatal("OIDC was downgraded or selected another source")
			}
		})
	}
}

func TestSecurityConfigRetiredSettingsRejectedByPresence(t *testing.T) {
	retired := []string{
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
		if err != nil || c.Mode != "off" || c.ConfigFile != "" {
			t.Fatal("OFF prerequisites", err)
		}
	}
	t.Setenv("LANTERN_SECURITY_CONFIG_FILE", "")
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("OFF ignored a present authority setting")
	}
	if validateSecurityConfig(SecurityConfig{Mode: "off", ConfigFile: "/operator/ignored"}) == nil {
		t.Fatal("direct OFF composition ignored authority input")
	}
}
