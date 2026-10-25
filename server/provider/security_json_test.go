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

func TestSecurityJSONEnvironment(t *testing.T) {
	clearSecurityEnvironment(t)
	config, err := loadSecuritySetupJSON(inlineSecurityExample())
	if err != nil || validateSecuritySetup(config) != nil {
		t.Fatal("small browser-only setup rejected", err)
	}
	issuer := config.InitialIssuers[0]
	if issuer.URL != security.GoogleIssuer || !issuer.BrowserEnabled() || issuer.APIEnabled() || issuer.SecretRef != "google_web" || issuer.RedirectURI != config.BrowserOrigin+oidc.CallbackPath(security.GoogleIssuer) {
		t.Fatal("browser preset did not derive exact trust")
	}
	binding := config.SecretBindings[issuer.SecretRef]
	if binding.Path != "" || binding.Value != "private-example" || binding.Issuer != issuer.URL || binding.ClientID != issuer.ClientID || binding.TokenOrigin != "https://oauth2.googleapis.com" {
		t.Fatal("secret destination binding changed")
	}
	encoded, _ := json.Marshal(config.InitialIssuers)
	if strings.Contains(string(encoded), "private-example") || strings.Contains(string(encoded), "client_secret") {
		t.Fatal("secret entered replicated trust")
	}
	for _, finding := range envconfig.Findings() {
		if finding.Key == "LANTERN_SECURITY_CONFIG_JSON" {
			t.Fatal("private JSON entered diagnostics")
		}
	}
	if _, err := loadSecurityConfigJSON(inlineSecurityExample()); err == nil {
		t.Fatal("normal startup accepted setup intent instead of provisioned trust")
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
				t.Setenv("LANTERN_SECURITY_CONFIG_JSON", `{"provisioned_file":"/missing/security.json"}`)
				t.Setenv(name, value)
				if _, err := loadSecurityConfig(); err == nil {
					t.Fatal("competing source accepted")
				}
			})
		}
	}
	clearSecurityEnvironment(t)
	t.Setenv("LANTERN_SECURITY_CONFIG_JSON", inlineSecurityExample())
	if _, err := loadSecurityConfig(); err == nil {
		t.Fatal("setup ignored in OFF")
	}
	if err := os.Unsetenv("LANTERN_SECURITY_CONFIG_JSON"); err != nil {
		t.Fatal(err)
	}
	if c, err := loadSecurityConfig(); err != nil || c.Mode != "off" {
		t.Fatal("OFF acquired provisioning prerequisites", err)
	}
}

func TestSecurityJSONStrictInput(t *testing.T) {
	valid := inlineSecurityExample()
	for name, raw := range map[string]string{
		"empty": "", "null": "null", "oversized": strings.Repeat(" ", 120<<10+1),
		"old authority":  strings.Replace(valid, `"public_origin"`, `"authority":{},"public_origin"`, 1),
		"unknown nested": strings.Replace(valid, `"client_secret"`, `"secret_path"`, 1),
		"duplicate":      strings.Replace(valid, `"issuers":`, `"public_origin":"https://other.example","issuers":`, 1),
		"no issuer":      strings.Replace(valid, `"issuer":"google","client_id"`, `"issuer":"","client_id"`, 1),
		"plaintext":      strings.Replace(valid, "https://admin.example", "http://admin.example", 1),
		"origin path":    strings.Replace(valid, "https://admin.example", "https://admin.example/path", 1),
		"bad origin":     strings.Replace(valid, `"client_secret":`, `"token_origin":"https://oauth2.googleapis.com/token","client_secret":`, 1),
		"secret newline": strings.Replace(valid, `"private-example"`, `"private-example\n"`, 1),
		"invalid handle": strings.Replace(valid, `"id":"google_web"`, `"id":"/private/key"`, 1),
		"bad roots":      strings.Replace(valid, `"nodes":`, `"root_ca_pem":"private-example","nodes":`, 1),
		"proxy CIDR":     strings.Replace(valid, `"nodes":`, `"trusted_proxy_ips":["127.0.0.0/8"],"nodes":`, 1),
		"TLS pair":       strings.Replace(valid, `"nodes":`, `"tls":{"certificate_pem":"private-example"},"nodes":`, 1),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := loadSecuritySetupJSON(raw); err == nil {
				t.Fatal("invalid setup accepted")
			} else if strings.Contains(err.Error(), "private-example") {
				t.Fatal("secret disclosed")
			}
		})
	}
	for _, raw := range []string{`{}`, `{"authority":{}}`, `{"provisioned_file":"relative"}`, `{"provisioned_file":"/missing","unknown":true}`, `{"provisioned_file":"/missing","provisioned_file":"/other"}`} {
		if _, err := loadSecurityConfigJSON(raw); err == nil {
			t.Fatal("invalid runtime reference accepted")
		}
	}
}

