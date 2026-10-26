package service

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/anaregdesign/lantern/server/decisiontrace"
)

func TestSecurityManagementAuthorizationRequiresOriginOwner(t *testing.T) {
	r := NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter("oidc", BrowserConfig{BrowserOrigin: "https://admin.example"}, nil)})
	for _, path := range []string{"/auth/management-authorization/invalid", "/auth/management-authorization/AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA?purpose=login"} {
		w := httptest.NewRecorder()
		r.AuthHTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://admin.example"+path, nil))
		if w.Code == http.StatusFound || len(w.Result().Cookies()) != 0 {
			t.Fatal("unbound navigation started authentication", w.Code)
		}
	}
}

func TestManagementNavigationDiagnosticKeepsPublicUnavailable(t *testing.T) {
	capture := decisiontrace.Start(8)
	if capture == nil {
		t.Fatal("capture already owned")
	}
	defer capture.Stop()
	r := NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter("oidc", BrowserConfig{BrowserOrigin: "https://admin.example"}, nil)})
	w := httptest.NewRecorder()
	r.AuthHTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "https://admin.example/auth/management-authorization/private-ticket", nil))
	if w.Code != http.StatusServiceUnavailable || len(w.Result().Cookies()) != 0 {
		t.Fatal("diagnostic changed browser response", w.Code)
	}
	capture.Stop()
	events, dropped := capture.Drain()
	if dropped != 0 || len(events) != 1 || events[0].Stage != "writer" || events[0].Reason != "authority" {
		t.Fatal("missing navigation boundary refusal", events, dropped)
	}
}
