package integration_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	cachegraph "github.com/anaregdesign/lantern/core/cache/graph"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

// gatewayHarness wires the real grpc-gateway provider against an
// in-process service and serves it on an OS-assigned port. It returns the
// base URL the test client should hit plus a cleanup function.
//
// The harness deliberately does NOT spin up a gRPC server — the gateway
// uses the in-process handler (RegisterLanternServiceHandlerServer) so
// REST round-trips go straight through to the service value.
type gatewayHarness struct {
	baseURL string
	cleanup func()
}

func startGatewayHarness(t *testing.T, ready bool, hsStatus healthpb.HealthCheckResponse_ServingStatus) *gatewayHarness {
	return startGatewayHarnessOpts(t, ready, hsStatus, provider.CORSConfig{})
}

// startGatewayHarnessOpts is the full-fat variant that lets a test inject
// a CORS allow-list. The two-helper split keeps the common path readable
// while still routing through one implementation.
func startGatewayHarnessOpts(t *testing.T, ready bool, hsStatus healthpb.HealthCheckResponse_ServingStatus, cors provider.CORSConfig) *gatewayHarness {
	t.Helper()

	// Pre-bind to find a free port — the provider's NewGatewayServer
	// only accepts an addr, not a listener, so we let the OS pick a port
	// then hand the resolved host:port back to the provider.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	svc := service.NewLanternService(cachegraph.NewGraphCache[string, *pb.Vertex](time.Minute))
	svc.MarkStarted(time.Now().Add(-3 * time.Second))

	hs := health.NewServer()
	hs.SetServingStatus("", hsStatus)
	hs.SetServingStatus(service.ServiceName, hsStatus)
	hs.SetServingStatus(service.ReplicationServiceName, hsStatus)

	// readiness.Gate is constructed in single-instance mode (hasPeers=false)
	// so it is permanently Ready by default. We flip it by toggling
	// MarkBootstrapped which is a no-op in single-instance mode; instead
	// we synthesise a multi-peer gate when the test wants NOT_READY.
	var gate *readiness.Gate
	if ready {
		gate = readiness.NewGate(0, false, hs)
	} else {
		gate = readiness.NewGate(0, true, hs)
		// Multi-peer gate stays NOT_READY until MarkBootstrapped is
		// called. We deliberately do not call it.
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	gw, err := provider.NewGatewayServer(
		provider.GatewayConfig{Addr: addr, ReadHeaderTimeout: time.Second},
		cors,
		svc, hs, gate, logger,
	)
	if err != nil {
		t.Fatalf("new gateway server: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := gw.Run(ctx); err != nil {
			t.Logf("gateway Run returned: %v", err)
		}
	}()

	base := "http://" + addr
	waitForGateway(t, base)

	return &gatewayHarness{
		baseURL: base,
		cleanup: func() {
			cancel()
			select {
			case <-done:
			case <-time.After(3 * time.Second):
				t.Log("gateway harness: shutdown timeout")
			}
		},
	}
}

// waitForGateway polls the harness until the /v1/health endpoint becomes
// reachable. Without this, the test races the goroutine that calls
// ListenAndServe.
func waitForGateway(t *testing.T, base string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(base + "/v1/health")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("gateway never came up on %s", base)
}

func TestGateway_HealthServing(t *testing.T) {
	h := startGatewayHarness(t, true, healthpb.HealthCheckResponse_SERVING)
	defer h.cleanup()

	resp, err := http.Get(h.baseURL + "/v1/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status: got %d want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Errorf("content-type: got %q want application/json...", ct)
	}
	var body struct {
		Status        string            `json:"status"`
		Checks        map[string]string `json:"checks"`
		UptimeSeconds int64             `json:"uptime_seconds"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "SERVING" {
		t.Errorf("status: got %q want SERVING", body.Status)
	}
	for _, k := range []string{"grpc", "graph_cache", "replication"} {
		if body.Checks[k] != "SERVING" {
			t.Errorf("checks[%s]: got %q want SERVING", k, body.Checks[k])
		}
	}
	if body.UptimeSeconds <= 0 {
		t.Errorf("uptime_seconds: got %d want >0", body.UptimeSeconds)
	}
}

func TestGateway_HealthNotServing_WhenGateNotReady(t *testing.T) {
	h := startGatewayHarness(t, false, healthpb.HealthCheckResponse_SERVING)
	defer h.cleanup()

	resp, err := http.Get(h.baseURL + "/v1/health")
	if err != nil {
		t.Fatalf("get health: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want 503", resp.StatusCode)
	}
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Status != "NOT_SERVING" {
		t.Errorf("status: got %q want NOT_SERVING", body.Status)
	}
}

func TestGateway_StatusRoute(t *testing.T) {
	// The gateway must forward /v1/status to GetServerStatus. This
	// exercises the runtime mux beyond the custom /v1/health handler.
	h := startGatewayHarness(t, true, healthpb.HealthCheckResponse_SERVING)
	defer h.cleanup()

	resp, err := http.Get(h.baseURL + "/v1/status")
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200, body=%s", resp.StatusCode, string(body))
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// GetServerStatus always populates GoVersion (runtime.Version() is
	// never empty) so it is a safe stable assertion key.
	if _, ok := got["goVersion"]; !ok {
		// JSONPb defaults to camelCase; allow snake_case fallback.
		if _, ok2 := got["go_version"]; !ok2 {
			t.Errorf("response missing goVersion/go_version field; got keys=%v", keys(got))
		}
	}
}

func TestGateway_ReplicationStatusRoute(t *testing.T) {
	h := startGatewayHarness(t, true, healthpb.HealthCheckResponse_SERVING)
	defer h.cleanup()

	resp, err := http.Get(h.baseURL + "/v1/replication/status")
	if err != nil {
		t.Fatalf("get replication status: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 200, body=%s", resp.StatusCode, string(body))
	}
	var got map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if _, ok := got["nodeId"]; !ok {
		if _, ok2 := got["node_id"]; !ok2 {
			t.Errorf("response missing nodeId/node_id field; got keys=%v", keys(got))
		}
	}
}

func keys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// Compile-time guard: the harness builds against the public provider
// surface only.
var _ = fmt.Sprintf

// TestGateway_CORSPreflight exercises the CORS middleware end-to-end on a
// live gateway listener. It does the smallest possible round-trip that
// proves wiring is correct: a preflight OPTIONS from an allowed origin
// must return 204 with the echoed Access-Control-Allow-Origin and the
// advertised methods/headers.
func TestGateway_CORSPreflight(t *testing.T) {
	h := startGatewayHarnessOpts(
		t, true, healthpb.HealthCheckResponse_SERVING,
		provider.CORSConfig{AllowedOrigins: []string{"http://localhost:5173"}},
	)
	defer h.cleanup()

	req, err := http.NewRequest(http.MethodOptions, h.baseURL+"/v1/status", nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	req.Header.Set("Origin", "http://localhost:5173")
	req.Header.Set("Access-Control-Request-Method", "GET")
	req.Header.Set("Access-Control-Request-Headers", "Content-Type")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("preflight: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status: got %d want 204, body=%s", resp.StatusCode, string(body))
	}
	if got := resp.Header.Get("Access-Control-Allow-Origin"); got != "http://localhost:5173" {
		t.Errorf("ACAO: got %q want echoed origin", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Methods"); !strings.Contains(got, "GET") {
		t.Errorf("ACAM: got %q want to contain GET", got)
	}
	if got := resp.Header.Get("Access-Control-Allow-Credentials"); got != "" {
		t.Errorf("ACAC must not be set, got %q", got)
	}
}
