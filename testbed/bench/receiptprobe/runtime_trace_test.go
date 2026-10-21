package main

import (
	"encoding/json"
	"net"
	"net/http/httptrace"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type runtimeTracePrivateError struct{ methodCalls atomic.Int32 }

func (e *runtimeTracePrivateError) Error() string {
	e.methodCalls.Add(1)
	return "SECRET_runtime_trace_error"
}

func (e *runtimeTracePrivateError) Timeout() bool {
	e.methodCalls.Add(1)
	return true
}

func (e *runtimeTracePrivateError) Temporary() bool {
	e.methodCalls.Add(1)
	return true
}

// The hooks receive a connection, but diagnostics must never inspect or use it.
type runtimeTracePrivateConn struct{}

func (*runtimeTracePrivateConn) Read([]byte) (int, error)  { panic("SECRET_conn_read") }
func (*runtimeTracePrivateConn) Write([]byte) (int, error) { panic("SECRET_conn_write") }
func (*runtimeTracePrivateConn) Close() error              { panic("SECRET_conn_close") }
func (*runtimeTracePrivateConn) LocalAddr() net.Addr       { panic("SECRET_conn_local") }
func (*runtimeTracePrivateConn) RemoteAddr() net.Addr      { panic("SECRET_conn_remote") }
func (*runtimeTracePrivateConn) SetDeadline(time.Time) error {
	panic("SECRET_conn_deadline")
}
func (*runtimeTracePrivateConn) SetReadDeadline(time.Time) error {
	panic("SECRET_conn_read_deadline")
}
func (*runtimeTracePrivateConn) SetWriteDeadline(time.Time) error {
	panic("SECRET_conn_write_deadline")
}

func invokeRuntimeTraceCallbacks(callbacks *httptrace.ClientTrace, err error, reused bool) {
	callbacks.GetConn("SECRET_runtime_trace_host:443")
	callbacks.GotConn(httptrace.GotConnInfo{
		Conn: &runtimeTracePrivateConn{}, Reused: reused, WasIdle: true, IdleTime: time.Hour,
	})
	callbacks.ConnectStart("SECRET_runtime_trace_network", "SECRET_runtime_trace_address")
	callbacks.ConnectDone("SECRET_runtime_trace_network", "SECRET_runtime_trace_address", err)
	callbacks.WroteRequest(httptrace.WroteRequestInfo{Err: err})
	callbacks.GotFirstResponseByte()
}

func runtimeTraceJSON(t *testing.T, value *runtimeHTTPTraceDiagnostic) []byte {
	t.Helper()
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func runtimeTracePoints(value *runtimeHTTPTraceDiagnostic) []*runtimeHTTPTracePoint {
	return []*runtimeHTTPTracePoint{
		value.GetConn, value.GotConn, value.ConnectStart,
		value.ConnectDone, value.WroteRequest, value.FirstResponseByte,
	}
}

func TestRuntimeHTTPTraceUnobservedAndLateCallbacksStayEmpty(t *testing.T) {
	trace := &runtimeHTTPTrace{started: time.Now()}
	callbacks := trace.callbacks()
	first := trace.finish()
	if raw := runtimeTraceJSON(t, first); string(raw) != "{}" {
		t.Fatalf("unobserved hooks invented trace points: %s", raw)
	}
	privateError := &runtimeTracePrivateError{}
	invokeRuntimeTraceCallbacks(callbacks, privateError, true)
	if raw := runtimeTraceJSON(t, first); string(raw) != "{}" {
		t.Fatalf("late callback changed emitted diagnostic: %s", raw)
	}
	if raw := runtimeTraceJSON(t, trace.finish()); string(raw) != "{}" {
		t.Fatalf("sealed trace admitted a late hook: %s", raw)
	}
	if privateError.methodCalls.Load() != 0 {
		t.Fatal("late callback inspected a private error")
	}
}

func TestRuntimeHTTPTraceRecordsOnlyBoundedSafeHookFacts(t *testing.T) {
	for _, tc := range []struct {
		name       string
		failed     bool
		reused     bool
		wantResult string
	}{
		{name: "new connection success", wantResult: "ok"},
		{name: "reused connection success", reused: true, wantResult: "ok"},
		{name: "private error", failed: true, reused: true, wantResult: "error"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			trace := &runtimeHTTPTrace{started: time.Now()}
			privateError := &runtimeTracePrivateError{}
			var hookError error
			if tc.failed {
				hookError = privateError
			}
			invokeRuntimeTraceCallbacks(trace.callbacks(), hookError, tc.reused)
			diagnostic := trace.finish()
			for index, point := range runtimeTracePoints(diagnostic) {
				if point == nil || point.Count != 1 || point.FirstMS < 0 || point.LastMS < point.FirstMS {
					t.Fatalf("hook %d has missing or invalid offsets/count: %+v", index, point)
				}
			}
			if diagnostic.LastConnReused == nil || *diagnostic.LastConnReused != tc.reused ||
				diagnostic.LastConnectResult != tc.wantResult || diagnostic.LastWriteResult != tc.wantResult {
				t.Fatalf("fixed hook metadata differs: %+v", diagnostic)
			}
			raw := runtimeTraceJSON(t, diagnostic)
			if len(raw) > 2048 || strings.Contains(string(raw), "SECRET") {
				t.Fatalf("trace leaked callback details or exceeded its schema bound: %s", raw)
			}
			if privateError.methodCalls.Load() != 0 {
				t.Fatal("trace called Error, Timeout or Temporary on a private error")
			}
		})
	}
}

func TestRuntimeHTTPTraceConcurrentCallbacksKeepCountsAndOffsets(t *testing.T) {
	const workers, iterations = 16, 128
	trace := &runtimeHTTPTrace{started: time.Now()}
	callbacks := trace.callbacks()
	start := make(chan struct{})
	var joined sync.WaitGroup
	for range workers {
		joined.Go(func() {
			<-start
			for range iterations {
				invokeRuntimeTraceCallbacks(callbacks, nil, true)
			}
		})
	}
	close(start)
	joined.Wait()
	diagnostic := trace.finish()
	for index, point := range runtimeTracePoints(diagnostic) {
		if point == nil || point.Count != workers*iterations || point.FirstMS < 0 || point.LastMS < point.FirstMS {
			t.Fatalf("concurrent hook %d lost updates or reversed offsets: %+v", index, point)
		}
	}
}

func TestRuntimeHTTPTraceConcurrentSealOwnsItsSnapshot(t *testing.T) {
	trace := &runtimeHTTPTrace{started: time.Now()}
	callbacks := trace.callbacks()
	invokeRuntimeTraceCallbacks(callbacks, nil, false)
	start := make(chan struct{})
	sealed := make(chan *runtimeHTTPTraceDiagnostic, 1)
	var joined sync.WaitGroup
	for range 8 {
		joined.Go(func() {
			<-start
			for range 256 {
				invokeRuntimeTraceCallbacks(callbacks, nil, true)
			}
		})
	}
	go func() {
		<-start
		sealed <- trace.finish()
	}()
	close(start)
	diagnostic := <-sealed
	before := string(runtimeTraceJSON(t, diagnostic))
	joined.Wait()
	invokeRuntimeTraceCallbacks(callbacks, &runtimeTracePrivateError{}, true)
	if after := string(runtimeTraceJSON(t, diagnostic)); after != before {
		t.Fatalf("callbacks mutated emitted trace: before=%s after=%s", before, after)
	}
	if after := string(runtimeTraceJSON(t, trace.finish())); after != before {
		t.Fatalf("sealed trace changed after callback join: before=%s after=%s", before, after)
	}
	// A consumer also owns its returned point/bool copies, not the sealed owner.
	diagnostic.GetConn.Count = 0
	*diagnostic.LastConnReused = !*diagnostic.LastConnReused
	if after := string(runtimeTraceJSON(t, trace.finish())); after != before {
		t.Fatalf("consumer changed the sealed trace through returned pointers: before=%s after=%s", before, after)
	}
}

func TestRuntimeHTTPTraceCountsSaturateWithinBoundedSchema(t *testing.T) {
	// The zero start exercises the largest elapsed offset duration without sleeps.
	trace := &runtimeHTTPTrace{started: time.Time{}}
	callbacks := trace.callbacks()
	for range (1 << 16) + 1 {
		invokeRuntimeTraceCallbacks(callbacks, nil, true)
	}
	diagnostic := trace.finish()
	for index, point := range runtimeTracePoints(diagnostic) {
		if point == nil || point.Count != ^uint16(0) || point.FirstMS < 0 || point.LastMS < point.FirstMS {
			t.Fatalf("hook %d wrapped its saturated count or offset: %+v", index, point)
		}
	}
	if raw := runtimeTraceJSON(t, diagnostic); len(raw) > 2048 || strings.Contains(string(raw), "SECRET") {
		t.Fatalf("fully populated saturated trace exceeds its fixed safe schema: %s", raw)
	}
}
