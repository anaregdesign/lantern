package main

import (
	"context"
	"net"
	"net/http"
	"net/http/httptrace"
	"sync"
	"time"
)

// A nil point means that the hook was not observed. Counts and first/last
// offsets (counts saturate at 65535) summarize concurrent retries/redirects; they are not a
// connection history or a pairing of individual dial attempts.
type runtimeHTTPTracePoint struct {
	Count   uint16 `json:"count"`
	FirstMS int64  `json:"first_ms"`
	LastMS  int64  `json:"last_ms"`
}

type runtimeHTTPTraceDiagnostic struct {
	GetConn      *runtimeHTTPTracePoint `json:"get_conn,omitempty"`
	DialEnter    *runtimeHTTPTracePoint `json:"dial_enter,omitempty"`
	DialReturn   *runtimeHTTPTracePoint `json:"dial_return,omitempty"`
	DNSStart     *runtimeHTTPTracePoint `json:"dns_start,omitempty"`
	DNSDone      *runtimeHTTPTracePoint `json:"dns_done,omitempty"`
	GotConn      *runtimeHTTPTracePoint `json:"got_conn,omitempty"`
	ConnectStart *runtimeHTTPTracePoint `json:"connect_start,omitempty"`
	ConnectDone  *runtimeHTTPTracePoint `json:"connect_done,omitempty"`
	// WroteRequest is the standard hook, not proof of socket flush or peer
	// receipt. FirstResponseByte is not complete/final response headers.
	WroteRequest      *runtimeHTTPTracePoint `json:"wrote_request,omitempty"`
	FirstResponseByte *runtimeHTTPTracePoint `json:"first_response_byte,omitempty"`
	LastConnReused    *bool                  `json:"last_conn_reused,omitempty"`
	LastDNSCoalesced  *bool                  `json:"last_dns_coalesced,omitempty"`
	LastDialResult    string                 `json:"last_dial_result,omitempty"`
	LastDNSResult     string                 `json:"last_dns_result,omitempty"`
	LastConnectResult string                 `json:"last_connect_result,omitempty"`
	LastWriteResult   string                 `json:"last_write_result,omitempty"`
}

type runtimeHTTPTrace struct {
	mu        sync.Mutex
	started   time.Time
	sealed    bool
	points    [tracePointCount]runtimeHTTPTracePoint
	reused    bool
	coalesced bool
	dial      string
	dns       string
	connect   string
	write     string
}

const (
	traceGetConn = iota
	traceDialEnter
	traceDialReturn
	traceDNSStart
	traceDNSDone
	traceGotConn
	traceConnectStart
	traceConnectDone
	traceWroteRequest
	traceFirstByte
	tracePointCount
)

type runtimeHTTPTraceContextKey struct{}

// Observe only the clone's existing dial function. Preserve nil fallback and
// forward the same context/network/address without changing dial ownership.
// A Transport-owned dial can outlive Do or serve another waiting request:
// these points describe the originating request's observed prefix, not a
// complete connection history. The request's trace ignores hooks after finish.
func observeRuntimeMetricsDial(transport *http.Transport) {
	dial := transport.DialContext
	if dial == nil {
		return
	}
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		trace, _ := ctx.Value(runtimeHTTPTraceContextKey{}).(*runtimeHTTPTrace)
		if trace != nil {
			trace.record(traceDialEnter, nil, false)
		}
		conn, err := dial(ctx, network, address)
		if trace != nil {
			trace.record(traceDialReturn, err, false)
		}
		return conn, err
	}
}

func (t *runtimeHTTPTrace) callbacks() *httptrace.ClientTrace {
	// Fresh callbacks for each request: WithClientTrace may compose them with
	// an existing caller trace. Never retain host/address/Conn/error strings.
	return &httptrace.ClientTrace{
		GetConn:              func(string) { t.record(traceGetConn, nil, false) },
		DNSStart:             func(httptrace.DNSStartInfo) { t.record(traceDNSStart, nil, false) },
		DNSDone:              func(info httptrace.DNSDoneInfo) { t.record(traceDNSDone, info.Err, info.Coalesced) },
		GotConn:              func(info httptrace.GotConnInfo) { t.record(traceGotConn, nil, info.Reused) },
		ConnectStart:         func(string, string) { t.record(traceConnectStart, nil, false) },
		ConnectDone:          func(_, _ string, err error) { t.record(traceConnectDone, err, false) },
		WroteRequest:         func(info httptrace.WroteRequestInfo) { t.record(traceWroteRequest, info.Err, false) },
		GotFirstResponseByte: func() { t.record(traceFirstByte, nil, false) },
	}
}

func (t *runtimeHTTPTrace) record(kind int, err error, flag bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.sealed {
		return
	}
	point := &t.points[kind]
	// Take time under the lock so concurrent callbacks cannot reverse offsets.
	elapsed := max(0, time.Since(t.started).Milliseconds())
	if point.Count == 0 {
		point.FirstMS = elapsed
	}
	if point.Count != ^uint16(0) {
		point.Count++
	}
	point.LastMS = elapsed
	switch kind {
	case traceGotConn:
		t.reused = flag
	case traceDialReturn, traceDNSDone, traceConnectDone, traceWroteRequest:
		result := "ok"
		if err != nil {
			result = "error"
		}
		switch kind {
		case traceDialReturn:
			t.dial = result
		case traceDNSDone:
			t.dns, t.coalesced = result, flag
		case traceConnectDone:
			t.connect = result
		case traceWroteRequest:
			t.write = result
		}
	}
}

func (t *runtimeHTTPTrace) finish() *runtimeHTTPTraceDiagnostic {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.sealed = true
	// Copy values; callbacks may outlive Do but cannot mutate the emitted record.
	point := func(kind int) *runtimeHTTPTracePoint {
		value := t.points[kind]
		if value.Count == 0 {
			return nil
		}
		return &value
	}
	result := &runtimeHTTPTraceDiagnostic{
		GetConn: point(traceGetConn), GotConn: point(traceGotConn),
		DialEnter: point(traceDialEnter), DialReturn: point(traceDialReturn),
		DNSStart: point(traceDNSStart), DNSDone: point(traceDNSDone),
		ConnectStart: point(traceConnectStart), ConnectDone: point(traceConnectDone),
		WroteRequest: point(traceWroteRequest), FirstResponseByte: point(traceFirstByte),
		LastDialResult: t.dial, LastDNSResult: t.dns,
		LastConnectResult: t.connect, LastWriteResult: t.write,
	}
	if result.GotConn != nil {
		reused := t.reused
		result.LastConnReused = &reused
	}
	if result.DNSDone != nil {
		coalesced := t.coalesced
		result.LastDNSCoalesced = &coalesced
	}
	return result
}
