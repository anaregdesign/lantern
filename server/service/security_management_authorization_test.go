package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
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
