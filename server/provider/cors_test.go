package provider

import (
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"testing"
)

func TestLoadCORSConfig(t *testing.T) {
	cases := []struct {
		name string
		env  string
		want []string
	}{
		{name: "empty", env: "", want: nil},
		{name: "single", env: "http://localhost:5173", want: []string{"http://localhost:5173"}},
		{
			name: "multiple_with_whitespace",
			env:  " http://localhost:5173 , https://admin.example.com ",
			want: []string{"http://localhost:5173", "https://admin.example.com"},
		},
		{name: "wildcard_alone", env: "*", want: []string{"*"}},
		{
			name: "wildcard_with_others_is_dropped",
			env:  "*, http://localhost:5173",
			want: []string{"http://localhost:5173"},
		},
		{name: "trailing_comma_ignored", env: "http://a,,", want: []string{"http://a"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("LANTERN_CORS_ALLOWED_ORIGINS", tc.env)
			if tc.env == "" {
				// t.Setenv with "" still sets the var; we want the
				// unset path too.
				if err := os.Unsetenv("LANTERN_CORS_ALLOWED_ORIGINS"); err != nil {
					t.Fatalf("unsetenv: %v", err)
				}
			}
			got := loadCORSConfig().AllowedOrigins
			if !reflect.DeepEqual(got, tc.want) {
				t.Fatalf("got %#v want %#v", got, tc.want)
			}
		})
	}
}

func TestCORSMiddleware_DisabledIsIdentity(t *testing.T) {
	// When the allow-list is empty the middleware must not touch
	// requests — same byte-for-byte response as the bare handler.
	hit := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusTeapot)
	})
	h := CORSMiddleware(CORSConfig{})(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !hit {
		t.Fatal("downstream handler was not invoked")
	}
	if rec.Code != http.StatusTeapot {
		t.Fatalf("status: got %d want %d", rec.Code, http.StatusTeapot)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO must be absent when disabled, got %q", got)
	}
}

func TestCORSMiddleware_PreflightAllowed(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream must not be invoked on preflight")
	})
	h := CORSMiddleware(CORSConfig{AllowedOrigins: []string{"http://localhost:5173"}})(next)

	req := httptest.NewRequest(http.MethodOptions, "/v1/vertices/x", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("ACAO: got %q want echoed origin", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != corsAllowedMethods {
		t.Fatalf("ACAM: got %q want %q", got, corsAllowedMethods)
	}
	if got := rec.Header().Get("Access-Control-Allow-Headers"); got != corsAllowedHeaders {
		t.Fatalf("ACAH: got %q want %q", got, corsAllowedHeaders)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary: got %q want Origin", got)
	}
	// Per issue #317, Allow-Credentials must never be true (no auth, no
	// cookies in v1).
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("ACAC must not be set, got %q", got)
	}
}

func TestCORSMiddleware_PreflightDeniedNoHeaders(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("downstream must not be invoked on preflight")
	})
	h := CORSMiddleware(CORSConfig{AllowedOrigins: []string{"http://localhost:5173"}})(next)

	req := httptest.NewRequest(http.MethodOptions, "/v1/vertices/x", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	req.Header.Set("Access-Control-Request-Method", "PUT")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status: got %d want 204", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO must be empty for disallowed origin, got %q", got)
	}
	if got := rec.Header().Get("Access-Control-Allow-Methods"); got != "" {
		t.Fatalf("ACAM must be empty for disallowed origin, got %q", got)
	}
}

func TestCORSMiddleware_ActualRequestAllowed(t *testing.T) {
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	h := CORSMiddleware(CORSConfig{AllowedOrigins: []string{"http://localhost:5173"}})(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("downstream handler was not invoked")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status: got %d want 200", rec.Code)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Fatalf("ACAO: got %q want echoed origin", got)
	}
	if got := rec.Header().Get("Vary"); got != "Origin" {
		t.Fatalf("Vary: got %q want Origin", got)
	}
}

func TestCORSMiddleware_ActualRequestDeniedPassesThrough(t *testing.T) {
	// Browsers — not the server — enforce SOP on non-preflight CORS
	// failures. The middleware must therefore forward the request and
	// just omit the Access-Control-Allow-Origin header; the caller can
	// confirm in their own browser console that the response is denied.
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(CORSConfig{AllowedOrigins: []string{"http://localhost:5173"}})(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Origin", "http://evil.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("downstream handler must still run")
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "" {
		t.Fatalf("ACAO must be absent, got %q", got)
	}
}

func TestCORSMiddleware_WildcardEchoesStar(t *testing.T) {
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(CORSConfig{AllowedOrigins: []string{"*"}})(next)

	req := httptest.NewRequest(http.MethodGet, "/v1/status", nil)
	req.Header.Set("Origin", "http://anywhere.example.com")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "*" {
		t.Fatalf("ACAO: got %q want %q", got, "*")
	}
	// Per the fetch spec, Allow-Credentials with `*` is invalid; make
	// sure we don't even tempt it.
	if got := rec.Header().Get("Access-Control-Allow-Credentials"); got != "" {
		t.Fatalf("ACAC must not be set under wildcard, got %q", got)
	}
}

func TestCORSMiddleware_BareOptionsIsNotPreflight(t *testing.T) {
	// OPTIONS without Access-Control-Request-Method must pass through
	// so the gateway can answer real OPTIONS RPCs if any are added
	// later.
	called := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	})
	h := CORSMiddleware(CORSConfig{AllowedOrigins: []string{"http://localhost:5173"}})(next)

	req := httptest.NewRequest(http.MethodOptions, "/v1/status", nil)
	req.Header.Set("Origin", "http://localhost:5173")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)

	if !called {
		t.Fatal("bare OPTIONS must fall through to downstream")
	}
}
