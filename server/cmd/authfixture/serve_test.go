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
	"sync"
	"sync/atomic"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
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
	for i := range f.Nodes {
		f.Nodes[i].Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR"] = "/unused-graph-only-spool"
		f.Nodes[i].Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES"] = "134217728"
		f.Nodes[i].Environment["LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"] = "33554432"
	}
	if err := addFixtureReceipts(&f, t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, node := range f.Nodes {
		for _, key := range []string{"LANTERN_GRAPH_SNAPSHOT_SPOOL_DIR", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_DISK_BYTES", "LANTERN_GRAPH_SNAPSHOT_SPOOL_MAX_FRAME_BYTES"} {
			if _, present := node.Environment[key]; present {
				t.Fatal("receipt fixture retained graph-only storage configuration")
			}
		}
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
	// These are untrusted probe response/expected-input fixtures, not authority.
	profile := &pb.CurrentAuthorityProfile{Version: 2, Domain: bytes.Repeat([]byte{1}, 32), Cohort: bytes.Repeat([]byte{2}, 32), Generation: bytes.Repeat([]byte{3}, 16), Protocol: bytes.Repeat([]byte{4}, 32), TimeProfile: bytes.Repeat([]byte{5}, 32), Membership: bytes.Repeat([]byte{6}, 32), Configuration: bytes.Repeat([]byte{7}, 32)}
	other := proto.Clone(profile).(*pb.CurrentAuthorityProfile)
	other.TimeProfile[0] ^= 1
	for _, tc := range []struct {
		name, health, mode         string
		ready                      bool
		version, member, status    int
		expected, advertised       *pb.CurrentAuthorityProfile
		accept                     bool
		originalMember             uint32
		wantHealth, wantCapability string
	}{
		{"oidc original profile", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, profile, profile, true, 0, "serving", "ready"},
		{"off", "SERVING_STATUS_SERVING", "AUTH_MODE_OFF", true, 1, 0, 200, nil, nil, true, 0, "serving", "ready"},
		{"wrong health spelling", "SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, profile, profile, false, 0, "unrecognized", "not_probed"},
		{"not serving", "SERVING_STATUS_NOT_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, profile, profile, false, 0, "not_serving", "not_probed"},
		{"not certified", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", false, 2, 1, 200, profile, profile, false, 0, "serving", "not_ready"},
		{"unknown mode", "SERVING_STATUS_SERVING", "AUTH_MODE_UNSPECIFIED", true, 2, 1, 200, profile, profile, false, 0, "serving", "uncertified"},
		{"retired authenticated protocol", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 1, 0, 200, profile, nil, false, 0, "serving", "uncertified"},
		{"missing member", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 0, 200, profile, profile, false, 0, "serving", "uncertified"},
		{"missing advertised profile", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, profile, nil, false, 0, "serving", "uncertified"},
		{"foreign advertised profile", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, profile, other, false, 0, "serving", "uncertified"},
		{"missing original expected profile", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, nil, profile, false, 0, "serving", "uncertified"},
		{"oidc cannot downgrade", "SERVING_STATUS_SERVING", "AUTH_MODE_OFF", true, 1, 0, 200, profile, nil, false, 0, "serving", "uncertified"},
		{"transport error", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 503, profile, profile, false, 0, "http_failure", "not_probed"},
		{"original fourth member subset", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 4, 200, profile, profile, true, 4, "serving", "ready"},
		{"subset cannot rebind member", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 1, 200, profile, profile, false, 4, "serving", "uncertified"},
		{"invalid original member", "SERVING_STATUS_SERVING", "AUTH_MODE_OIDC", true, 2, 32, 200, profile, profile, false, 32, "serving", "uncertified"},
		{"subset cannot lose its original profile", "SERVING_STATUS_SERVING", "AUTH_MODE_OFF", true, 1, 0, 200, nil, nil, false, 4, "serving", "uncertified"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var healthCalls, capabilityCalls atomic.Uint32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				w.WriteHeader(tc.status)
				if r.URL.Path == "/grpc.health.v1.Health/Check" {
					healthCalls.Add(1)
					_ = json.NewEncoder(w).Encode(map[string]any{"status": tc.health})
				} else {
					capabilityCalls.Add(1)
					body := map[string]any{"mode": tc.mode, "ready": tc.ready, "protocolVersion": tc.version, "currentMember": tc.member}
					if tc.advertised != nil {
						raw, err := protojson.Marshal(tc.advertised)
						if err != nil {
							t.Error(err)
							return
						}
						body["currentProfile"] = json.RawMessage(raw)
					}
					_ = json.NewEncoder(w).Encode(body)
				}
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			f := fixture{CAFile: ca, authorityProfile: tc.expected, Nodes: []fixtureNode{{PublicOrigin: server.URL, originalMember: tc.originalMember}}}
			poll, err := newFixtureReadinessPoll(f, []*fixtureProcess{{done: make(chan struct{})}})
			if err != nil {
				t.Fatal(err)
			}
			defer poll.close()
			ready, err := poll.once(t.Context())
			if err != nil || ready != tc.accept {
				t.Fatalf("accept=%t ready=%t error=%v", tc.accept, ready, err)
			}
			wantCapabilities := uint32(1)
			if tc.wantCapability == "not_probed" {
				wantCapabilities = 0
			}
			if poll.health[0] != tc.wantHealth || poll.capabilities[0] != tc.wantCapability || poll.statuses[0] != tc.status || poll.healthCount[0] != 1 || poll.capabilityCount[0] != wantCapabilities || healthCalls.Load() != 1 || capabilityCalls.Load() != wantCapabilities {
				t.Fatalf("completed poll lost contract or exact probe counts: health=%s capabilities=%s status=%d health_count=%d capabilities_count=%d HTTP_calls=%d/%d", poll.health[0], poll.capabilities[0], poll.statuses[0], poll.healthCount[0], poll.capabilityCount[0], healthCalls.Load(), capabilityCalls.Load())
			}
		})
	}
}

func TestFixtureReadinessDiagnosticChild(t *testing.T) {
	if os.Getenv("LANTERN_TEST_FIXTURE_DIAGNOSTIC_CHILD") != "exit" {
		return
	}
	// Arbitrary private text must never be replayed into the public diagnostic.
	_, _ = os.Stderr.WriteString("{\"msg\":\"server exited with error\",\"err\":\"initialize app: listen tcp :12345: bind: address already in use private-token-key-body\"}\n")
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
	var healthCalls, capabilityCalls atomic.Uint32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/grpc.health.v1.Health/Check" {
			healthCalls.Add(1)
		} else {
			capabilityCalls.Add(1)
		}
		_, _ = w.Write([]byte(`{"status":"private-token-key-body"}`))
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	poll, err := newFixtureReadinessPoll(fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{})}})
	if err != nil {
		t.Fatal(err)
	}
	defer poll.close()
	if ready, err := poll.once(t.Context()); err != nil || ready {
		t.Fatal("unexpected completed readiness result", ready, err)
	}
	// Serialize the completed observation through the ordinary failure path;
	// this does not run a second probe or assert that a deadline elapsed.
	err = poll.failure("timeout", context.DeadlineExceeded)
	var output bytes.Buffer
	writeFixtureFailure(&output, &fixtureFailure{stage: "readiness", cause: err})
	if !strings.Contains(output.String(), `"probe":"health"`) || strings.Contains(output.String(), "private-token-key-body") {
		t.Fatal("response escaped fixed diagnostic categories")
	}
	if !strings.Contains(output.String(), `"last_health":"unrecognized"`) || !strings.Contains(output.String(), `"last_capability":"not_probed"`) {
		t.Fatal("unrecognized response or unobserved capabilities misrepresented")
	}
	if poll.healthCount[0] != 1 || poll.capabilityCount[0] != 0 || healthCalls.Load() != 1 || capabilityCalls.Load() != 0 {
		t.Fatal("failure serialization made another readiness evaluation")
	}
}

