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

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
// NewGrpcServerOptions).
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

// TestSlowRPCInterceptor_UnaryFiresOnSlowHandler asserts a single warn
// line per slow unary RPC with the documented field schema.
func TestSlowRPCInterceptor_UnaryFiresOnSlowHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf, slog.LevelWarn)
	s := NewSlowRPCInterceptor(5*time.Millisecond, logger)

	handler := func(ctx context.Context, req any) (any, error) {
		time.Sleep(20 * time.Millisecond)
		return "ok", status.Error(codes.DeadlineExceeded, "boom")
	}
	info := &grpc.UnaryServerInfo{FullMethod: "/lantern.v1.Test/Slow"}

	_, err := s.UnaryServerInterceptor()(context.Background(), nil, info, handler)
	if status.Code(err) != codes.DeadlineExceeded {
		t.Fatalf("err code = %s, want DeadlineExceeded", status.Code(err))
	}

	recs := decodeRecords(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	rec := recs[0]
	if rec["msg"] != "slow rpc" {
		t.Errorf("msg = %v, want %q", rec["msg"], "slow rpc")
	}
	if rec["method"] != info.FullMethod {
		t.Errorf("method = %v, want %q", rec["method"], info.FullMethod)
	}
	if rec["code"] != codes.DeadlineExceeded.String() {
		t.Errorf("code = %v, want %s", rec["code"], codes.DeadlineExceeded)
	}
	if rec["threshold_ms"] != float64(5) {
		t.Errorf("threshold_ms = %v, want 5", rec["threshold_ms"])
	}
	if d, _ := rec["duration_ms"].(float64); d < 5 {
		t.Errorf("duration_ms = %v, want >= 5", rec["duration_ms"])
	}
}

// TestSlowRPCInterceptor_UnarySilentOnFastHandler asserts that handlers
// completing within the threshold emit no log records.
func TestSlowRPCInterceptor_UnarySilentOnFastHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf, slog.LevelWarn)
	s := NewSlowRPCInterceptor(500*time.Millisecond, logger)

	handler := func(ctx context.Context, req any) (any, error) { return "ok", nil }
	info := &grpc.UnaryServerInfo{FullMethod: "/lantern.v1.Test/Fast"}

	if _, err := s.UnaryServerInterceptor()(context.Background(), nil, info, handler); err != nil {
		t.Fatalf("handler: %v", err)
	}
	if buf.Len() != 0 {
		t.Errorf("unexpected log output: %s", buf.String())
	}
}

// fakeServerStream lets us drive StreamServerInterceptor without spinning
// up a real gRPC server.
type fakeServerStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f *fakeServerStream) Context() context.Context { return f.ctx }

// TestSlowRPCInterceptor_StreamFiresOnSlowHandler asserts the stream
// interceptor mirrors the unary fields and uses ServerStream.Context()
// for the log record.
func TestSlowRPCInterceptor_StreamFiresOnSlowHandler(t *testing.T) {
	var buf bytes.Buffer
	logger := newJSONLogger(&buf, slog.LevelWarn)
	s := NewSlowRPCInterceptor(5*time.Millisecond, logger)

	info := &grpc.StreamServerInfo{FullMethod: "/lantern.v1.Test/StreamSlow"}
	ss := &fakeServerStream{ctx: context.Background()}
	handler := func(srv any, ss grpc.ServerStream) error {
		time.Sleep(20 * time.Millisecond)
		return errors.New("stream boom")
	}

	err := s.StreamServerInterceptor()(nil, ss, info, handler)
	// errors.New() wraps as codes.Unknown.
	if status.Code(err) != codes.Unknown {
		t.Fatalf("err code = %s, want Unknown", status.Code(err))
	}

	recs := decodeRecords(t, &buf)
	if len(recs) != 1 {
		t.Fatalf("got %d log records, want 1", len(recs))
	}
	rec := recs[0]
	if rec["method"] != info.FullMethod {
		t.Errorf("method = %v, want %q", rec["method"], info.FullMethod)
	}
	if rec["code"] != codes.Unknown.String() {
		t.Errorf("code = %v, want %s", rec["code"], codes.Unknown)
	}
}
