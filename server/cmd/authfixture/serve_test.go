package main

import (
	"context"
	"encoding/json"
	"encoding/pem"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestFixtureEnvironmentPreservesOwnedTrust(t *testing.T) {
	t.Setenv("LANTERN_AUTH_TOKENS", "retired-fixture")
	node := fixtureNode{Environment: map[string]string{"LANTERN_PORT": "16380", "LANTERN_AUTH_MODE": "oidc", "LANTERN_METRICS_ADDR": ""}}
	env, err := fixtureEnvironment(node, map[string]string{"LANTERN_METRICS_ADDR": "127.0.0.1:16490", "LANTERN_SEARCH_ENABLED": "false"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(env, "\n")
	if strings.Contains(joined, "LANTERN_AUTH_TOKENS=") || !strings.Contains(joined, "LANTERN_AUTH_MODE=oidc") || !strings.Contains(joined, "LANTERN_SEARCH_ENABLED=false") {
		t.Fatal("inherited credentials or lost fixture configuration")
	}
	for _, override := range []map[string]string{{"LANTERN_AUTH_MODE": "off"}, {"LANTERN_PORT": "16381"}, {"PATH": "forged"}, {"LANTERN_BAD": "\x00"}} {
		if _, err := fixtureEnvironment(node, override); err == nil {
			t.Fatal("unsafe fixture override admitted")
		}
	}
}
func TestFixtureOverrideFileIsExactAndBounded(t *testing.T) {
	file := filepath.Join(t.TempDir(), "overrides.json")
	for _, body := range []string{`[{"LANTERN_SEARCH_ENABLED":"false"}]`, `[]`, `[{}] {}`, `[{"LANTERN_SEARCH_ENABLED":true}]`} {
		if err := os.WriteFile(file, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		result, err := loadFixtureOverrides(file, 1)
		if body == `[{"LANTERN_SEARCH_ENABLED":"false"}]` {
			if err != nil || result[0]["LANTERN_SEARCH_ENABLED"] != "false" {
				t.Fatal(result, err)
			}
		} else if err == nil {
			t.Fatal("invalid override framing admitted")
		}
	}
}
func TestFixtureReceiptCohortRetainsOneEpoch(t *testing.T) {
	f := fixture{Nodes: []fixtureNode{{Name: "one", Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}, {Name: "two", Environment: map[string]string{"LANTERN_AUTH_MODE": "oidc"}}}}
	if err := addFixtureReceipts(&f, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if f.Nodes[0].Environment["LANTERN_RECEIPT_EPOCH"] != f.Nodes[1].Environment["LANTERN_RECEIPT_EPOCH"] || f.Nodes[0].Environment["LANTERN_RECEIPT_WAL_PATH"] == f.Nodes[1].Environment["LANTERN_RECEIPT_WAL_PATH"] {
		t.Fatal("receipt domain or ownership drift")
	}
	if err := addFixtureReceipts(&fixture{Nodes: []fixtureNode{{Environment: map[string]string{}}}}, t.TempDir()); err == nil {
		t.Fatal("OFF receipt fixture admitted")
	}
	raw, err := json.Marshal(f)
	if err != nil || strings.Contains(string(raw), "lnt_m1_") {
		t.Fatal("stdout credential disclosure")
	}
	if err := serveFixture(context.Background(), f, "relative-binary", t.TempDir(), "", strings.NewReader("shutdown\n"), os.Stdout, time.Second); err == nil {
		t.Fatal("unbound Server binary admitted")
	}
}

func TestFixtureReadinessRequiresHealthAndCertifiedCapabilities(t *testing.T) {
	for _, tc := range []struct {
		name, health, mode string
		ready              bool
		version, status    int
		accept             bool
	}{
		{"oidc", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 1, 200, true},
		{"off", "SERVING_STATUS_SERVING", "AUTH_MODE_OFF", true, 1, 200, true},
		{"wrong health spelling", "SERVING", "AUTH_MODE_OIDC", true, 1, 200, false},
		{"not serving", "SERVING_STATUS_NOT_SERVING", "AUTH_MODE_OIDC", true, 1, 200, false},
		{"not certified", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", false, 1, 200, false},
		{"unknown mode", "SERVING_STATUS_SERVING", "AUTH_MODE_UNSPECIFIED", true, 1, 200, false},
		{"protocol drift", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 200, false},
		{"transport error", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 1, 503, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if r.URL.Path == "/grpc.health.v1.Health/Check" {
					_ = json.NewEncoder(w).Encode(map[string]any{"status": tc.health})
				} else {
					_ = json.NewEncoder(w).Encode(map[string]any{"mode": tc.mode, "ready": tc.ready, "protocolVersion": tc.version})
				}
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			f := fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}
			err := waitFixtureReady(ctx, f, []*fixtureProcess{{done: make(chan struct{})}})
			if (err == nil) != tc.accept {
				t.Fatalf("accept=%t error=%v", tc.accept, err)
			}
		})
	}
}
