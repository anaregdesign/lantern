package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func newJSONLogger(buf *bytes.Buffer, lvl slog.Level) *slog.Logger {
	return slog.New(slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: lvl}))
}

// decodeRecords returns one map per JSON line written to buf.
func decodeRecords(t *testing.T, buf *bytes.Buffer) []map[string]any {
	t.Helper()
	var out []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if line == "" {
			continue
		}
		var rec map[string]any
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			t.Fatalf("decode log line %q: %v", line, err)
		}
		out = append(out, rec)
	}
	return out
}

// TestSlowRPCInterceptor_Enabled asserts the Enabled() gate honours
// threshold/logger preconditions (drives the wiring branch in
// NewLanternListener).
func TestSlowRPCInterceptor_Enabled(t *testing.T) {
	var buf bytes.Buffer
	l := newJSONLogger(&buf, slog.LevelWarn)

	cases := []struct {
		name string
		s    *SlowRPCInterceptor
		want bool
	}{
		{"nil_receiver", nil, false},
		{"zero_threshold", NewSlowRPCInterceptor(0, l), false},
		{"nil_logger", NewSlowRPCInterceptor(time.Millisecond, nil), false},
		{"enabled", NewSlowRPCInterceptor(time.Millisecond, l), true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.s.Enabled(); got != tc.want {
				t.Errorf("Enabled() = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestSlowRPCInterceptor_ConnectFiresOnSlowHandler asserts a single
// warn line per slow unary Connect call with the documented field
// schema. method comes from req.Spec().Procedure; code is the Connect
// canonical lower_snake_case string.
func TestSlowRPCInterceptor_ConnectFiresOnSlowHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf, slog.LevelWarn)
	s := NewSlowRPCInterceptor(5*time.Millisecond, logger)

	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		time.Sleep(20 * time.Millisecond)
		return nil, connect.NewError(connect.CodeDeadlineExceeded, errors.New("boom"))
	})

	_, err := s.ConnectInterceptor()(next)(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"}))
	if connect.CodeOf(err) != connect.CodeDeadlineExceeded {
		t.Fatalf("err code = %v, want CodeDeadlineExceeded", connect.CodeOf(err))
	}

	recs := decodeRecords(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	rec := recs[0]
	if rec["msg"] != "slow rpc" {
		t.Errorf("msg = %v, want %q", rec["msg"], "slow rpc")
	}
	// Spec().Procedure is populated only when the request flows
	// through a real connect.Handler; the helper-only test path
	// leaves it empty. The wired-up integration tests cover the
	// procedure path.
	if _, ok := rec["method"].(string); !ok {
		t.Errorf("method field missing: %v", rec)
	}
	if rec["code"] != "deadline_exceeded" {
		t.Errorf("code = %v, want %q", rec["code"], "deadline_exceeded")
	}
	if rec["threshold_ms"] != float64(5) {
		t.Errorf("threshold_ms = %v, want 5", rec["threshold_ms"])
	}
	if d, _ := rec["duration_ms"].(float64); d < 5 {
		t.Errorf("duration_ms = %v, want >= 5", rec["duration_ms"])
	}
}

// TestSlowRPCInterceptor_ConnectSilentOnFastHandler asserts that
// handlers completing within the threshold emit no log records.
func TestSlowRPCInterceptor_ConnectSilentOnFastHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf, slog.LevelWarn)
	s := NewSlowRPCInterceptor(500*time.Millisecond, logger)

	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})

	if _, err := s.ConnectInterceptor()(next)(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected log output: %s", buf.String())
	}
}

func TestSlowRPCInterceptor_IlluminateFamilyFields(t *testing.T) {
	var buf bytes.Buffer
	s := NewSlowRPCInterceptor(time.Nanosecond, newJSONLogger(&buf, slog.LevelWarn))
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return nil, connect.NewError(connect.CodeFailedPrecondition, errors.New("bad reduction"))
	})
	req := connect.NewRequest(&pb.IlluminateRequest{Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{
		Step: 1, FanOut: 1, Reduction: pb.Reduction_REDUCTION_SHORTEST_PATH_TREE,
	}}})
	_, _ = s.ConnectInterceptor()(next)(context.Background(), req)
	recs := decodeRecords(t, &buf)
	if len(recs) != 1 || recs[0]["illuminate.family"] != "bfs" || recs[0]["illuminate.reduction"] != "spt" {
		t.Fatalf("slow Illuminate fields = %+v, want family=bfs reduction=spt", recs)
	}
}