func TestFixtureReadinessProbeCategories(t *testing.T) {
	for _, tc := range []struct {
		name, health, capability, wantHealth, wantCapability, wantProbe string
		healthStatus, capabilityStatus                                  int
	}{
		{"missing health", `{}`, `{}`, "missing", "not_probed", "health", 200, 200},
		{"unknown health", `{"status":"SERVING_STATUS_UNKNOWN"}`, `{}`, "unknown", "not_probed", "health", 200, 200},
		{"decode health", `{"status":3}`, `{}`, "decode_failure", "not_probed", "json", 200, 200},
		{"http health", `{}`, `{}`, "http_failure", "not_probed", "http", 503, 200},
		{"missing capability", `{"status":"SERVING_STATUS_SERVING"}`, `{}`, "serving", "missing", "capabilities", 200, 200},
		{"decode capability", `{"status":"SERVING_STATUS_SERVING"}`, `{"ready":"bad"}`, "serving", "decode_failure", "json", 200, 200},
		{"http capability", `{"status":"SERVING_STATUS_SERVING"}`, `{}`, "serving", "http_failure", "http", 200, 503},
		{"not ready", `{"status":"SERVING_STATUS_SERVING"}`, `{"ready":false}`, "serving", "not_ready", "capabilities", 200, 200},
		{"uncertified", `{"status":"SERVING_STATUS_SERVING"}`, `{"ready":true,"mode":"AUTH_MODE_OIDC","protocolVersion":2}`, "serving", "uncertified", "capabilities", 200, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var healthCalls, capabilityCalls atomic.Uint32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/grpc.health.v1.Health/Check" {
					healthCalls.Add(1)
					w.WriteHeader(tc.healthStatus)
					_, _ = w.Write([]byte(tc.health))
				} else {
					capabilityCalls.Add(1)
					w.WriteHeader(tc.capabilityStatus)
					_, _ = w.Write([]byte(tc.capability))
				}
			}))
			defer server.Close()
			ca := filepath.Join(t.TempDir(), "ca.pem")
			if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
				t.Fatal(err)
			}
			poll, err := newFixtureReadinessPoll(fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{})}})
			if err != nil {
				t.Fatal(err)
			}
			defer poll.close()
			if ready, err := poll.once(t.Context()); err != nil || ready {
				t.Fatal("unexpected completed readiness result", ready, err)
			}
			err = poll.failure("timeout", context.DeadlineExceeded)
			var failure *fixtureReadinessError
			if !errors.As(err, &failure) {
				t.Fatal("missing typed diagnostic", err)
			}
			d := failure.diagnostic
			wantCapabilities, wantStatus := uint32(1), tc.capabilityStatus
			if tc.wantCapability == "not_probed" {
				wantCapabilities, wantStatus = 0, tc.healthStatus
			}
			if d.LastHealth != tc.wantHealth || d.LastCapability != tc.wantCapability || d.HealthProbes != 1 || d.CapabilityProbes != wantCapabilities || d.HTTPStatus != wantStatus || d.Probe != tc.wantProbe || d.ChildExited || d.Port == 0 || healthCalls.Load() != 1 || capabilityCalls.Load() != wantCapabilities {
				t.Fatal(d)
			}
		})
	}
}

