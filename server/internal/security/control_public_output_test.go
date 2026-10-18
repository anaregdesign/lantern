package security

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
	"errors"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func currentPublicOutputFixture(t *testing.T) (*CurrentAuthority, *CurrentOutput, *httptest.Server, *atomic.Uint64) {
	t.Helper()
	_, origins, ticks := authorityTestComposite(t)
	o := &CurrentAuthority{origin: origins[1]}
	if err := o.origin.network.renewAuthority(s3aTestContext(t)); err != nil {
		t.Fatal(err)
	}
	s := httptest.NewUnstartedServer(nil)
	s.EnableHTTP2 = true
	p, err := o.ConfigurePublicOutput(s.Config, CurrentOutputLimits{ReadBytes: 1 << 20, SendBytes: 1 << 20, ConcurrentStreams: 16})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Close)
	return o, p, s, ticks[1]
}

func TestCurrentPublicOutputCreditBeforeHandler(t *testing.T) {
	_, p, _, _ := currentPublicOutputFixture(t)
	var connections []*currentOutputConnection
	for range currentOutputConnections {
		left, right := net.Pipe()
		t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
		ctx := p.connContext(context.Background(), left)
		c, _ := ctx.Value(currentOutputConnectionKey{}).(*currentOutputConnection)
		if c == nil {
			t.Fatal("finite connection refused")
		}
		connections = append(connections, c)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	if p.connContext(context.Background(), left).Value(currentOutputConnectionKey{}) != nil {
		t.Fatal("connection credit bypass")
	}
	req := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	var requests []*currentOutputRequest
	for range currentOutputRequests {
		r, err := p.reserve(connections[0], req)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, r)
	}
	called := false
	h := p.Wrap(p.Handler(func() http.Handler {
		called = true
		return http.NotFoundHandler()
	}))
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Fatal("capacity did not refuse before factory")
			}
		}()
		ctx := context.WithValue(req.Context(), currentOutputConnectionKey{}, connections[1])
		h.ServeHTTP(httptest.NewRecorder(), req.WithContext(ctx))
	}()
	if called || p.active != currentOutputRequests || p.bytes > currentOutputPoolBytes {
		t.Fatal("handler entry preceded finite global reserve")
	}
	p.connState(connections[0].conn, http.StateClosed)
	if p.connections[connections[0].conn] == nil {
		t.Fatal("closed socket released entered encoder credit early")
	}
	for _, request := range requests {
		p.release(request)
	}
	if p.active != 0 || p.connections[connections[0].conn] != nil {
		t.Fatal("request/connection credit leaked")
	}
	if err := p.stop(); err != nil {
		t.Fatal(err)
	}
	if _, err := p.reserve(connections[1], req); err == nil {
		t.Fatal("owner Close accepted new request")
	}
}

func TestCurrentPublicOutputConfigurationIsFinite(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := &CurrentAuthority{origin: owners[1]}
	for _, limits := range []CurrentOutputLimits{{}, {ReadBytes: 1, SendBytes: 1}, {ReadBytes: -1, SendBytes: 1, ConcurrentStreams: 1}, {ReadBytes: 1, SendBytes: 65 << 20, ConcurrentStreams: 1}, {ReadBytes: 1, SendBytes: 1, ConcurrentStreams: 4097}} {
		if _, err := o.ConfigurePublicOutput(&http.Server{}, limits); err == nil {
			t.Fatal("unbounded output configuration")
		}
	}
	s := &http.Server{ConnState: func(net.Conn, http.ConnState) {}}
	if _, err := o.ConfigurePublicOutput(s, CurrentOutputLimits{1, 1, 1}); err == nil {
		t.Fatal("foreign connection owner accepted")
	}
}