func TestLoggingInterceptor_IlluminateSpanAttributes(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("test").Start(context.Background(), "Illuminate")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.IlluminateResponse{}), nil
	})
	req := connect.NewRequest(&pb.IlluminateRequest{Params: &pb.IlluminateRequest_Ppr{Ppr: &pb.PprParams{TopN: 2}}})
	if _, err := NewLoggingInterceptor(nil).ConnectInterceptor()(next)(ctx, req); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attrs := map[string]string{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsString()
	}
	if attrs["lantern.illuminate.family"] != "ppr" || attrs["lantern.illuminate.reduction"] != "none" {
		t.Fatalf("Illuminate span attributes = %+v", attrs)
	}
}

func TestSlowRPCInterceptor_SearchFieldsAreBounded(t *testing.T) {
	var buf bytes.Buffer
	s := NewSlowRPCInterceptor(time.Nanosecond, newJSONLogger(&buf, slog.LevelWarn))
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.SearchVerticesResponse{}), nil
	})
	req := connect.NewRequest(&pb.SearchVerticesRequest{
		Query:  "private-query-value",
		Prefix: "private-prefix-value",
		Options: &pb.SearchOptions{
			MatchMode:   pb.MatchMode(99),
			Phrase:      true,
			Fuzziness:   99,
			PrefixTerms: true,
		},
	})
	if _, err := s.ConnectInterceptor()(next)(context.Background(), req); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	recs := decodeRecords(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("slow search records = %d, want 1", len(recs))
	}
	rec := recs[0]
	for key, want := range map[string]any{
		"search.mode":           "unknown",
		"search.phrase":         true,
		"search.fuzziness":      "other",
		"search.prefix_terms":   true,
		"search.prefix_present": true,
	} {
		if got := rec[key]; got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
	if strings.Contains(buf.String(), req.Msg.GetQuery()) || strings.Contains(buf.String(), req.Msg.GetPrefix()) {
		t.Fatalf("slow search log leaked query or prefix: %s", buf.String())
	}
}

func TestLoggingInterceptor_SearchSpanAttributesAreBounded(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := trace.NewTracerProvider(trace.WithSpanProcessor(recorder))
	ctx, span := provider.Tracer("test").Start(context.Background(), "SearchVertices")
	next := connect.UnaryFunc(func(_ context.Context, _ connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.SearchVerticesResponse{}), nil
	})
	req := connect.NewRequest(&pb.SearchVerticesRequest{
		Query:  "private-query-value",
		Prefix: "private-prefix-value",
		Options: &pb.SearchOptions{
			MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD,
			Fuzziness: 2,
		},
	})
	if _, err := NewLoggingInterceptor(nil).ConnectInterceptor()(next)(ctx, req); err != nil {
		t.Fatalf("interceptor: %v", err)
	}
	span.End()
	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attrs := map[string]any{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	for key, want := range map[string]any{
		"lantern.search.mode":           "min_should",
		"lantern.search.phrase":         false,
		"lantern.search.fuzziness":      "2",
		"lantern.search.prefix_terms":   false,
		"lantern.search.prefix_present": true,
	} {
		if got := attrs[key]; got != want {
			t.Errorf("%s = %#v, want %#v", key, got, want)
		}
	}
	for _, attr := range ended[0].Attributes() {
		value := attr.Value.Emit()
		if strings.Contains(value, req.Msg.GetQuery()) || strings.Contains(value, req.Msg.GetPrefix()) {
			t.Fatalf("search span leaked query or prefix in %s=%q", attr.Key, value)
		}
	}
}
