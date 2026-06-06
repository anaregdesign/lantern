package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	healthpb "google.golang.org/grpc/health/grpc_health_v1"
)

type fakeUptime struct {
	d time.Duration
}

func (f *fakeUptime) Uptime() time.Duration { return f.d }

type fakeHealth struct {
	statuses map[string]healthpb.HealthCheckResponse_ServingStatus
	err      error
}

func (f *fakeHealth) Check(_ context.Context, req *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if f.err != nil {
		return nil, f.err
	}
	st, ok := f.statuses[req.GetService()]
	if !ok {
		return &healthpb.HealthCheckResponse{Status: healthpb.HealthCheckResponse_SERVICE_UNKNOWN}, nil
	}
	return &healthpb.HealthCheckResponse{Status: st}, nil
}

type fakeReady struct{ v bool }

func (f *fakeReady) Ready() bool { return f.v }

// healthBody is the JSON envelope returned by /v1/health. Mirrors the
// unexported healthResponse type used by the handler so tests can decode
// without re-declaring the struct.
type healthBody struct {
	Status        string            `json:"status"`
	Checks        map[string]string `json:"checks"`
	UptimeSeconds int64             `json:"uptime_seconds"`
}

func decode(t *testing.T, rr *httptest.ResponseRecorder) healthBody {
	t.Helper()
	var got healthBody
	if err := json.NewDecoder(rr.Body).Decode(&got); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return got
}

func TestHealthHandler(t *testing.T) {
	t.Run("ServingWhenAllChecksServing", func(t *testing.T) {
		h := newHealthHandler(
			&fakeUptime{d: 42 * time.Second},
			&fakeHealth{statuses: map[string]healthpb.HealthCheckResponse_ServingStatus{
				"":                                   healthpb.HealthCheckResponse_SERVING,
				"graph.v1.LanternService":            healthpb.HealthCheckResponse_SERVING,
				"graph.v1.LanternReplicationService": healthpb.HealthCheckResponse_SERVING,
			}},
			&fakeReady{v: true},
		)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

		if rr.Code != http.StatusOK {
			t.Fatalf("status: got %d want 200", rr.Code)
		}
		got := decode(t, rr)
		if got.Status != "SERVING" {
			t.Errorf("overall status: got %q want SERVING", got.Status)
		}
		if got.UptimeSeconds != 42 {
			t.Errorf("uptime: got %d want 42", got.UptimeSeconds)
		}
		for _, k := range []string{"grpc", "graph_cache", "replication"} {
			if got.Checks[k] != "SERVING" {
				t.Errorf("checks[%s]: got %q want SERVING", k, got.Checks[k])
			}
		}
	})

	t.Run("NotServingWhenGrpcOverallDown", func(t *testing.T) {
		h := newHealthHandler(
			&fakeUptime{d: time.Second},
			&fakeHealth{statuses: map[string]healthpb.HealthCheckResponse_ServingStatus{
				"":                        healthpb.HealthCheckResponse_NOT_SERVING,
				"graph.v1.LanternService": healthpb.HealthCheckResponse_SERVING,
			}},
			&fakeReady{v: true},
		)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status: got %d want 503", rr.Code)
		}
		got := decode(t, rr)
		if got.Status != "NOT_SERVING" {
			t.Errorf("overall status: got %q want NOT_SERVING", got.Status)
		}
		if got.Checks["grpc"] != "NOT_SERVING" {
			t.Errorf("checks[grpc]: got %q want NOT_SERVING", got.Checks["grpc"])
		}
	})

	t.Run("NotServingWhenGateNotReady", func(t *testing.T) {
		h := newHealthHandler(
			&fakeUptime{d: 5 * time.Second},
			&fakeHealth{statuses: map[string]healthpb.HealthCheckResponse_ServingStatus{
				"": healthpb.HealthCheckResponse_SERVING,
			}},
			&fakeReady{v: false},
		)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

		if rr.Code != http.StatusServiceUnavailable {
			t.Fatalf("status: got %d want 503", rr.Code)
		}
		got := decode(t, rr)
		if got.Status != "NOT_SERVING" {
			t.Errorf("overall status: got %q want NOT_SERVING", got.Status)
		}
		// grpc subcheck is still SERVING because the gate is a
		// separate axis from per-service health.
		if got.Checks["grpc"] != "SERVING" {
			t.Errorf("checks[grpc]: got %q want SERVING", got.Checks["grpc"])
		}
	})

	t.Run("ZeroUptimeWhenNotMarked", func(t *testing.T) {
		h := newHealthHandler(
			&fakeUptime{d: 0},
			&fakeHealth{statuses: map[string]healthpb.HealthCheckResponse_ServingStatus{
				"": healthpb.HealthCheckResponse_SERVING,
			}},
			&fakeReady{v: true},
		)
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/v1/health", nil))

		got := decode(t, rr)
		if got.UptimeSeconds != 0 {
			t.Errorf("uptime: got %d want 0", got.UptimeSeconds)
		}
	})
}

func TestNoopGatewayServer(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := (NoopGatewayServer{}).Run(ctx); err != nil {
		t.Errorf("noop run: %v", err)
	}
}

func TestNewGatewayServer_DisabledWhenAddrEmpty(t *testing.T) {
	gw, err := NewGatewayServer(GatewayConfig{Addr: ""}, CORSConfig{}, nil, nil, nil, nil)
	if err != nil {
		t.Fatalf("new gateway: %v", err)
	}
	if _, ok := gw.(NoopGatewayServer); !ok {
		t.Errorf("got %T, want NoopGatewayServer", gw)
	}
}
