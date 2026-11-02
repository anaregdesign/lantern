package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
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
// threshold/logger preconditions. Disabled owners remain installed.
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

func TestSlowRPCInterceptor_ValidatedUpdates(t *testing.T) {
	var buf bytes.Buffer
	s := NewSlowRPCInterceptor(-time.Millisecond, newJSONLogger(&buf, slog.LevelWarn))
	if s.Threshold() != 0 {
		t.Fatal("legacy negative startup threshold must disable emission")
	}
	const maximum = int64(1<<63-1) / int64(time.Millisecond)
	for _, value := range []int64{0, 5, maximum} {
		if err := s.SetThresholdMilliseconds(value); err != nil || s.Threshold() != time.Duration(value)*time.Millisecond {
			t.Fatalf("valid threshold %d: %v, %v", value, s.Threshold(), err)
		}
	}
	for _, value := range []int64{-1, maximum + 1} {
		before := s.Threshold()
		if err := s.SetThresholdMilliseconds(value); err == nil || s.Threshold() != before {
			t.Fatalf("invalid threshold %d changed policy: %v", value, err)
		}
	}
	for _, owner := range []*SlowRPCInterceptor{nil, NewSlowRPCInterceptor(0, nil)} {
		if err := owner.SetThresholdMilliseconds(1); err == nil {
			t.Fatal("an absent logging owner cannot report an applied threshold")
		}
	}
}

func TestSlowRPCInterceptorManagedGenerations(t *testing.T) {
	s := NewSlowRPCInterceptor(500*time.Millisecond, slog.New(slog.DiscardHandler))
	if got := s.Snapshot(); got != (SlowRPCPolicySnapshot{Threshold: 500 * time.Millisecond}) {
		t.Fatalf("bootstrap snapshot = %+v", got)
	}
	const maximum = int64(1<<63-1) / int64(time.Millisecond)
	for _, invalid := range []struct {
		milliseconds int64
		generation   uint64
	}{{-1, 1}, {maximum + 1, 1}, {5, 0}} {
		before := s.Snapshot()
		if err := s.ApplyManagedThresholdMilliseconds(invalid.milliseconds, invalid.generation); err == nil || s.Snapshot() != before {
			t.Fatal("invalid managed callback changed the installed tuple", err)
		}
	}
	for _, owner := range []*SlowRPCInterceptor{nil, NewSlowRPCInterceptor(0, nil)} {
		if err := owner.ApplyManagedThresholdMilliseconds(5, 1); err == nil {
			t.Fatal("missing real logger accepted managed policy")
		}
	}
	for _, next := range []struct {
		milliseconds int64
		generation   uint64
	}{{5, 7}, {5, 7}, {0, 8}, {maximum, ^uint64(0)}} {
		if err := s.ApplyManagedThresholdMilliseconds(next.milliseconds, next.generation); err != nil {
			t.Fatal(err)
		}
		want := SlowRPCPolicySnapshot{Threshold: time.Duration(next.milliseconds) * time.Millisecond, Generation: next.generation}
		if s.Snapshot() != want || s.Threshold() != want.Threshold || s.Enabled() != (want.Threshold > 0) {
			t.Fatal("effective managed snapshot is inconsistent")
		}
	}
	before := s.Snapshot()
	if s.ApplyManagedThresholdMilliseconds(5, 7) == nil || s.ApplyManagedThresholdMilliseconds(0, before.Generation) == nil {
		t.Fatal("stale or conflicting managed callback accepted")
	}
	if s.SetThresholdMilliseconds(0) == nil || s.Snapshot() != before {
		t.Fatal("bootstrap setter overrode managed ownership")
	}
	detached := s.Snapshot()
	detached.Generation = 0
	detached.Threshold = 0
	if detached == s.Snapshot() || s.Snapshot() != before {
		t.Fatal("snapshot leaked mutable ownership")
	}
}