func TestCurrentPublicOutputDefaultCapacityReleasesIdleConnection(t *testing.T) {
	// Exercise only the real configuration/accounting boundary. No authority
	// admission, network listener or external/native time source is constructed.
	network := &s3aOwner{kernel: &s2cParticipant{}}
	owner := &CurrentAuthority{origin: &authorityOriginOwner{network: network}}
	// These are provider.NewConfig's ordinary public-listener defaults.
	p, err := owner.ConfigurePublicOutput(&http.Server{}, CurrentOutputLimits{
		ReadBytes: 16 << 20, SendBytes: 16 << 20, ConcurrentStreams: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}
	if p.requestCharge != 438304768 || p.connectionCharge != 88162304 {
		t.Fatal("default output reservation changed; review the matrix topology")
	}
	connections := make([]*currentOutputConnection, 5)
	for i := range connections {
		left, right := net.Pipe()
		t.Cleanup(func() { _ = left.Close(); _ = right.Close() })
		ctx := p.connContext(context.Background(), left)
		connections[i], _ = ctx.Value(currentOutputConnectionKey{}).(*currentOutputConnection)
		if connections[i] == nil {
			t.Fatal("five connection reservations must fit without active requests")
		}
	}
	request := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	requests := make([]*currentOutputRequest, 0, 4)
	for _, connection := range connections[:3] {
		r, err := p.reserve(connection, request)
		if err != nil {
			t.Fatal("three default-size requests must fit with five connections", err)
		}
		requests = append(requests, r)
	}
	if p.active != 3 || len(p.connections) != 5 || p.bytes+p.requestCharge <= currentOutputPoolBytes || p.active >= currentOutputRequests || connections[3].active != 0 || connections[3].closed {
		t.Fatal("unit no longer isolates the pool-capacity predicate")
	}
	if r, err := p.reserve(connections[3], request); r != nil || !errors.Is(err, ErrControlReserve) {
		t.Fatal("fourth request bypassed the finite pool")
	}
	// Confirm the actual outer HTTP boundary aborts before entering the handler
	// or writing any response headers, matching the observed header-stage EOF.
	called := false
	destination := httptest.NewRecorder()
	func() {
		defer func() {
			if recover() != http.ErrAbortHandler {
				t.Fatal("pool refusal did not take the pre-header abort path")
			}
		}()
		ctx := context.WithValue(request.Context(), currentOutputConnectionKey{}, connections[3])
		p.Wrap(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { called = true })).ServeHTTP(destination, request.WithContext(ctx))
	}()
	if called || destination.Flushed || destination.Body.Len() != 0 || len(destination.Header()) != 0 || p.active != 3 {
		t.Fatal("capacity refusal entered output or leaked a request reservation")
	}
	t.Logf(`current_output_capacity:{"reason":"pool","connections":%d,"active_requests":%d,"reserved_bytes":%d,"requested_bytes":%d,"pool_bytes":%d}`,
		len(p.connections), p.active, p.bytes, p.requestCharge, currentOutputPoolBytes)

	// Releasing only the unused seed connection makes the same fourth request
	// admissible. Preserve all limits and the three already-active reservations.
	p.connState(connections[4].conn, http.StateClosed)
	fourth, err := p.reserve(connections[3], request)
	if err != nil {
		t.Fatal("four replay requests did not fit after seed connection release", err)
	}
	requests = append(requests, fourth)
	if p.active != 4 || len(p.connections) != 4 || p.bytes > currentOutputPoolBytes {
		t.Fatal("successful fourth reservation broke the unchanged pool bound")
	}
	t.Logf(`current_output_capacity:{"reason":"admitted_after_idle_close","connections":%d,"active_requests":%d,"reserved_bytes":%d,"pool_bytes":%d}`,
		len(p.connections), p.active, p.bytes, currentOutputPoolBytes)
	for _, r := range requests {
		p.release(r)
	}
	for _, c := range connections[:4] {
		p.connState(c.conn, http.StateClosed)
	}
	if p.active != 0 || p.bytes != 0 || len(p.connections) != 0 {
		t.Fatal("request or connection reservation leaked")
	}
}

func TestCurrentPublicOutputCloseJoinsBlockedSocketProducer(t *testing.T) {
	o, p, server, _ := currentPublicOutputFixture(t)
	entered := make(chan struct{})
	finished := make(chan struct{})
	server.Config.Handler = p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer close(finished)
		_, a, err := o.WithRequestCredential(r.Context(), authorityFakeCredentialProducer{})
		if err != nil || a.BindCurrentSelfOutput(r.Context()) != nil {
			return
		}
		close(entered)
		unit := bytes.Repeat([]byte("x"), 128<<10)
		for range 1024 {
			if _, err := w.Write(unit); err != nil {
				return
			}
		}
	}))
	server.Start()
	conn, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := io.WriteString(conn, "GET /blocked HTTP/1.1\r\nHost: current.test\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("producer did not enter")
	}
	// The peer deliberately never consumes response bytes. Close must release
	// the actual socket and join the entered producer without an arrival deadline.
	closed := make(chan error, 1)
	go func() { closed <- o.Close() }()
	select {
	case err := <-closed:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Close stranded socket producer")
	}
	select {
	case <-finished:
	default:
		t.Fatal("Close returned before producer joined")
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.active != 0 || !p.closed {
		t.Fatal("entered encoder credit survived Close", p.active)
	}
}

func TestCurrentPublicOutputDecodeRefusalReleasesCredit(t *testing.T) {
	o, p, _, _ := currentPublicOutputFixture(t)
	before, err := o.ExportFloors()
	if err != nil {
		t.Fatal(err)
	}
	left, right := net.Pipe()
	defer left.Close()
	defer right.Close()
	req := httptest.NewRequest(http.MethodPost, "/rpc", nil)
	req = req.WithContext(p.connContext(req.Context(), left))
	initial := p.bytes
	for _, name := range []string{"proto", "json", "json; charset=utf-8"} {
		raw := bytes.Repeat([]byte{0x0a, 0}, 65536)
		if name != "proto" {
			raw = []byte(`{"vertices":[` + strings.TrimSuffix(strings.Repeat(`{},`, 65536), ",") + `]}`)
		}
		message := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "unchanged"}}}
		h := p.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if p.active != 1 || p.bytes != initial+p.requestCharge || p.requestCharge != uint64(4*p.limits.ReadBytes+10*p.limits.SendBytes+1<<20)+currentDecodeCharge(p.limits.ReadBytes) {
				t.Fatal("decoder entered without complete credit")
			}
			err := (currentOutputCodec{name: name, read: p.limits.ReadBytes, send: p.limits.SendBytes}).Unmarshal(raw, message)
			if connect.CodeOf(err) != connect.CodeResourceExhausted {
				t.Fatal("no predecode refusal", err)
			}
			BindCurrentFailure(w)
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte("fixed public refusal"))
		}))
		h.ServeHTTP(httptest.NewRecorder(), req)
		if p.active != 0 || p.bytes != initial || len(message.Vertices) != 1 || message.Vertices[0].Key != "unchanged" {
			t.Fatal("refusal leaked credit or mutated destination")
		}
	}
	after, err := o.ExportFloors()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("decode refusal changed M/P/B floors", err)
	}
}
