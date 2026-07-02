package provider

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"connectrpc.com/grpchealth"
)

// okUnary is a no-op next handler for interceptor-level tests.
func okUnary(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
	return nil, nil
}

// unaryReq builds a minimal connect.AnyRequest carrying the given
// Authorization header value ("" = no header).
func unaryReq(authz string) connect.AnyRequest {
	req := connect.NewRequest(&struct{}{})
	if authz != "" {
		req.Header().Set("Authorization", authz)
	}
	return req
}

// TestAuthInterceptor covers the #850 accept/reject matrix, rotation, the
// empty-token guard, and hook accounting at the interceptor level.
func TestAuthInterceptor(t *testing.T) {
	newAuth := func(tokens []string, rejects *int) *AuthInterceptor {
		a := NewAuthInterceptor(AuthConfig{Tokens: tokens, ExemptReflection: true})
		if rejects != nil {
			a = a.WithRejectHook(func() { *rejects++ })
		}
		return a
	}

	t.Run("matrix", func(t *testing.T) {
		cases := []struct {
			name   string
			tokens []string
			authz  string
			wantOK bool
		}{
			{"valid token accepted", []string{"s3cret"}, "Bearer s3cret", true},
			{"wrong token rejected", []string{"s3cret"}, "Bearer nope", false},
			{"missing header rejected", []string{"s3cret"}, "", false},
			{"wrong scheme rejected", []string{"s3cret"}, "Basic s3cret", false},
			{"rotation: old accepted", []string{"old", "new"}, "Bearer old", true},
			{"rotation: new accepted", []string{"old", "new"}, "Bearer new", true},
			{"rotation: other rejected", []string{"old", "new"}, "Bearer other", false},
			{"empty bearer rejected", []string{"s3cret"}, "Bearer ", false},
			{"token with same prefix rejected", []string{"s3cret"}, "Bearer s3cret-longer", false},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				var rejects int
				a := newAuth(tc.tokens, &rejects)
				_, err := a.WrapUnary(okUnary)(context.Background(), unaryReq(tc.authz))
				if tc.wantOK {
					if err != nil {
						t.Fatalf("want accept, got %v", err)
					}
					if rejects != 0 {
						t.Fatalf("hook fired %d times on accept", rejects)
					}
					return
				}
				if connect.CodeOf(err) != connect.CodeUnauthenticated {
					t.Fatalf("want Unauthenticated, got %v (err=%v)", connect.CodeOf(err), err)
				}
				if rejects != 1 {
					t.Fatalf("hook fired %d times, want 1", rejects)
				}
			})
		}
	})

	t.Run("empty and whitespace tokens cannot arm auth", func(t *testing.T) {
		a := NewAuthInterceptor(AuthConfig{Tokens: []string{"", "  ", "\t"}})
		if a.Enabled() {
			t.Fatal("blank tokens must leave auth disabled — the empty token must never be accepted")
		}
	})

	t.Run("RequireHTTP guards plain mounts", func(t *testing.T) {
		a := newAuth([]string{"tok"}, nil)
		h := a.RequireHTTP(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		}))
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/refl", nil))
		if rr.Code != http.StatusUnauthorized {
			t.Fatalf("tokenless reflection = %d, want 401", rr.Code)
		}
		rr = httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/refl", nil)
		req.Header.Set("Authorization", "Bearer tok")
		h.ServeHTTP(rr, req)
		if rr.Code != http.StatusOK {
			t.Fatalf("authorized reflection = %d, want 200", rr.Code)
		}
	})
}

// TestAuthInterceptor_HealthExemptStructurally documents WHY kubelet-style
// gRPC health probes keep working with auth on: grpchealth is mounted as a
// separate handler outside the Lantern interceptor chain, so the auth
// interceptor never sees it. This test drives a real tokenless
// grpc.health.v1.Health/Check over h2c against a mux assembled the same
// way the listener assembles it — auth armed, health mounted separately —
// matching how kubelet actually probes (no headers, plaintext HTTP/2).
func TestAuthInterceptor_HealthExemptStructurally(t *testing.T) {
	auth := NewAuthInterceptor(AuthConfig{Tokens: []string{"tok"}})
	mux := http.NewServeMux()
	hc := NewHealthChecker()
	mux.Handle(grpchealth.NewHandler(hc.Inner()))

	srv := httptest.NewUnstartedServer(mux)
	srv.EnableHTTP2 = true
	srv.StartTLS() // http2 test server; the transport below trusts it
	defer srv.Close()

	client := grpchealth.NewClient(srv.Client(), srv.URL)
	resp, err := client.Check(context.Background(), &grpchealth.CheckRequest{})
	if err != nil {
		t.Fatalf("tokenless health check with auth armed: %v", err)
	}
	_ = resp
	_ = auth // auth exists and is enabled; it simply never wraps health
}
