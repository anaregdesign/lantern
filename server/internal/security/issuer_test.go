package security

import "testing"

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
