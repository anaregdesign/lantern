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
