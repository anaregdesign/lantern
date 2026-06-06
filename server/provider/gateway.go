package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/envconfig"
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/protobuf/encoding/protojson"
)

// GatewayConfig governs the optional REST surface exposed via grpc-gateway
// for the admin SPA and HTTP-only orchestrators (k8s httpGet, plain uptime
// monitors).
//
//   - LANTERN_GATEWAY_ADDR                    host:port for the REST/JSON
//     gateway. Default ":6381". Set empty to disable.
//   - LANTERN_GATEWAY_READ_HEADER_TIMEOUT_MS  protect against slowloris on
//     the gateway listener. Default 5000 (5s).
type GatewayConfig struct {
	Addr              string
	ReadHeaderTimeout time.Duration
}

// NewGatewayConfig selects the Gateway slice of Config.
func NewGatewayConfig(c *Config) GatewayConfig { return c.Gateway }

// loadGatewayConfig reads GatewayConfig from the environment.
func loadGatewayConfig() GatewayConfig {
	return GatewayConfig{
		Addr: envconfig.String("LANTERN_GATEWAY_ADDR", ":6381"),
		ReadHeaderTimeout: time.Duration(envconfig.Int(
			"LANTERN_GATEWAY_READ_HEADER_TIMEOUT_MS", 5000,
		)) * time.Millisecond,
	}
}

// GatewayServer is the long-running goroutine that exposes the grpc-gateway
// REST surface plus the JSON /v1/health probe. NewGatewayServer returns a
// real HTTP server when GatewayConfig.Addr is non-empty and a
// NoopGatewayServer otherwise, so callers never have to nil-check before
// calling Run (Null Object pattern; mirrors MetricsServer).
type GatewayServer interface {
	Run(ctx context.Context) error
}

// NoopGatewayServer is the disabled-gateway implementation. Its Run waits
// for ctx to be canceled and returns nil, so the App errgroup behaves
// identically whether or not the gateway is enabled.
type NoopGatewayServer struct{}

func (NoopGatewayServer) Run(ctx context.Context) error {
	<-ctx.Done()
	return nil
}

// httpGatewayServer is the real REST gateway server.
type httpGatewayServer struct {
	srv    *http.Server
	logger *slog.Logger
	addr   string
}

// NewGatewayServer wires the LanternService onto a grpc-gateway runtime mux
// via the in-process handler (no extra dial / no extra TCP slot per
// request). Streaming RPCs (Illuminate, Subscribe) are intentionally not
// routed through the gateway — admin clients are expected to keep using
// gRPC for streams.
//
// The /v1/health JSON probe is mounted under a sibling mux at a path that
// the gateway pattern matcher does not claim (the gRPC service surface
// does not declare /v1/health as an RPC). Returns 200 + {"status":"SERVING"
// ...} when both the overall gRPC health entry is SERVING and the
// readiness gate is Ready; returns 503 + {"status":"NOT_SERVING", ...}
// otherwise. The body always includes per-subcheck status so a future
// /v1/health/ready split can be added without rewriting the shape.
func NewGatewayServer(
	cfg GatewayConfig,
	svc *service.LanternService,
	hs *health.Server,
	gate *readiness.Gate,
	logger *slog.Logger,
) (GatewayServer, error) {
	if cfg.Addr == "" {
		return NoopGatewayServer{}, nil
	}
	mux := runtime.NewServeMux(
		// Emit unpopulated message fields so the admin SPA can rely on
		// stable JSON shapes without conditional checks.
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				EmitUnpopulated: true,
			},
			UnmarshalOptions: protojson.UnmarshalOptions{
				DiscardUnknown: true,
			},
		}),
	)
	if err := pb.RegisterLanternServiceHandlerServer(context.Background(), mux, svc); err != nil {
		return nil, fmt.Errorf("register lantern service gateway: %w", err)
	}

	root := http.NewServeMux()
	root.HandleFunc("/v1/health", newHealthHandler(svc, hs, gate))
	// Anything else falls through to the gRPC gateway mux so the
	// REST/JSON contract documented by the proto annotations stays
	// authoritative.
	root.Handle("/", mux)

	return &httpGatewayServer{
		srv: &http.Server{
			Addr:              cfg.Addr,
			Handler:           root,
			ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		},
		logger: logger,
		addr:   cfg.Addr,
	}, nil
}

// Run blocks until ctx is canceled or ListenAndServe returns an error other
// than http.ErrServerClosed.
func (g *httpGatewayServer) Run(ctx context.Context) error {
	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = g.srv.Shutdown(shutdownCtx)
	}()
	g.logger.Info("gateway server starting", slog.String("addr", g.addr))
	if err := g.srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// healthResponse is the JSON shape returned by GET /v1/health. The order of
// fields is stable so probes that diff bodies get a deterministic payload.
type healthResponse struct {
	Status        string            `json:"status"`
	Checks        map[string]string `json:"checks"`
	UptimeSeconds int64             `json:"uptime_seconds"`
}

// uptimeProvider is the read-only slice of LanternService that the health
// handler needs. Declared here as a consumer-defined interface so the
// handler test can stub it without standing up the whole service.
type uptimeProvider interface {
	Uptime() time.Duration
}

// healthChecker is the read-only slice of *health.Server the handler needs.
type healthChecker interface {
	Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error)
}

// readyChecker is the narrow slice of *readiness.Gate the handler needs.
type readyChecker interface {
	Ready() bool
}

func newHealthHandler(up uptimeProvider, hs healthChecker, gate readyChecker) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Probe-style endpoints must never block on tail latency. 1s is
		// well above any expected health-server response and well below
		// any reasonable probe timeout.
		ctx, cancel := context.WithTimeout(r.Context(), time.Second)
		defer cancel()

		checks := map[string]string{
			"grpc":        checkServingStatus(ctx, hs, ""),
			"graph_cache": checkServingStatus(ctx, hs, service.ServiceName),
			"replication": checkServingStatus(ctx, hs, service.ReplicationServiceName),
		}

		overall := "SERVING"
		statusCode := http.StatusOK
		// Overall must be SERVING by the health-server *and* the
		// readiness gate must be Ready. The readiness gate is what
		// drives drain (#188), so it is the source of truth for
		// "this node should receive new traffic".
		if checks["grpc"] != "SERVING" || (gate != nil && !gate.Ready()) {
			overall = "NOT_SERVING"
			statusCode = http.StatusServiceUnavailable
		}

		var uptime int64
		if up != nil {
			uptime = int64(up.Uptime().Seconds())
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		_ = json.NewEncoder(w).Encode(healthResponse{
			Status:        overall,
			Checks:        checks,
			UptimeSeconds: uptime,
		})
	}
}

// checkServingStatus folds a Check call into a stable string. Anything that
// is not explicitly SERVING (NOT_SERVING, SERVICE_UNKNOWN, or an error) is
// reported as NOT_SERVING; the JSON contract is "operator can tell at a
// glance whether a sub-system is up", not "expose every grpc-health enum
// value".
func checkServingStatus(ctx context.Context, hs healthChecker, name string) string {
	if hs == nil {
		return "NOT_SERVING"
	}
	resp, err := hs.Check(ctx, &healthpb.HealthCheckRequest{Service: name})
	if err != nil {
		return "NOT_SERVING"
	}
	if resp.GetStatus() == healthpb.HealthCheckResponse_SERVING {
		return "SERVING"
	}
	return "NOT_SERVING"
}
