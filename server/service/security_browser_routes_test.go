package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/security"
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

func TestBrowserWriterDiagnosticPreservesShortCircuit(t *testing.T) {
	for _, tc := range []struct {
		name string
		r    *AuthenticationAdapter
		want string
	}{
		{"missing adapter", nil, "adapter"},
		{"missing authority", &AuthenticationAdapter{}, "authority"},
		{"disabled origin", &AuthenticationAdapter{current: &security.CurrentAuthority{}, ready: func(context.Context) bool {
			t.Fatal("diagnostic evaluated readiness after refused origin")
			return true
		}}, "origin_disabled"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			capture := decisiontrace.Start(8)
			if capture == nil {
				t.Fatal("capture already owned")
			}
			defer capture.Stop()
			if tc.r.browserWriter(httptest.NewRequest(http.MethodGet, "https://admin.example/auth/login", nil)) {
				t.Fatal("diagnostic granted browser writer")
			}
			capture.Stop()
			events, dropped := capture.Drain()
			if dropped != 0 || len(events) != 1 || events[0].Stage != "writer" || events[0].Reason != tc.want {
				t.Fatal("wrong first boundary refusal", events, dropped)
			}
		})
	}
}
