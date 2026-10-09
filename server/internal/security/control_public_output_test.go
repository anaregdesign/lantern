package security

import (
	"bytes"
	"connectrpc.com/connect"
	"context"
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
