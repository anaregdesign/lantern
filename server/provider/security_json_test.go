package provider

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func inlineSecurityExample() string {
	return `{"public_origin":"https://admin.example","authority":{},"issuers":[{"id":"google_web","issuer":"https://accounts.google.com","client_id":"web-client","api_audience":"lantern-api","token_origin":"https://oauth2.googleapis.com","client_secret":"private-example"}],"cdc":{"enabled":false}}`
}

func TestSecurityJSONEnvironment(t *testing.T) {
	clearSecurityEnvironment(t)
	t.Setenv("LANTERN_AUTH_MODE", "oidc")
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", inlineSecurityExample())
	c, err := loadSecurityConfig()
	if err != nil {
		t.Fatal("inline example rejected", err)
	}
	if c.Mode != "oidc" || c.StoreMode != "resume" || c.ConfigFile != "" || c.RootCAFile != "" || string(c.AuthorityJSON) != "{}" {
		t.Fatal("inline startup selected another authority source or lifecycle")
	}
	issuer := c.InitialIssuers[0]
	if issuer.URL != security.GoogleIssuer || !issuer.Enabled || issuer.SecretRef != "google_web" ||
		issuer.RedirectURI != c.BrowserOrigin+oidc.CallbackPath(security.GoogleIssuer) || len(issuer.Algorithms) != 1 || issuer.Algorithms[0] != "RS256" {
		t.Fatal("inline Issuer did not derive the exact browser trust")
	}
	binding := c.SecretBindings[issuer.SecretRef]
	if binding.Path != "" || binding.Value != "private-example" || binding.Issuer != issuer.URL || binding.ClientID != issuer.ClientID || binding.TokenOrigin != "https://oauth2.googleapis.com" {
		t.Fatal("inline client secret is not exactly bound")
	}
	encoded, err := json.Marshal(c.InitialIssuers)
	if err != nil || strings.Contains(string(encoded), "private-example") || strings.Contains(string(encoded), "client_secret") {
		t.Fatal("client secret entered committed Issuer configuration")
	}
	for _, finding := range envconfig.Findings() {
		if finding.Key == "LANTERN_SECURITY_CONFIG_JSON" {
			t.Fatal("raw security JSON entered malformed-value diagnostics")
		}
	}
	// The startup config is detached from subsequent process-environment updates.
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", "changed-after-start")
	if c.SecretBindings[issuer.SecretRef].Value != "private-example" {
		t.Fatal("running credentials changed with the environment")
	}
}

func TestSecurityJSONRejectsMixedSources(t *testing.T) {
	for _, name := range securityEnvironmentNames {
		if name == "LANTERN_SECURITY_CONFIG_JSON" {
			continue
		}
		for _, value := range []string{"", "legacy"} {
			t.Run(name+"/"+value, func(t *testing.T) {
				clearSecurityEnvironment(t)
				t.Setenv("LANTERN_AUTH_MODE", "oidc")
				t.Setenv("LANTERN_SECURITY_CONFIG_JSON", inlineSecurityExample())
				t.Setenv(name, value)
				if _, err := loadSecurityConfig(); err == nil {
					t.Fatal("inline configuration ignored a competing source")
				}
			})
		}
	}
	clearSecurityEnvironment(t)
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", inlineSecurityExample())
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("inline input was ignored in default OFF")
	}
	if err := os.Unsetenv("LANTERN_SECURITY_CONFIG_JSON"); err != nil {
		t.Fatal(err)
	}
	if c, err := loadSecurityConfig(); err != nil || c.Mode != "off" {
		t.Fatal("default OFF acquired an inline prerequisite", err)
	}
}

