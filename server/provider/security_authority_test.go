package provider

import (
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCurrentRuntimeNeverFallsBackFromMissingProvisioning(t *testing.T) {
	data := securityDataFixture(t)
	c := SecurityConfig{Mode: "oidc", BrowserOrigin: "https://admin.example", MaterialFile: filepath.Join(t.TempDir(), "absent.json")}
	if runtime, cleanup, err := NewSecurityRuntime(t.Context(), c, data); err == nil || runtime != nil || cleanup != nil {
		t.Fatal("missing independent inputs became public runtime")
	}
	r := &SecurityRuntime{current: &security.CurrentAuthority{}}
	if r.currentAttemptPrefix() != "" || r.currentAttemptAffinity([32]byte{1}) != "" {
		t.Fatal("missing process incarnation invented callback affinity")
	}
	r.attemptProcess = [32]byte{1}
	if r.currentAttemptAffinity([32]byte{}) != "" {
		t.Fatal("empty attempt accepted")
	}

}

func TestSettingsLoginReturnUsesExactRegisteredPath(t *testing.T) {
	now := time.Date(2026, 11, 2, 0, 0, 0, 0, time.UTC)
	manager, err := oidc.NewLoginTransactionsWithClock("https://admin.example", currentBrowserLoginReturns(), func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	issuer := security.Issuer{URL: "https://issuer.example", Enabled: true, ClientID: "admin", ConfigRevision: 1, Algorithms: []string{"EdDSA"}}
	issuer.RedirectURI = "https://admin.example" + oidc.CallbackPath(issuer.URL)
	trust := oidc.Trust{Issuer: issuer, Generation: [16]byte{1}, ConfigRevision: 1}
	discovery := oidc.Discovery{Issuer: issuer.URL, AuthorizationEndpoint: issuer.URL + "/auth", TokenEndpoint: issuer.URL + "/token", JWKSURI: issuer.URL + "/keys", ResponseTypes: []string{"code"}, CodeChallengeMethods: []string{"S256"}}
	start, err := manager.Begin(trust, discovery, "/settings", "", false)
	if err != nil {
		t.Fatal("settings login return was not registered", err)
	}
	redirect, err := url.Parse(start.AuthorizationURL)
	if err != nil {
		t.Fatal(err)
	}
	completion, err := manager.Consume(redirect.Query().Get("state"), start.TransactionCookie, oidc.CallbackPath(issuer.URL), issuer.URL)
	if err != nil || completion.ReturnPath() != "/settings" {
		t.Fatal("login did not retain the exact settings route", err)
	}
	for _, path := range []string{"https://other.example/settings", "//other.example/settings", "/settings?next=https://other.example", "/settings/"} {
		if _, err := manager.Begin(trust, discovery, path, "", false); err == nil {
			t.Fatalf("unregistered login return accepted: %s", path)
		}
	}
}

func TestCurrentRuntimeFloorExportRequiresActualOwner(t *testing.T) {
	for _, runtime := range []*SecurityRuntime{nil, {}, {current: &security.CurrentAuthority{}}} {
		if raw, err := runtime.ExportCurrentAuthorityFloors(); err == nil || len(raw) != 0 {
			t.Fatal("unopened owner manufactured resume floors")
		}
	}
}