func TestSecurityJSONNativeProfiles(t *testing.T) {
	var input securitySetupJSON
	if err := json.Unmarshal([]byte(inlineSecurityExample()), &input); err != nil {
		t.Fatal(err)
	}
	input.Issuers = []securityIssuerJSON{
		{Issuer: security.GoogleIssuer, NativeClients: []securityNativeClientJSON{{ID: "google_mobile", Kind: security.GoogleIDToken, ClientID: "google-backend", Presenters: []string{"ios-client", "android-client"}}}},
		{Issuer: security.AppleIssuer, NativeClients: []securityNativeClientJSON{{ID: "apple_ios", Kind: security.AppleNative, ClientID: "com.example.ios", TokenOrigin: security.AppleIssuer, ClientSecret: "ios-jwt"}, {ID: "apple_android", Kind: security.AppleAndroidWeb, ClientID: "com.example.web", AndroidPackage: "com.example.app", SecretID: "apple_android_secret", TokenOrigin: security.AppleIssuer, ClientSecret: "android-jwt"}}},
	}
	raw, _ := json.Marshal(input)
	c, err := loadSecuritySetupJSON(string(raw))
	if err != nil {
		t.Fatal(err)
	}
	google, apple := c.InitialIssuers[0], c.InitialIssuers[1]
	if google.BrowserEnabled() || google.APIEnabled() || google.NativeClients[0].Presenters[0] != "android-client" {
		t.Fatal("native Google acquired another profile")
	}
	if apple.NativeClients[0].SecretRef != "apple_android_secret" || apple.NativeClients[0].RedirectURI != c.BrowserOrigin+"/auth/native/apple/callback/apple_android" || len(c.SecretBindings) != 2 {
		t.Fatal("native Apple destination changed")
	}
	input.Issuers[0].NativeClients[0].ClientSecret = "forbidden"
	raw, _ = json.Marshal(input)
	if _, err := loadSecuritySetupJSON(string(raw)); err == nil {
		t.Fatal("Google native secret accepted")
	}
}

func TestSecurityIssuerGooglePresetPins(t *testing.T) {
	for _, name := range []string{"google", security.GoogleIssuer} {
		base := securityIssuerJSON{Issuer: name, ClientID: "browser-client", ClientSecret: "synthetic-google-secret"}
		profile, err := securityIssuerPreset(base)
		if err != nil || profile.Issuer != security.GoogleIssuer || len(profile.Algorithms) != 1 || profile.Algorithms[0] != "RS256" || profile.TokenOrigin != "https://oauth2.googleapis.com" {
			t.Fatal("Google preset did not pin its provider contract", err)
		}
		for _, mutate := range []func(*securityIssuerJSON){
			func(i *securityIssuerJSON) { i.Algorithms = []string{"EdDSA"} },
			func(i *securityIssuerJSON) { i.Algorithms = []string{"RS256", "ES256"} },
			func(i *securityIssuerJSON) { i.TokenOrigin = "https://attacker.example" },
			func(i *securityIssuerJSON) { i.TokenOrigin = "https://oauth2.googleapis.com/token" },
		} {
			input := base
			mutate(&input)
			if _, err := securityIssuerPreset(input); err == nil || strings.Contains(err.Error(), base.ClientSecret) {
				t.Fatal("Google preset accepted changed trust or disclosed its secret")
			}
		}
	}
}
