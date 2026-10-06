package provider

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityConfigStrictOperatorContract(t *testing.T) {
	for _, test := range []struct {
		name       string
		key, value string
		remove     bool
		wantError  bool
	}{
		{name: "complete"},
		{name: "machine bootstrap"},
		{name: "relative machine file", key: "LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE", value: "machines.json", wantError: true},
		{name: "missing subjects", key: "LANTERN_OIDC_ADMIN_SUBJECTS", remove: true, wantError: true},
		{name: "empty subjects", key: "LANTERN_OIDC_ADMIN_SUBJECTS", value: "[]", wantError: true},
		{name: "duplicate subjects", key: "LANTERN_OIDC_ADMIN_SUBJECTS", value: `["admin","admin"]`, wantError: true},
		{name: "email is not inferred", key: "LANTERN_OIDC_ADMIN_SUBJECTS", value: `["Admin@example.com"]`},
		{name: "qualified human namespace", key: "LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED", value: "true"},
		{name: "mixed human namespace", key: "LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED", value: "false"},
		{name: "ambiguous namespace assertion", key: "LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED", value: "yes", wantError: true},
		{name: "shared callback", key: "LANTERN_OIDC_REDIRECT_URI", value: "https://admin.example/auth/callback", wantError: true},
		{name: "plaintext origin", key: "LANTERN_OIDC_BROWSER_ORIGIN", value: "http://admin.example", wantError: true},
		{name: "missing client", key: "LANTERN_OIDC_CLIENT_ID", remove: true, wantError: true},
		{name: "missing audience", key: "LANTERN_OIDC_API_AUDIENCE", remove: true, wantError: true},
		{name: "unknown algorithm", key: "LANTERN_OIDC_ALGORITHMS", value: `["HS256"]`, wantError: true},
		{name: "relative journal", key: "LANTERN_SECURITY_STORE_PATH", value: "state.wal", wantError: true},
		{name: "zero generation", key: "LANTERN_SECURITY_GENERATION", value: strings.Repeat("0", 32), wantError: true},
		{name: "clock unqualified", key: "LANTERN_SECURITY_CLOCK_QUALIFIED", value: "false", wantError: true},
		{name: "unknown control mode", key: "LANTERN_SECURITY_NODE_ROLE", value: "leader", wantError: true},
		{name: "configuration typo", key: "LANTERN_OIDC_ADMIN_SUBECTS", value: `["admin"]`, wantError: true},
		{name: "wildcard SSRF exception", key: "LANTERN_OIDC_PRIVATE_ORIGINS", value: `{"https://idp.example":["0.0.0.0/0"]}`, wantError: true},
		{name: "noncanonical SSRF exception", key: "LANTERN_OIDC_PRIVATE_ORIGINS", value: `{"https://idp.example/path":["127.0.0.1/32"]}`, wantError: true},
		{name: "unknown secret field", key: "LANTERN_OIDC_SECRET_BINDINGS", value: `{"binding":{"issuer":"https://idp.example","client_id":"admin","token_origin":"https://idp.example","path":"/tmp/client","secret":"leaked"}}`, wantError: true},
		{name: "duplicate secret field", key: "LANTERN_OIDC_SECRET_BINDINGS", value: `{"binding":{"issuer":"https://idp.example","issuer":"https://other.example"}}`, wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, setting := range os.Environ() {
				name, _, _ := strings.Cut(setting, "=")
				upper := strings.ToUpper(name)
				if strings.HasPrefix(upper, "LANTERN_AUTH_") || strings.HasPrefix(upper, "LANTERN_OIDC_") || strings.HasPrefix(upper, "LANTERN_SECURITY_") {
					t.Setenv(name, "")
					if err := os.Unsetenv(name); err != nil {
						t.Fatal(err)
					}
				}
			}
			for name, value := range map[string]string{
				"LANTERN_AUTH_MODE": "oidc", "LANTERN_OIDC_ADMIN_ISSUER": "https://idp.example", "LANTERN_OIDC_ADMIN_SUBJECTS": `["admin"]`,
				"LANTERN_OIDC_CLIENT_ID": "admin", "LANTERN_OIDC_API_AUDIENCE": "api", "LANTERN_OIDC_BROWSER_ORIGIN": "https://admin.example",
				"LANTERN_OIDC_REDIRECT_URI":   "https://admin.example" + oidc.CallbackPath("https://idp.example"),
				"LANTERN_SECURITY_STORE_MODE": "fresh", "LANTERN_SECURITY_STORE_PATH": "/tmp/security.wal", "LANTERN_SECURITY_GENERATION": "01000000000000000000000000000000",
				"LANTERN_SECURITY_NODE_ROLE": "writer", "LANTERN_SECURITY_WRITER_KEY_FILE": "/tmp/writer.key", "LANTERN_SECURITY_WRITER_PUBLIC_KEY_FILE": "/tmp/writer.pub",
				"LANTERN_SECURITY_WRITER_ENDPOINT": "https://peer.example", "LANTERN_SECURITY_BOOTSTRAP_REVISION": "1", "LANTERN_SECURITY_CLOCK_QUALIFIED": "true",
			} {
				t.Setenv(name, value)
			}
			if test.key != "" {
				if test.remove {
					if err := os.Unsetenv(test.key); err != nil {
						t.Fatal(err)
					}
				} else {
					t.Setenv(test.key, test.value)
				}
			}
			if test.name == "machine bootstrap" {
				token, err := security.NewMachineToken()
				if err != nil {
					t.Fatal(err)
				}
				path := filepath.Join(t.TempDir(), "machines.json")
				now := time.Now().UTC().Format(time.RFC3339Nano)
				expires := time.Now().Add(time.Hour).UTC().Format(time.RFC3339Nano)
				if err := os.WriteFile(path, []byte(`[{"name":"worker","role_ids":["reader"],"credentials":[{"token":"`+token+`","created_at":"`+now+`","expires_at":"`+expires+`"}]}]`), 0600); err != nil {
					t.Fatal(err)
				}
				t.Setenv("LANTERN_SECURITY_MACHINE_BOOTSTRAP_FILE", path)
				t.Setenv("LANTERN_SECURITY_BOOTSTRAP_ROLES", `[{"id":"reader","rules":[{"id":"read","effect":"allow","action":"vertex.read","resource":"data","prefix":"orders:"}]}]`)
			}
			config, err := loadSecurityConfig()
			if (err != nil) != test.wantError {
				t.Fatal("contract mismatch", err)
			}
			if err == nil && (config.Mode != "oidc" || config.Bootstrap.AdminSubjects[0] == "" || config.MaxJournalBytes != 64<<20) {
				t.Fatal("configuration was downgraded")
			}
			if test.name == "machine bootstrap" && (len(config.Bootstrap.Machines) != 1 || len(config.Bootstrap.Machines[0].Credentials) != 1) {
				t.Fatal("machine configuration lost")
			}
			if err == nil && config.Bootstrap.Issuer.HumanSubjectNamespaceQualified != (test.value == "true" && test.key == "LANTERN_OIDC_HUMAN_SUBJECT_NAMESPACE_QUALIFIED") {
				t.Fatal("human namespace qualification was inferred")
			}
		})
	}
}
func TestSecurityConfigDefaultOffHasNoPrerequisites(t *testing.T) {
	t.Setenv("LANTERN_AUTH_MODE", "off")
	config, err := loadSecurityConfig()
	if err != nil || config.Mode != "off" || config.StorePath != "" || config.Generation != [16]byte{} {
		t.Fatal(config.Mode, err)
	}
	t.Setenv("LANTERN_OIDC_ADMIN_ISSUER", "https://idp.example")
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("OFF silently ignored configured trust")
	}
	if err := validateSecurityConfig(SecurityConfig{Mode: "off", StorePath: "/tmp/ignored"}); err == nil {
		t.Fatal("direct OFF composition ignored security state")
	}
}
