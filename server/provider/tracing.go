package provider

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"runtime/debug"
	"time"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracegrpc"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/propagation"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
)

// Tracing owns the optional OpenTelemetry TracerProvider. The zero value (and
// a nil receiver) represent "tracing disabled" — Shutdown is a no-op.
type Tracing struct {
	tp     *sdktrace.TracerProvider
	logger *slog.Logger
}

// NewTracing installs an OTLP tracer provider when the standard OTel env vars
// are present, hooks it into the global otel.SetTracerProvider so the
// existing otelgrpc stats handler actually exports spans, and returns a
// handle whose Shutdown is wired into App's graceful-stop path.
//
// Activation rules:
//   - OTEL_EXPORTER_OTLP_ENDPOINT (or the trace-specific
//     OTEL_EXPORTER_OTLP_TRACES_ENDPOINT) must be set; otherwise no provider
//     is installed and the global default (noop) stays in place.
//   - OTEL_EXPORTER_OTLP_PROTOCOL (or _TRACES_PROTOCOL) selects the wire
//     protocol — `grpc` (default) or `http/protobuf`.
//
// All other OTel SDK env vars (insecure, headers, timeout, …) are honoured by
// the exporters out of the box.
func NewTracing(logger *slog.Logger) (*Tracing, error) {
	endpoint := firstNonEmpty(
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT"),
		os.Getenv("OTEL_EXPORTER_OTLP_ENDPOINT"),
	)
	if endpoint == "" {
		logger.Info("otel tracing disabled (OTEL_EXPORTER_OTLP_ENDPOINT unset)")
		return &Tracing{logger: logger}, nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	protocol := firstNonEmpty(
		os.Getenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL"),
		os.Getenv("OTEL_EXPORTER_OTLP_PROTOCOL"),
		"grpc",
	)

	var (
		exp otlptrace.Client
	)
	switch protocol {
	case "grpc":
		exp = otlptracegrpc.NewClient()
	case "http/protobuf", "http":
		exp = otlptracehttp.NewClient()
	default:
		return nil, fmt.Errorf("unsupported OTEL_EXPORTER_OTLP_PROTOCOL=%q (want grpc|http/protobuf)", protocol)
	}

	exporter, err := otlptrace.New(ctx, exp)
	if err != nil {
		return nil, fmt.Errorf("init otlp trace exporter: %w", err)
	}

	res, err := resource.Merge(
		resource.Default(),
		resource.NewSchemaless(
			semconv.ServiceName("lantern"),
			semconv.ServiceVersion(buildVersion()),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("build otel resource: %w", err)
	}

	tp := sdktrace.NewTracerProvider(
		sdktrace.WithBatcher(exporter),
		sdktrace.WithResource(res),
	)
	otel.SetTracerProvider(tp)
	// Propagate W3C tracecontext + baggage so spans link across services.
	otel.SetTextMapPropagator(propagation.NewCompositeTextMapPropagator(
		propagation.TraceContext{},
		propagation.Baggage{},
	))

	logger.Info("otel tracing enabled",
		slog.String("endpoint", endpoint),
		slog.String("protocol", protocol),
	)
	return &Tracing{tp: tp, logger: logger}, nil
}

// Shutdown flushes pending spans and tears down the exporter. Safe to call on
// a nil receiver or when tracing was never enabled.
func (t *Tracing) Shutdown(ctx context.Context) error {
	if t == nil || t.tp == nil {
		return nil
	}
	return t.tp.Shutdown(ctx)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func buildVersion() string {
	bi, ok := debug.ReadBuildInfo()
	if !ok || bi.Main.Version == "" {
		return "(devel)"
	}
	return bi.Main.Version
}