func TestFixtureReadinessOuterDeadline(t *testing.T) {
	release := make(chan struct{})
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	poll, err := newFixtureReadinessPoll(fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{})}})
	if err != nil {
		t.Fatal(err)
	}
	defer poll.close()
	// Establish diagnostic elapsed time before starting the external deadline;
	// scheduling cannot make measurement begin after the timeout has expired.
	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	err = poll.wait(ctx)
	var failure *fixtureReadinessError
	if !errors.As(err, &failure) || failure.diagnostic.Reason != "timeout" || failure.diagnostic.ElapsedMillis < 90 || failure.diagnostic.ChildExited {
		t.Fatal("outer readiness deadline did not retain its bounded timeout", err)
	}
	// The deadline can precede verified TLS or the response. It establishes no
	// HTTP-response classification, unlike the completed-poll cases above.
}

func TestFixtureReadinessPollCancellationStopsHealthRequest(t *testing.T) {
	var healthCalls, capabilityCalls atomic.Uint32
	entered, release := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	server := httptest.NewTLSServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/grpc.health.v1.Health/Check" {
			healthCalls.Add(1)
			enteredOnce.Do(func() { close(entered) })
		} else {
			capabilityCalls.Add(1)
		}
		select {
		case <-r.Context().Done():
		case <-release:
		}
	}))
	defer server.Close()
	defer close(release)
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	poll, err := newFixtureReadinessPoll(fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{})}})
	if err != nil {
		t.Fatal(err)
	}
	defer poll.close()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type result struct {
		ready bool
		err   error
	}
	done := make(chan result, 1)
	go func() {
		ready, err := poll.once(ctx)
		done <- result{ready, err}
	}()
	select {
	case <-entered:
		cancel()
	case result := <-done:
		t.Fatal("poll ended before the Health request entered", result)
	case <-t.Context().Done():
		t.Fatal("Health request did not enter", t.Context().Err())
	}
	select {
	case result := <-done:
		if result.err != nil || result.ready {
			t.Fatal("canceled Health request acquired readiness", result)
		}
	case <-t.Context().Done():
		t.Fatal("canceled Health poll did not join", t.Context().Err())
	}
	var failure *fixtureReadinessError
	err = poll.failure("timeout", ctx.Err())
	if !errors.As(err, &failure) || !errors.Is(err, context.Canceled) || failure.diagnostic.HealthProbes != 1 || failure.diagnostic.CapabilityProbes != 0 || failure.diagnostic.LastHealth != "transport_failure" || failure.diagnostic.LastCapability != "not_probed" || healthCalls.Load() != 1 || capabilityCalls.Load() != 0 {
		t.Fatal("cancellation lost its causal Health-only observation", err)
	}
}

