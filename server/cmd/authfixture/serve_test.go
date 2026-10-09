package main

import (
	"bytes"
	"context"
	"encoding/json"
	"encoding/pem"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
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
	for _, override := range []map[string]string{{"LANTERN_AUTH_MODE": "off"}, {"LANTERN_PORT": "16381"}, {"PATH": "forged"}, {"LANTERN_BAD": "\x00"}, {"LANTERN_TLS_CLIENT_CA_FILE": "foreign"}, {"LANTERN_PEER_LISTEN_ADDR": "127.0.0.1:1"}} {
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
			if !tc.accept {
				var failure *fixtureReadinessError
				if !errors.As(err, &failure) || failure.diagnostic.Reason != "timeout" || failure.diagnostic.Port == 0 || failure.diagnostic.ElapsedMillis < 90 || failure.diagnostic.HTTPStatus != tc.status || failure.diagnostic.ChildExited {
					t.Fatal("missing bounded readiness timeout diagnostic")
				}
			}
		})
	}
}

func TestFixtureReadinessDiagnosticChild(t *testing.T) {
	if os.Getenv("LANTERN_TEST_FIXTURE_DIAGNOSTIC_CHILD") != "exit" {
		return
	}
	// Arbitrary private text must never be replayed into the public diagnostic.
	_, _ = os.Stderr.WriteString("{\"msg\":\"failed to initialize app\",\"err\":\"listen tcp :12345: bind: address already in use private-token-key-body\"}\n")
	os.Exit(23)
}

func TestFixtureReadinessRetainsExitedChildBeforeCleanup(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer server.Close()
	dir := t.TempDir()
	ca := filepath.Join(dir, "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	log, err := os.OpenFile(filepath.Join(dir, "private-child.log"), os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestFixtureReadinessDiagnosticChild$")
	command.Env = append(os.Environ(), "LANTERN_TEST_FIXTURE_DIAGNOSTIC_CHILD=exit")
	command.Stdout, command.Stderr = log, log
	if err := command.Run(); err == nil || command.ProcessState.ExitCode() != 23 {
		t.Fatal("controlled child did not exit as requested", err)
	}
	child := &fixtureProcess{command: command, log: log, done: make(chan struct{})}
	close(child.done)
	err = waitFixtureReady(context.Background(), fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: "https://localhost:12345"}}}, []*fixtureProcess{child})
	// Match supervision ordering: capture failure, reap/close child, remove
	// temporary private material, then inspect the retained public evidence.
	child.stop()
	if removeErr := os.RemoveAll(dir); removeErr != nil {
		t.Fatal(removeErr)
	}
	var output bytes.Buffer
	writeFixtureFailure(&output, &fixtureFailure{stage: "readiness", cause: err})
	var failure *fixtureReadinessError
	if !errors.As(err, &failure) {
		t.Fatal("child exit lost readiness detail", err)
	}
	d := failure.diagnostic
	if d.Reason != "child_exit" || !d.ChildExited || d.ChildExitCode != 23 || d.Port != 12345 || d.Node != 0 || d.ServerLog != "bind_address_in_use" || d.ElapsedMillis < 0 {
		t.Fatal("wrong retained child diagnostic", d)
	}
	if !strings.Contains(output.String(), "authfixture_readiness:") || strings.Contains(output.String(), "private-token-key-body") || strings.Contains(output.String(), dir) {
		t.Fatal("missing safe record or private log disclosure")
	}
}

func TestFixtureReadinessDiagnosticDoesNotExposeResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"status":"private-token-key-body"}`))
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	err := waitFixtureReady(ctx, fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{})}})
	var output bytes.Buffer
	writeFixtureFailure(&output, &fixtureFailure{stage: "readiness", cause: err})
	if !strings.Contains(output.String(), `"probe":"health"`) || strings.Contains(output.String(), "private-token-key-body") {
		t.Fatal("response escaped fixed diagnostic categories")
	}
}
