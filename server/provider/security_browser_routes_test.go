package provider

import (
	"net/http"
	"net/http/httptest"
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSecurityBrowserRoutesSessionLogout(t *testing.T) {
	runtime, _, session, csrf := browserSessionFixture(t)
	handler := runtime.AuthHTTPHandler()
	req := browserFixtureRequest(session, csrf)
	req.Method = http.MethodGet
	req.URL.Path = "/auth/session"
	req.Header.Del("Origin")
	req.Header.Del(browserCSRFHeader)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	var result pb.BrowserSession
	if w.Code != http.StatusOK || protojson.Unmarshal(w.Body.Bytes(), &result) != nil || result.Mode != pb.AuthMode_AUTH_MODE_OIDC || result.Principal.CsrfToken != csrf || result.Principal.Identity.Subject != "admin" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatal("session bootstrap", w.Code, w.Body.String())
	}
	request := browserFixtureRequest(session, csrf)
	request.URL.Path = "/auth/logout"
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, request)
	var revoked pb.SessionRevocation
	if w.Code != http.StatusOK || protojson.Unmarshal(w.Body.Bytes(), &revoked) != nil || revoked.Enforcement != pb.SecurityEnforcementState_SECURITY_ENFORCEMENT_STATE_ENFORCED || len(w.Result().Cookies()) != 2 {
		t.Fatal("explicit revocation", w.Code, w.Body.String())
	}
	w = httptest.NewRecorder()
	handler.ServeHTTP(w, req)
	if w.Code != http.StatusUnauthorized {
		t.Fatal("revoked cookie admitted", w.Code)
	}
}
func TestSecurityBrowserRoutesOffAndInvalidQuery(t *testing.T) {
	runtime, cleanup, err := NewSecurityRuntime(SecurityConfig{Mode: "off"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	w := httptest.NewRecorder()
	runtime.AuthHTTPHandler().ServeHTTP(w, httptest.NewRequest(http.MethodGet, "http://localhost/auth/session", nil))
	var session pb.BrowserSession
	if w.Code != http.StatusOK || protojson.Unmarshal(w.Body.Bytes(), &session) != nil || session.Mode != pb.AuthMode_AUTH_MODE_OFF || session.Principal != nil {
		t.Fatal("OFF requires a browser session")
	}
	for _, query := range []string{"state=a&state=b", "unexpected=value", "state=%invalid"} {
		req := httptest.NewRequest(http.MethodGet, "https://admin.example/auth/callback?"+query, nil)
		if _, err := browserQuery(req, "state"); err == nil {
			t.Fatal("ambiguous query accepted", query)
		}
	}
}