func TestSecurityJSONStrictInput(t *testing.T) {
	valid := inlineSecurityExample()
	for name, raw := range map[string]string{
		"empty":                 "",
		"null":                  "null",
		"oversized":             strings.Repeat(" ", 120<<10+1),
		"unknown field":         strings.Replace(valid, `"public_origin"`, `"browser_origin"`, 1),
		"unknown nested field":  strings.Replace(valid, `"client_secret"`, `"secret_path"`, 1),
		"duplicate field":       strings.Replace(valid, `"authority":{}`, `"authority":{},"authority":{}`, 1),
		"missing authority":     strings.Replace(valid, `"authority":{},`, "", 1),
		"null authority":        strings.Replace(valid, `"authority":{}`, `"authority":null`, 1),
		"array authority":       strings.Replace(valid, `"authority":{}`, `"authority":[]`, 1),
		"no Issuers":            `{"public_origin":"https://admin.example","authority":{},"issuers":[]}`,
		"plaintext origin":      strings.Replace(valid, "https://admin.example", "http://admin.example", 1),
		"origin path":           strings.Replace(valid, "https://admin.example", "https://admin.example/path", 1),
		"missing audience":      strings.Replace(valid, `"api_audience":"lantern-api",`, "", 1),
		"missing secret origin": strings.Replace(valid, `"token_origin":"https://oauth2.googleapis.com",`, "", 1),
		"secret origin path":    strings.Replace(valid, `"https://oauth2.googleapis.com"`, `"https://oauth2.googleapis.com/token"`, 1),
		"secret newline":        strings.Replace(valid, `"private-example"`, `"private-example\n"`, 1),
		"invalid handle":        strings.Replace(valid, `"id":"google_web"`, `"id":"/private/key"`, 1),
		"invalid roots":         strings.Replace(valid, `"authority":{}`, `"authority":{},"root_ca_pem":"private-example"`, 1),
		"private CIDR wildcard": strings.Replace(valid, `"authority":{}`, `"authority":{},"private_origins":{"https://idp.example":["0.0.0.0/0"]}`, 1),
		"proxy CIDR":            strings.Replace(valid, `"authority":{}`, `"authority":{},"trusted_proxy_ips":["127.0.0.0/8"]`, 1),
		"TLS pair":              strings.Replace(valid, `"authority":{}`, `"authority":{},"tls":{"certificate_pem":"private-example"}`, 1),
		"invalid TLS values":    strings.Replace(valid, `"authority":{}`, `"authority":{},"tls":{"certificate_pem":"private-example","private_key_pem":"private-example"}`, 1),
		"invalid CDC ring":      strings.Replace(valid, `"enabled":false`, `"enabled":true`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSecurityConfigJSON(raw); err == nil {
				t.Fatal("ambiguous or incomplete inline configuration accepted")
			} else if strings.Contains(err.Error(), "private-example") {
				t.Fatal("configuration error exposed a secret")
			}
		})
	}
	t.Run("duplicate Issuer", func(t *testing.T) {
		var input securityJSON
		if err := json.Unmarshal([]byte(valid), &input); err != nil {
			t.Fatal(err)
		}
		input.Issuers = append(input.Issuers, input.Issuers[0])
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := loadSecurityConfigJSON(string(raw)); err == nil {
			t.Fatal("duplicate Issuer accepted")
		}
	})
}

func TestSecurityJSONNativeProfiles(t *testing.T) {
	raw := `{"public_origin":"https://admin.example","authority":{},"issuers":[
		{"issuer":"https://accounts.google.com","native_clients":[{"id":"google_mobile","kind":"google_id_token","client_id":"google-backend","presenters":["ios-client","android-client"]}]},
		{"issuer":"https://appleid.apple.com","native_clients":[
			{"id":"apple_ios","kind":"apple_native","client_id":"com.example.ios","token_origin":"https://appleid.apple.com","client_secret":"ios-jwt"},
			{"id":"apple_android","kind":"apple_android_web","client_id":"com.example.web","android_package":"com.example.app","secret_id":"apple_android_secret","token_origin":"https://appleid.apple.com","client_secret":"android-jwt"}]}]}`
	c, err := loadSecurityConfigJSON(raw)
	if err != nil {
		t.Fatal("native profiles rejected", err)
	}
	google, apple := c.InitialIssuers[0], c.InitialIssuers[1]
	if google.BrowserEnabled() || google.NativeClients[0].Presenters[0] != "android-client" || google.NativeClients[0].ServiceOrigin != c.BrowserOrigin {
		t.Fatal("native Google profile acquired browser credentials or wrong trust")
	}
	android, ios := apple.NativeClients[0], apple.NativeClients[1]
	if android.ID != "apple_android" || android.SecretRef != "apple_android_secret" || android.RedirectURI != c.BrowserOrigin+"/auth/native/apple/callback/apple_android" || ios.RedirectURI != "" || len(ios.SecretRef) != 64 {
		t.Fatal("native Apple callbacks or audience-specific bindings changed")
	}
	if len(c.SecretBindings) != 2 || c.SecretBindings[ios.SecretRef].Value != "ios-jwt" || c.SecretBindings[android.SecretRef].Value != "android-jwt" {
		t.Fatal("native credentials were combined or lost")
	}
	if _, err := loadSecurityConfigJSON(strings.Replace(raw, `"id":"google_mobile"`, `"id":"google_mobile","client_secret":"forbidden"`, 1)); err == nil {
		t.Fatal("native Google client accepted a secret")
	}
}
