package main

import (
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
	GotConn      *runtimeHTTPTracePoint `json:"got_conn,omitempty"`
	ConnectStart *runtimeHTTPTracePoint `json:"connect_start,omitempty"`
	ConnectDone  *runtimeHTTPTracePoint `json:"connect_done,omitempty"`
	// WroteRequest is the standard hook, not proof of socket flush or peer
	// receipt. FirstResponseByte is not complete/final response headers.
	WroteRequest      *runtimeHTTPTracePoint `json:"wrote_request,omitempty"`
	FirstResponseByte *runtimeHTTPTracePoint `json:"first_response_byte,omitempty"`
	LastConnReused    *bool                  `json:"last_conn_reused,omitempty"`
	LastConnectResult string                 `json:"last_connect_result,omitempty"`
	LastWriteResult   string                 `json:"last_write_result,omitempty"`
}

type runtimeHTTPTrace struct {
	mu      sync.Mutex
	started time.Time
	sealed  bool
	points  [6]runtimeHTTPTracePoint
	reused  bool
	connect string
	write   string
}

const (
	traceGetConn = iota
	traceGotConn
	traceConnectStart
	traceConnectDone
	traceWroteRequest
	traceFirstByte
)

func (t *runtimeHTTPTrace) callbacks() *httptrace.ClientTrace {
	// Fresh callbacks for each request: WithClientTrace may compose them with
	// an existing caller trace. Never retain host/address/Conn/error strings.
	return &httptrace.ClientTrace{
		GetConn:              func(string) { t.record(traceGetConn, nil, false) },
		GotConn:              func(info httptrace.GotConnInfo) { t.record(traceGotConn, nil, info.Reused) },
		ConnectStart:         func(string, string) { t.record(traceConnectStart, nil, false) },
		ConnectDone:          func(_, _ string, err error) { t.record(traceConnectDone, err, false) },
		WroteRequest:         func(info httptrace.WroteRequestInfo) { t.record(traceWroteRequest, info.Err, false) },
		GotFirstResponseByte: func() { t.record(traceFirstByte, nil, false) },
	}
}

func (t *runtimeHTTPTrace) record(kind int, err error, reused bool) {
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
		t.reused = reused
	case traceConnectDone, traceWroteRequest:
		result := "ok"
		if err != nil {
			result = "error"
		}
		if kind == traceConnectDone {
			t.connect = result
		} else {
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
		ConnectStart: point(traceConnectStart), ConnectDone: point(traceConnectDone),
		WroteRequest: point(traceWroteRequest), FirstResponseByte: point(traceFirstByte),
		LastConnectResult: t.connect, LastWriteResult: t.write,
	}
	if result.GotConn != nil {
		reused := t.reused
		result.LastConnReused = &reused
	}
	return result
}
