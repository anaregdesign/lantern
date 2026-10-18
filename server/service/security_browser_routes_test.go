package service

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestSecurityBrowserRoutesOffAndInvalidQuery(t *testing.T) {
	runtime := NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter("off", BrowserConfig{}, nil), Now: time.Now})
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
