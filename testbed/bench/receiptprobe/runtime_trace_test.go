package main

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
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
	callbacks.DNSStart(httptrace.DNSStartInfo{Host: "SECRET_runtime_trace_dns"})
	callbacks.DNSDone(httptrace.DNSDoneInfo{Addrs: []net.IPAddr{{IP: net.IPv4(192, 0, 2, 1)}}, Err: err, Coalesced: reused})
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
		value.GetConn, value.DNSStart, value.DNSDone, value.GotConn, value.ConnectStart,
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
				diagnostic.LastDNSCoalesced == nil || *diagnostic.LastDNSCoalesced != tc.reused || diagnostic.LastDNSResult != tc.wantResult ||
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
	*diagnostic.LastDNSCoalesced = !*diagnostic.LastDNSCoalesced
	if after := string(runtimeTraceJSON(t, trace.finish())); after != before {
		t.Fatalf("consumer changed the sealed trace through returned pointers: before=%s after=%s", before, after)
	}
}

func TestRuntimeHTTPTraceGetConnOnlyDoesNotInventDialOrDNS(t *testing.T) {
	trace := &runtimeHTTPTrace{started: time.Now()}
	callbacks := trace.callbacks()
	callbacks.GetConn("SECRET_runtime_trace_host")
	before := string(runtimeTraceJSON(t, trace.finish()))
	if !strings.Contains(before, "get_conn") || strings.Contains(before, "dns") ||
		strings.Contains(before, "dial") || strings.Contains(before, "got_conn") || strings.Contains(before, "SECRET") {
		t.Fatalf("GetConn-only prefix invented later hooks: %s", before)
	}
	invokeRuntimeTraceCallbacks(callbacks, &runtimeTracePrivateError{}, true)
	if after := string(runtimeTraceJSON(t, trace.finish())); after != before {
		t.Fatalf("late DNS/connection hooks changed sealed prefix: %s", after)
	}
}

func TestRuntimeMetricsDialObserverPreservesExistingDial(t *testing.T) {
	t.Run("nil context dial preserves legacy fallback", func(t *testing.T) {
		var calls int
		transport := &http.Transport{Dial: func(network, address string) (net.Conn, error) { //nolint:staticcheck // SA1019: intentional legacy-Dial regression control.
			calls++
			if network != "SECRET_network" || address != "SECRET_address" {
				t.Fatal("legacy dial arguments changed")
			}
			return &runtimeTracePrivateConn{}, nil
		}}
		observeRuntimeMetricsDial(transport)
		if transport.DialContext != nil || transport.Dial == nil || calls != 0 { //nolint:staticcheck // SA1019: verify the observer preserves the legacy fallback.
			t.Fatal("observer replaced or invoked nil-context fallback")
		}
		if _, err := transport.Dial("SECRET_network", "SECRET_address"); err != nil || calls != 1 { //nolint:staticcheck // SA1019: verify the original legacy dialer still runs unchanged.
			t.Fatal("legacy dial no longer works unchanged")
		}
	})

	t.Run("context arguments connection and error identities are unchanged", func(t *testing.T) {
		trace := &runtimeHTTPTrace{started: time.Now()}
		ctx := context.WithValue(t.Context(), runtimeHTTPTraceContextKey{}, trace)
		privateError := &runtimeTracePrivateError{}
		privateConn := &runtimeTracePrivateConn{}
		var calls int
		transport := &http.Transport{DialContext: func(got context.Context, network, address string) (net.Conn, error) {
			calls++
			if got != ctx || network != "SECRET_network" || address != "SECRET_address" {
				t.Fatal("observer changed the original dial context/arguments")
			}
			return privateConn, privateError
		}}
		observeRuntimeMetricsDial(transport)
		conn, err := transport.DialContext(ctx, "SECRET_network", "SECRET_address")
		if calls != 1 || conn != privateConn || err != privateError || privateError.methodCalls.Load() != 0 {
			t.Fatal("observer changed the result or inspected private values")
		}
		value := trace.finish()
		if value.DialEnter == nil || value.DialEnter.Count != 1 || value.DialReturn == nil ||
			value.DialReturn.Count != 1 || value.LastDialResult != "error" || value.DNSStart != nil {
			t.Fatalf("unexpected dial boundary observation: %+v", value)
		}
		before := string(runtimeTraceJSON(t, value))
		_, _ = transport.DialContext(ctx, "SECRET_network", "SECRET_address")
		if after := string(runtimeTraceJSON(t, trace.finish())); after != before || strings.Contains(after, "SECRET") {
			t.Fatalf("late dial return changed or leaked sealed state: %s", after)
		}
	})

	t.Run("no trace forwards the original context", func(t *testing.T) {
		ctx := t.Context()
		var calls int
		transport := &http.Transport{DialContext: func(got context.Context, _, _ string) (net.Conn, error) {
			calls++
			if got != ctx {
				t.Fatal("untraced context changed")
			}
			return nil, nil
		}}
		observeRuntimeMetricsDial(transport)
		if _, err := transport.DialContext(ctx, "SECRET_network", "SECRET_address"); err != nil || calls != 1 {
			t.Fatal("untraced dial result changed")
		}
	})
}

func TestRuntimeMetricsDialObserverConcurrentCountsAndSeal(t *testing.T) {
	trace := &runtimeHTTPTrace{started: time.Time{}}
	ctx := context.WithValue(t.Context(), runtimeHTTPTraceContextKey{}, trace)
	transport := &http.Transport{DialContext: func(context.Context, string, string) (net.Conn, error) { return nil, nil }}
	observeRuntimeMetricsDial(transport)
	const workers, count = 8, 8193
	var joined sync.WaitGroup
	for range workers {
		joined.Go(func() {
			for range count {
				_, _ = transport.DialContext(ctx, "SECRET_network", "SECRET_address")
			}
		})
	}
	joined.Wait()
	value := trace.finish()
	for _, point := range []*runtimeHTTPTracePoint{value.DialEnter, value.DialReturn} {
		if point == nil || point.Count != ^uint16(0) || point.FirstMS < 0 || point.LastMS < point.FirstMS {
			t.Fatalf("concurrent dial counters did not saturate: %+v", value)
		}
	}
	if value.LastDialResult != "ok" {
		t.Fatal("successful dial was misclassified")
	}
	before := string(runtimeTraceJSON(t, value))
	value.DialReturn.Count = 0
	_, _ = transport.DialContext(ctx, "SECRET_network", "SECRET_address")
	if raw := string(runtimeTraceJSON(t, trace.finish())); raw != before || len(raw) > 2048 || strings.Contains(raw, "SECRET") {
		t.Fatalf("dial trace failed ownership, bounds or privacy: %s", raw)
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
