package security

import (
	"strings"
	"testing"
)

func TestIssuerIndependentBrowserAndAPIProfiles(t *testing.T) {
	base := Issuer{URL: "https://idp.example", Enabled: true, Algorithms: []string{"RS256"}}
	for _, tc := range []struct {
		name    string
		browser bool
		api     bool
	}{
		{"browser only", true, false},
		{"API only", false, true},
		{"browser and API", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := base.clone()
			if tc.browser {
				issuer.ClientID, issuer.RedirectURI = "browser-client", "https://lantern.example/auth/callback"
			}
			if tc.api {
				issuer.APIAudience, issuer.HumanSubjectNamespaceQualified = "api", true
			}
			if err := ValidateIssuerConfiguration(issuer); err != nil {
				t.Fatal(err)
			}
			if issuer.BrowserEnabled() != tc.browser || issuer.APIEnabled() != tc.api {
				t.Fatal("one profile enabled another profile")
			}
		})
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Issuer)
	}{
		{"no profile", func(*Issuer) {}},
		{"client without redirect", func(i *Issuer) { i.ClientID = "browser" }},
		{"redirect without client", func(i *Issuer) { i.RedirectURI = "https://lantern.example/auth/callback" }},
		{"API with partial browser", func(i *Issuer) { i.APIAudience, i.ClientID = "api", "browser" }},
		{"API with orphan secret", func(i *Issuer) { i.APIAudience, i.SecretRef = "api", "secret" }},
		{"oversized audience", func(i *Issuer) { i.APIAudience = strings.Repeat("a", 513) }},
		{"browser RFC9068 qualification", func(i *Issuer) {
			i.ClientID, i.RedirectURI, i.HumanSubjectNamespaceQualified = "browser", "https://lantern.example/auth/callback", true
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			issuer := base.clone()
			tc.mutate(&issuer)
			if ValidateIssuerConfiguration(issuer) == nil {
				t.Fatal("incomplete or invalid profile accepted")
			}
		})
	}
}

func TestIssuerNativeConfiguration(t *testing.T) {
	issuer := Issuer{URL: GoogleIssuer, Enabled: true, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{{ID: "mobile", Kind: GoogleIDToken, ClientID: "web-client", ServiceOrigin: "https://lantern.example", Presenters: []string{"android-client", "ios-client"}}}}
	if err := ValidateIssuerConfiguration(issuer); err != nil || issuer.BrowserEnabled() {
		t.Fatalf("native-only configuration: %v", err)
	}
	for _, tc := range []struct {
		name   string
		mutate func(*Issuer)
	}{
		{"wrong issuer", func(i *Issuer) { i.URL = "https://other.example" }},
		{"no RS256", func(i *Issuer) { i.Algorithms = []string{"ES256"} }},
		{"unpinned Google algorithms", func(i *Issuer) { i.Algorithms = []string{"RS256", "ES256"} }},
		{"duplicate profile", func(i *Issuer) { i.NativeClients = append(i.NativeClients, i.NativeClients[0]) }},
		{"partial browser", func(i *Issuer) { i.ClientID = "browser" }},
		{"native RFC9068 qualification", func(i *Issuer) { i.HumanSubjectNamespaceQualified = true }},
		{"unknown profile", func(i *Issuer) { i.NativeClients[0].Kind = "oidc" }},
		{"wildcard presenter", func(i *Issuer) { i.NativeClients[0].Presenters = []string{"*"} }},
		{"noncanonical presenters", func(i *Issuer) { i.NativeClients[0].Presenters = []string{"z", "a"} }},
		{"unbounded profiles", func(i *Issuer) { i.NativeClients = make([]NativeClient, MaxNativeClients+1) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := issuer.clone()
			tc.mutate(&i)
			if ValidateIssuerConfiguration(i) == nil {
				t.Fatal("invalid trust accepted")
			}
		})
	}
	detached := issuer.clone()
	detached.NativeClients[0].Presenters[0] = "replacement"
	client, ok := issuer.NativeClient("mobile")
	if !ok || client.Presenters[0] != "android-client" {
		t.Fatal("clone aliases trust")
	}
	client.Presenters[0] = "replacement"
	if issuer.NativeClients[0].Presenters[0] != "android-client" {
		t.Fatal("selector aliases trust")
	}
}
