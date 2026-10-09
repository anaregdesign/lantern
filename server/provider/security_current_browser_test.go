package provider

import (
	"crypto/tls"
	"github.com/anaregdesign/lantern/server/internal/security"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestCurrentLogoutRejectsAmbiguityBeforeCookieClear(t *testing.T) {
	r := &SecurityRuntime{mode: "oidc", current: &security.CurrentAuthority{}, config: SecurityConfig{BrowserOrigin: "https://admin.example"}}
	for name, body := range map[string]string{"missing profile": "{}", "duplicate": "{\"localOnly\":true,\"localOnly\":false}", "legacy": "{\"changeId\":\"old\"}", "oversized": strings.Repeat(" ", 16<<10+1)} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "https://admin.example/auth/logout", strings.NewReader(body))
			request.TLS = &tls.ConnectionState{}
			request.Header.Set("Origin", "https://admin.example")
			request.Header.Set("Content-Type", "application/json")
			response := httptest.NewRecorder()
			r.currentBrowserLogout(response, request)
			if response.Code != http.StatusBadRequest || response.Header().Get("Set-Cookie") != "" {
				t.Fatal("unvalidated body affected session", response.Code, response.Header())
			}
		})
	}
	for _, path := range []string{"/auth/login", "/auth/callback/invalid", "/auth/management-authorization/invalid"} {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "https://admin.example"+path, nil)
		switch path {
		case "/auth/login":
			r.currentBrowserLogin(response, request)
		case "/auth/callback/invalid":
			r.currentBrowserCallback(response, request)
		default:
			r.currentBrowserManagementAuthorization(response, request)
		}
		if response.Code == http.StatusOK || response.Header().Get("Set-Cookie") != "" {
			t.Fatal("wrong-method browser mutation", path)
		}
	}
}