func TestFixtureReadinessPollRetainsPriorCapabilityAfterHealthFailure(t *testing.T) {
	var healthCalls, capabilityCalls atomic.Uint32
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/grpc.health.v1.Health/Check" {
			if healthCalls.Add(1) > 1 {
				w.WriteHeader(http.StatusServiceUnavailable)
			}
			_, _ = w.Write([]byte(`{"status":"SERVING_STATUS_SERVING"}`))
			return
		}
		capabilityCalls.Add(1)
		_, _ = w.Write([]byte(`{"ready":false,"mode":"AUTH_MODE_OIDC","protocolVersion":2}`))
	}))
	defer server.Close()
	ca := filepath.Join(t.TempDir(), "ca.pem")
	if err := os.WriteFile(ca, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: server.Certificate().Raw}), 0600); err != nil {
		t.Fatal(err)
	}
	poll, err := newFixtureReadinessPoll(fixture{CAFile: ca, Nodes: []fixtureNode{{PublicOrigin: server.URL}}}, []*fixtureProcess{{done: make(chan struct{})}})
	if err != nil {
		t.Fatal(err)
	}
	defer poll.close()
	if ready, err := poll.once(t.Context()); err != nil || ready {
		t.Fatal("first completed poll did not retain the capability refusal", ready, err)
	}
	if poll.health[0] != "serving" || poll.capabilities[0] != "not_ready" || poll.statuses[0] != 200 || poll.healthCount[0] != 1 || poll.capabilityCount[0] != 1 || healthCalls.Load() != 1 || capabilityCalls.Load() != 1 {
		t.Fatal("first poll lost its complete Health/capability observations")
	}
	if ready, err := poll.once(t.Context()); err != nil || ready {
		t.Fatal("later failed Health acquired readiness", ready, err)
	}
	var failure *fixtureReadinessError
	err = poll.failure("timeout", context.DeadlineExceeded)
	if !errors.As(err, &failure) {
		t.Fatal("missing retained two-poll diagnostic", err)
	}
	d := failure.diagnostic
	if d.Probe != "http" || d.HTTPStatus != 503 || d.LastHealth != "http_failure" || d.LastCapability != "not_ready" || d.HealthProbes != 2 || d.CapabilityProbes != 1 || healthCalls.Load() != 2 || capabilityCalls.Load() != 1 {
		t.Fatal("later Health failure replaced or re-evaluated the prior capability", d)
	}
}
