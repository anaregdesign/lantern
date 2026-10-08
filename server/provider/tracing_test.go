package provider

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/baggage"
	"go.opentelemetry.io/otel/propagation"
	collector "go.opentelemetry.io/proto/otlp/collector/trace/v1"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type traceCollector struct {
	collector.UnimplementedTraceServiceServer
	requests chan *collector.ExportTraceServiceRequest
	headers  chan string
}

func (c *traceCollector) Export(ctx context.Context, req *collector.ExportTraceServiceRequest) (*collector.ExportTraceServiceResponse, error) {
	md, _ := metadata.FromIncomingContext(ctx)
	c.headers <- strings.Join(md.Get("x-lantern-test"), ",")
	c.requests <- req
	return &collector.ExportTraceServiceResponse{}, nil
}

func tracingTestEnvironment(t *testing.T) *slog.Logger {
	t.Helper()
	for _, key := range []string{
		"OTEL_EXPORTER_OTLP_ENDPOINT", "OTEL_EXPORTER_OTLP_TRACES_ENDPOINT",
		"OTEL_EXPORTER_OTLP_PROTOCOL", "OTEL_EXPORTER_OTLP_TRACES_PROTOCOL",
		"OTEL_EXPORTER_OTLP_HEADERS", "OTEL_EXPORTER_OTLP_TRACES_HEADERS",
		"OTEL_EXPORTER_OTLP_INSECURE", "OTEL_EXPORTER_OTLP_TRACES_INSECURE",
		"OTEL_TRACES_SAMPLER", "OTEL_TRACES_SAMPLER_ARG", "OTEL_SDK_DISABLED",
	} {
		t.Setenv(key, "")
	}
	previousProvider, previousPropagator := otel.GetTracerProvider(), otel.GetTextMapPropagator()
	t.Cleanup(func() {
		otel.SetTracerProvider(previousProvider)
		otel.SetTextMapPropagator(previousPropagator)
	})
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func TestTracingDisabledAndInvalidProtocol(t *testing.T) {
	logger := tracingTestEnvironment(t)
	previous := otel.GetTracerProvider()
	disabled, err := NewTracing(logger)
	if err != nil || disabled.tp != nil || otel.GetTracerProvider() != previous {
		t.Fatalf("disabled tracing = %+v, %v", disabled, err)
	}
	for _, tracing := range []*Tracing{nil, disabled} {
		if err := tracing.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
	t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "unsupported")
	if tracing, err := NewTracing(logger); err == nil || tracing != nil || otel.GetTracerProvider() != previous {
		t.Fatalf("invalid protocol = %+v, %v", tracing, err)
	}
}

func TestTracingExportAndPropagation(t *testing.T) {
	for _, protocol := range []string{"http/protobuf", "grpc"} {
		t.Run(protocol, func(t *testing.T) {
			logger := tracingTestEnvironment(t)
			requests := make(chan *collector.ExportTraceServiceRequest, 1)
			headers := make(chan string, 1)
			var endpoint string
			if protocol == "grpc" {
				listener, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				server := grpc.NewServer()
				collector.RegisterTraceServiceServer(server, &traceCollector{requests: requests, headers: headers})
				go func() { _ = server.Serve(listener) }()
				t.Cleanup(server.Stop)
				endpoint = "http://" + listener.Addr().String()
			} else {
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					data, err := io.ReadAll(r.Body)
					req := &collector.ExportTraceServiceRequest{}
					if err != nil || proto.Unmarshal(data, req) != nil || r.URL.Path != "/v1/traces" || r.Header.Get("Content-Type") != "application/x-protobuf" {
						t.Errorf("invalid HTTP OTLP request: %s, %v", r.URL.Path, err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					headers <- r.Header.Get("X-Lantern-Test")
					requests <- req
					w.Header().Set("Content-Type", "application/x-protobuf")
				}))
				t.Cleanup(server.Close)
				endpoint = server.URL + "/v1/traces"
			}
			// Trace-specific endpoint/protocol/headers must take precedence.
			t.Setenv("OTEL_EXPORTER_OTLP_ENDPOINT", "http://127.0.0.1:1")
			t.Setenv("OTEL_EXPORTER_OTLP_PROTOCOL", "unsupported")
			t.Setenv("OTEL_EXPORTER_OTLP_HEADERS", "x-lantern-test=wrong")
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_ENDPOINT", endpoint)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_PROTOCOL", protocol)
			t.Setenv("OTEL_EXPORTER_OTLP_TRACES_HEADERS", "x-lantern-test=collector")
			tracing, err := NewTracing(logger)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = tracing.Shutdown(context.Background()) })
			const traceID = "0123456789abcdef0123456789abcdef"
			const parentID = "0123456789abcdef"
			request := httptest.NewRequest(http.MethodGet, "http://lantern.invalid/query", nil)
			request.Header.Set("traceparent", "00-"+traceID+"-"+parentID+"-01")
			request.Header.Set("baggage", "tenant=local")
			handler := otelhttp.NewHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if baggage.FromContext(r.Context()).Member("tenant").Value() != "local" {
					t.Error("W3C baggage not propagated")
				}
				carrier := propagation.HeaderCarrier(http.Header{})
				otel.GetTextMapPropagator().Inject(r.Context(), carrier)
				if carrier.Get("traceparent") == "" || carrier.Get("baggage") != "tenant=local" {
					t.Error("outbound W3C context not propagated")
				}
				w.WriteHeader(http.StatusNoContent)
			}), "query", otelhttp.WithTracerProvider(tracing.tp))
			handler.ServeHTTP(httptest.NewRecorder(), request)
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			if err := tracing.tp.ForceFlush(cancelled); !errors.Is(err, context.Canceled) {
				t.Fatalf("cancelled flush = %v", err)
			}
			ctx, stop := context.WithTimeout(context.Background(), 5*time.Second)
			defer stop()
			// Shutdown must flush the queued span even after a cancelled flush.
			if err := tracing.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
			select {
			case req := <-requests:
				if len(req.ResourceSpans) != 1 || len(req.ResourceSpans[0].ScopeSpans) != 1 || len(req.ResourceSpans[0].ScopeSpans[0].Spans) != 1 {
					t.Fatalf("unexpected exported spans: %v", req)
				}
				span := req.ResourceSpans[0].ScopeSpans[0].Spans[0]
				if span.Name != "GET" || span.EndTimeUnixNano < span.StartTimeUnixNano || span.EndTimeUnixNano == 0 {
					t.Fatalf("unfinished or misnamed span: %v", span)
				}
				if hex.EncodeToString(span.TraceId) != traceID || hex.EncodeToString(span.ParentSpanId) != parentID {
					t.Fatalf("remote trace/parent lost: %x/%x", span.TraceId, span.ParentSpanId)
				}
			case <-ctx.Done():
				t.Fatal("Shutdown exported no spans")
			}
			if got := <-headers; got != "collector" {
				t.Fatalf("exporter header = %q", got)
			}
			if err := tracing.Shutdown(ctx); err != nil {
				t.Fatal(err)
			}
		})
	}
}
