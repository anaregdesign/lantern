// Package provider: connect_middleware.go hosts the Connect-Go
// interceptors that supply Lantern's per-RPC observability. The slog
// channels and Prometheus metric names are intentionally locked to the
// `grpc.*` / `grpc_server_*` vocabulary that the historical
// `grpc-ecosystem/go-grpc-middleware` chain emitted, so existing
// dashboards, alerts, and log-search patterns keep working unchanged.
// The wire protocol is Connect; the field names are an operator-
// continuity contract, not a transport claim.
//
// Coverage:
//   - LoggingInterceptor       slog StartCall / FinishCall lines per RPC
//   - PrometheusInterceptor    grpc_server_started_total /
//     grpc_server_handled_total /
//     grpc_server_handling_seconds — same names
//     the grpcprom collector used.
//   - SlowRPCInterceptor.ConnectInterceptor() exposes the per-RPC
//     threshold knob.
//
// Panic recovery is handled by Connect's own connect.WithRecover handler
// option, not by an interceptor here, because connect.WithRecover hooks
// the per-RPC error path before any interceptor runs.
//
// OpenTelemetry tracing is handled by otelhttp at the listener level
// (server/provider/lantern_listener.go) so every request — including
// non-RPC paths like /grpc.health.v1.Health/Check — gets a span without
// per-handler wiring.
package provider

import (
	"context"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/prometheus/client_golang/prometheus"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ConnectInterceptor returns a connect.UnaryInterceptorFunc that emits
// the warn-level "slow rpc" slog channel. Threshold of 0 disables
// emission (Enabled() reports false and the listener skips
// installation).
//
// Fields:
//
//   - method        Connect procedure path (e.g.
//     "/graph.v1.LanternService/GetVertex")
//   - code          Connect status code string (e.g. "not_found", "ok")
//   - duration_ms   handler wall-clock in milliseconds
//   - threshold_ms  the configured threshold in milliseconds
func (s *SlowRPCInterceptor) ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			illuminateLabels, hasIlluminateLabels := illuminateTelemetryLabelsFor(req.Any())
			if hasIlluminateLabels {
				setIlluminateSpanAttributes(ctx, illuminateLabels)
			}
			searchLabels, hasSearchLabels := searchTelemetryLabelsFor(req.Any())
			if hasSearchLabels {
				setSearchSpanAttributes(ctx, searchLabels)
			}
			start := time.Now()
			resp, err := next(ctx, req)
			d := time.Since(start)
			if d > s.threshold {
				attrs := []slog.Attr{
					slog.String("method", req.Spec().Procedure),
					slog.String("code", connectCodeString(err)),
					slog.Int64("duration_ms", d.Milliseconds()),
					slog.Int64("threshold_ms", s.threshold.Milliseconds()),
				}
				if hasIlluminateLabels {
					attrs = append(attrs, illuminateLabels.slogAttrs()...)
				}
				if hasSearchLabels {
					attrs = append(attrs, searchLabels.slogAttrs()...)
				}
				s.logger.LogAttrs(ctx, slog.LevelWarn, "slow rpc", attrs...)
			}
			return resp, err
		}
	}
}

// LoggingInterceptor emits one info-level "rpc start" line on call entry
// and one info-level "rpc end" line on call exit.
//
// The field names (started_at / msg / grpc.method / grpc.code /
// grpc.duration_ms) intentionally retain the `grpc.*` prefix the
// historical grpc-middleware logging interceptor used, so existing
// log-search patterns keep working. The wire protocol is Connect; the
// field names are an operator-continuity contract.
type LoggingInterceptor struct {
	logger *slog.Logger
}

// NewLoggingInterceptor builds the per-RPC slog interceptor. A nil
// logger disables logging entirely (the returned interceptor is a
// pass-through so callers can wire unconditionally).
func NewLoggingInterceptor(logger *slog.Logger) *LoggingInterceptor {
	return &LoggingInterceptor{logger: logger}
}

// ConnectInterceptor returns the unary interceptor. It is named
// ConnectInterceptor (not the more obvious Interceptor) for symmetry
// with SlowRPCInterceptor.ConnectInterceptor() and
// ValidationInterceptor.ConnectInterceptor() so the listener wiring
// reads uniformly.
func (l *LoggingInterceptor) ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			illuminateLabels, hasIlluminateLabels := illuminateTelemetryLabelsFor(req.Any())
			if hasIlluminateLabels {
				setIlluminateSpanAttributes(ctx, illuminateLabels)
			}
			searchLabels, hasSearchLabels := searchTelemetryLabelsFor(req.Any())
			if hasSearchLabels {
				setSearchSpanAttributes(ctx, searchLabels)
			}
			method := req.Spec().Procedure
			start := time.Now()
			if l != nil && l.logger != nil {
				attrs := []slog.Attr{slog.String("protocol", "connect"), slog.String("grpc.method", method)}
				if hasIlluminateLabels {
					attrs = append(attrs, illuminateLabels.slogAttrs()...)
				}
				if hasSearchLabels {
					attrs = append(attrs, searchLabels.slogAttrs()...)
				}
				l.logger.LogAttrs(ctx, slog.LevelInfo, "started call", attrs...)
			}
			resp, err := next(ctx, req)
			if l != nil && l.logger != nil {
				attrs := []slog.Attr{
					slog.String("protocol", "connect"),
					slog.String("grpc.method", method),
					slog.String("grpc.code", connectCodeString(err)),
					slog.Int64("grpc.duration_ms", time.Since(start).Milliseconds()),
				}
				if hasIlluminateLabels {
					attrs = append(attrs, illuminateLabels.slogAttrs()...)
				}
				if hasSearchLabels {
					attrs = append(attrs, searchLabels.slogAttrs()...)
				}
				l.logger.LogAttrs(ctx, slog.LevelInfo, "finished call", attrs...)
			}
			return resp, err
		}
	}
}