func TestSlowRPCInterceptorManagedConcurrentAtomicTuple(t *testing.T) {
	s := NewSlowRPCInterceptor(0, slog.New(slog.DiscardHandler))
	var workers sync.WaitGroup
	for worker := range 8 {
		workers.Go(func() {
			for i := range 100 {
				generation := uint64(worker*100 + i + 1)
				// Competing older callbacks may legitimately be rejected.
				_ = s.ApplyManagedThresholdMilliseconds(int64(generation), generation)
				got := s.Snapshot()
				if got.Threshold != time.Duration(got.Generation)*time.Millisecond {
					t.Errorf("torn threshold/generation: %+v", got)
				}
			}
		})
	}
	workers.Wait()
	if got := s.Snapshot(); got != (SlowRPCPolicySnapshot{Threshold: 800 * time.Millisecond, Generation: 800}) {
		t.Fatalf("newest callback not retained: %+v", got)
	}
}

func TestSlowRPCInterceptorBootstrapCannotRaceManagedAdoption(t *testing.T) {
	s := NewSlowRPCInterceptor(0, slog.New(slog.DiscardHandler))
	var workers sync.WaitGroup
	workers.Go(func() {
		for range 1000 {
			_ = s.SetThresholdMilliseconds(0)
		}
	})
	if err := s.ApplyManagedThresholdMilliseconds(5, 1); err != nil {
		t.Fatal(err)
	}
	workers.Wait()
	if s.Snapshot() != (SlowRPCPolicySnapshot{Threshold: 5 * time.Millisecond, Generation: 1}) {
		t.Fatal("concurrent bootstrap callback lost managed adoption")
	}
}

func TestSlowRPCInterceptor_ResidentSnapshots(t *testing.T) {
	var buf bytes.Buffer
	s := NewSlowRPCInterceptor(0, newJSONLogger(&buf, slog.LevelWarn))
	var ticks atomic.Int64
	s.now = func() time.Time { return time.Unix(0, ticks.Add(int64(10*time.Millisecond))) }
	update := int64(5)
	wrapped := s.ConnectInterceptor()(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		if err := s.SetThresholdMilliseconds(update); err != nil {
			return nil, err
		}
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	call := func() {
		t.Helper()
		if _, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
			t.Fatal(err)
		}
	}
	// Enabling during a disabled request cannot start timing that request.
	call()
	if ticks.Load() != 0 || buf.Len() != 0 {
		t.Fatal("disabled request performed timing or emitted a log")
	}
	// Disabling during the next request does not change its captured threshold.
	update = 0
	call()
	records := decodeRecords(t, &buf)
	if len(records) != 1 || records[0]["threshold_ms"] != float64(5) {
		t.Fatalf("in-flight threshold was not retained: %v", records)
	}
	buf.Reset()
	before := ticks.Load()
	call()
	if ticks.Load() != before || buf.Len() != 0 {
		t.Fatal("later disabled request performed timing or emitted a log")
	}
}

func TestSlowRPCInterceptor_ConcurrentUpdates(t *testing.T) {
	s := NewSlowRPCInterceptor(time.Hour, slog.New(slog.DiscardHandler))
	wrapped := s.ConnectInterceptor()(func(context.Context, connect.AnyRequest) (connect.AnyResponse, error) {
		return connect.NewResponse(&pb.GetVertexResponse{}), nil
	})
	var group sync.WaitGroup
	for worker := range 8 {
		group.Go(func() {
			for range 100 {
				if worker%2 == 0 {
					if err := s.SetThresholdMilliseconds(int64(worker) * 1000); err != nil {
						t.Error(err)
					}
				} else if _, err := wrapped(context.Background(), connect.NewRequest(&pb.GetVertexRequest{Key: "k"})); err != nil {
					t.Error(err)
				}
			}
		})
	}
	group.Wait()
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
		// Cross a measurable clock tick before asserting the slow-path log.
		time.Sleep(time.Millisecond)
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