type illuminateTelemetryLabels struct {
	family, reduction, objective, weighting string
}

func illuminateTelemetryLabelsFor(message any) (illuminateTelemetryLabels, bool) {
	req, ok := message.(*pb.IlluminateRequest)
	if !ok {
		return illuminateTelemetryLabels{}, false
	}
	labels := illuminateTelemetryLabels{
		family: "unknown", reduction: "none", objective: "maximize", weighting: telemetryWeighting(req.GetWeighting()),
	}
	switch params := req.GetParams().(type) {
	case *pb.IlluminateRequest_Bfs:
		labels.family = "bfs"
		if params.Bfs != nil {
			labels.reduction = telemetryReduction(params.Bfs.GetReduction())
			labels.objective = telemetryObjective(params.Bfs.GetObjective())
		}
	case *pb.IlluminateRequest_Ppr:
		labels.family = "ppr"
	case *pb.IlluminateRequest_Community:
		labels.family = "community"
		if params.Community != nil {
			labels.reduction = telemetryReduction(params.Community.GetReduction())
			labels.objective = telemetryObjective(params.Community.GetObjective())
		}
	}
	return labels, true
}

func (l illuminateTelemetryLabels) slogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.String("illuminate.family", l.family),
		slog.String("illuminate.reduction", l.reduction),
		slog.String("illuminate.objective", l.objective),
		slog.String("illuminate.weighting", l.weighting),
	}
}

func setIlluminateSpanAttributes(ctx context.Context, labels illuminateTelemetryLabels) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("lantern.illuminate.family", labels.family),
		attribute.String("lantern.illuminate.reduction", labels.reduction),
		attribute.String("lantern.illuminate.objective", labels.objective),
		attribute.String("lantern.illuminate.weighting", labels.weighting),
	)
}

type searchTelemetryLabels struct {
	mode          string
	phrase        bool
	fuzziness     string
	prefixTerms   bool
	prefixPresent bool
}

func searchTelemetryLabelsFor(message any) (searchTelemetryLabels, bool) {
	req, ok := message.(*pb.SearchVerticesRequest)
	if !ok {
		return searchTelemetryLabels{}, false
	}
	o := req.GetOptions()
	return searchTelemetryLabels{
		mode:          telemetrySearchMode(o),
		phrase:        o.GetPhrase(),
		fuzziness:     telemetrySearchFuzziness(o.GetFuzziness()),
		prefixTerms:   o.GetPrefixTerms(),
		prefixPresent: req.GetPrefix() != "",
	}, true
}

func (l searchTelemetryLabels) slogAttrs() []slog.Attr {
	return []slog.Attr{
		slog.String("search.mode", l.mode),
		slog.Bool("search.phrase", l.phrase),
		slog.String("search.fuzziness", l.fuzziness),
		slog.Bool("search.prefix_terms", l.prefixTerms),
		slog.Bool("search.prefix_present", l.prefixPresent),
	}
}

func setSearchSpanAttributes(ctx context.Context, labels searchTelemetryLabels) {
	trace.SpanFromContext(ctx).SetAttributes(
		attribute.String("lantern.search.mode", labels.mode),
		attribute.Bool("lantern.search.phrase", labels.phrase),
		attribute.String("lantern.search.fuzziness", labels.fuzziness),
		attribute.Bool("lantern.search.prefix_terms", labels.prefixTerms),
		attribute.Bool("lantern.search.prefix_present", labels.prefixPresent),
	)
}

func telemetrySearchMode(o *pb.SearchOptions) string {
	if o == nil || o.GetMatchMode() == pb.MatchMode_MATCH_MODE_UNSPECIFIED {
		return "server"
	}
	switch o.GetMatchMode() {
	case pb.MatchMode_MATCH_MODE_ANY:
		return "any"
	case pb.MatchMode_MATCH_MODE_ALL:
		return "all"
	case pb.MatchMode_MATCH_MODE_MIN_SHOULD:
		return "min_should"
	default:
		return "unknown"
	}
}

func telemetrySearchFuzziness(value uint32) string {
	switch value {
	case 0:
		return "0"
	case 1:
		return "1"
	case 2:
		return "2"
	default:
		return "other"
	}
}

func telemetryReduction(value pb.Reduction) string {
	switch value {
	case pb.Reduction_REDUCTION_UNSPECIFIED:
		return "none"
	case pb.Reduction_REDUCTION_MINIMUM_SPANNING_TREE:
		return "mst"
	case pb.Reduction_REDUCTION_SHORTEST_PATH_TREE:
		return "spt"
	default:
		return "unknown"
	}
}

func telemetryObjective(value pb.Objective) string {
	if value == pb.Objective_OBJECTIVE_MINIMIZE {
		return "minimize"
	}
	if value == pb.Objective_OBJECTIVE_UNSPECIFIED || value == pb.Objective_OBJECTIVE_MAXIMIZE {
		return "maximize"
	}
	return "unknown"
}

func telemetryWeighting(value pb.Weighting) string {
	switch value {
	case pb.Weighting_WEIGHTING_UNSPECIFIED, pb.Weighting_WEIGHTING_RAW:
		return "raw"
	case pb.Weighting_WEIGHTING_TFIDF:
		return "tfidf"
	case pb.Weighting_WEIGHTING_BM25:
		return "bm25"
	default:
		return "unknown"
	}
}

// PrometheusInterceptor reproduces the four grpc-middleware metric
// families on the Connect path so Grafana dashboards built against
// `grpc_server_*` metric names keep working. The names are LOCKED —
// operators have alerts wired against them; the wire protocol is
// Connect.
//
// Families (counter / counter / histogram):
//
//   - grpc_server_started_total{grpc_type,grpc_service,grpc_method}
//   - grpc_server_handled_total{grpc_type,grpc_service,grpc_method,grpc_code}
//   - grpc_server_handling_seconds_bucket{grpc_type,grpc_service,grpc_method,le}
//     (plus the matching _sum / _count series)
//
// grpc_type is hard-coded to "unary" because the only streaming RPCs
// (Subscribe / Snapshot) are not metered by this interceptor either
// (Connect's interceptor seam only fires on unary calls; stream
// lifecycle observation is captured by the listener-level otelhttp
// span).
type PrometheusInterceptor struct {
	started         *prometheus.CounterVec
	handled         *prometheus.CounterVec
	handlingSeconds *prometheus.HistogramVec
}

// NewPrometheusInterceptor registers the three metric vectors on reg.
// Panics if registration fails (mirrors grpcprom's strict approach —
// duplicate metric registration is a wire-time bug, not a runtime
// condition).
func NewPrometheusInterceptor(reg *prometheus.Registry) *PrometheusInterceptor {
	started := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_server_started_total",
			Help: "Total number of RPCs started on the server.",
		},
		[]string{"grpc_type", "grpc_service", "grpc_method"},
	)
	handled := prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name: "grpc_server_handled_total",
			Help: "Total number of RPCs completed on the server, regardless of success or failure.",
		},
		[]string{"grpc_type", "grpc_service", "grpc_method", "grpc_code"},
	)
	handlingSeconds := prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "grpc_server_handling_seconds",
			Help:    "Histogram of response latency (seconds) of RPCs that had been application-level handled by the server.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"grpc_type", "grpc_service", "grpc_method"},
	)
	reg.MustRegister(started, handled, handlingSeconds)
	return &PrometheusInterceptor{
		started:         started,
		handled:         handled,
		handlingSeconds: handlingSeconds,
	}
}

// ConnectInterceptor returns the interceptor that bumps the three
// metric families per RPC.
func (p *PrometheusInterceptor) ConnectInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			service, method := splitProcedure(req.Spec().Procedure)
			p.started.WithLabelValues("unary", service, method).Inc()
			start := time.Now()
			resp, err := next(ctx, req)
			code := connectCodeString(err)
			p.handled.WithLabelValues("unary", service, method, code).Inc()
			p.handlingSeconds.WithLabelValues("unary", service, method).Observe(time.Since(start).Seconds())
			return resp, err
		}
	}
}

// splitProcedure parses a Connect procedure path of the form
// "/<service>/<method>" into ("<service>", "<method>"). Malformed
// inputs collapse the whole path into method so the metric is still
// emitted (visibility > correctness when the parser surprises us).
func splitProcedure(p string) (service, method string) {
	if len(p) == 0 || p[0] != '/' {
		return "", p
	}
	rest := p[1:]
	for i := len(rest) - 1; i >= 0; i-- {
		if rest[i] == '/' {
			return rest[:i], rest[i+1:]
		}
	}
	return "", rest
}

// connectCodeString maps a handler error to the Connect code's
// canonical lower_snake_case string ("ok" / "not_found" /
// "resource_exhausted" / …). Returns "ok" for nil. Used by every
// metrics / logging label so the wire format matches what the
// grpc-middleware chain emitted historically.
func connectCodeString(err error) string {
	if err == nil {
		return "ok"
	}
	return connect.CodeOf(err).String()
}
