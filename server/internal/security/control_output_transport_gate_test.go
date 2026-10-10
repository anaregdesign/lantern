package security

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

// Real Connect TLS framing; fake time/facts are explicit in this focused
// transport test. The genuine producer gate reuses the same production path.
func TestAuthorityOutputConnectUnaryAndStream(t *testing.T) {
	_, owners, ticks := authorityTestComposite(t)
	o := owners[1]
	ctx := s3aTestContext(t)
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	routes := []authorityOutputRoute{authorityOutputUnary("/private.Unary/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue]) (*connect.Response[wrapperspb.StringValue], error) {
		switch r.Msg.Value {
		case "oversize":
			return connect.NewResponse(wrapperspb.String(strings.Repeat("private-oversize", authorityOutputUnitBytes))), nil
		case "error":
			failure := connect.NewError(connect.CodePermissionDenied, errors.New(strings.Repeat("secret", authorityOutputUnitBytes)))
			failure.Meta().Set("Private-Error", strings.Repeat("secret", authorityOutputUnitBytes))
			detail, err := connect.NewErrorDetail(wrapperspb.String(strings.Repeat("secret", authorityOutputUnitBytes)))
			if err != nil {
				return nil, err
			}
			failure.AddDetail(detail)
			return nil, failure
		}
		result := connect.NewResponse(wrapperspb.String("immutable unary"))
		if r.Msg.Value == "header" {
			result.Header().Set("Private-Oversize", strings.Repeat("secret", authorityOutputHeaderBytes))
		}
		result.Header().Set("Set-Cookie", "protected-unary-token")
		return result, nil
	}), authorityOutputStreaming("/private.Stream/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue], stream *connect.ServerStream[wrapperspb.StringValue]) error {
		stream.ResponseHeader().Set("Set-Cookie", "protected-stream-token")
		if r.Msg.Value == "trailer" {
			stream.ResponseTrailer().Set("Private-Oversize", strings.Repeat("secret", authorityOutputUnitBytes))
			return nil
		}
		if r.Msg.Value == "header" {
			stream.ResponseHeader().Set("Private-Oversize", strings.Repeat("secret", authorityOutputUnitBytes))
			return nil
		}
		if r.Msg.Value == "error" {
			return connect.NewError(connect.CodePermissionDenied, errors.New(strings.Repeat("secret", authorityOutputUnitBytes)))
		}
		if err := stream.Send(wrapperspb.String("first immutable frame")); err != nil {
			return err
		}
		return stream.Send(wrapperspb.String("forbidden next frame"))
	})}
	private, err := o.newOutputServer(routes, func(*http.Request) (CurrentCredentialProducer, error) { return authorityFakeCredentialProducer{}, nil }, func(*http.Request) authorityOutputRequirement {
		return authorityOutputRequirement{action: SecurityManage}
	})
	if err != nil {
		t.Fatal(err)
	}
	var outputCalls sync.WaitGroup
	handler := private.Handler
	private.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		outputCalls.Add(1)
		defer outputCalls.Done()
		handler.ServeHTTP(w, r)
	})
	server := httptest.NewUnstartedServer(private.Handler)
	server.Config = private
	server.TLS = private.TLSConfig
	server.StartTLS()
	defer server.Close()
	unary := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Unary/Call")
	response, err := unary.CallUnary(ctx, connect.NewRequest(wrapperspb.String("ok")))
	if err != nil || response.Msg.Value != "immutable unary" || response.Header().Get("Set-Cookie") != "protected-unary-token" {
		t.Fatal("unary framing/headers", err)
	}
	if _, err := unary.CallUnary(ctx, connect.NewRequest(wrapperspb.String("oversize"))); connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatal("oversize unary", err)
	}
	for input, code := range map[string]connect.Code{"header": connect.CodeResourceExhausted, "error": connect.CodePermissionDenied} {
		_, err := unary.CallUnary(ctx, connect.NewRequest(wrapperspb.String(input)))
		var failure *connect.Error
		if !errors.As(err, &failure) || failure.Code() != code || len(failure.Message()) > 128 || len(failure.Details()) != 0 || failure.Meta().Get("Private-Error") != "" {
			t.Fatal("unbounded unary metadata/error", input, err)
		}
	}
	streamClient := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Stream/Call")
	for input, code := range map[string]connect.Code{"header": connect.CodeResourceExhausted, "trailer": connect.CodeResourceExhausted, "error": connect.CodePermissionDenied} {
		stream, err := streamClient.CallServerStream(ctx, connect.NewRequest(wrapperspb.String(input)))
		if err != nil {
			t.Fatal(err)
		}
		if stream.Receive() || connect.CodeOf(stream.Err()) != code || len(stream.Err().Error()) > 128 || stream.ResponseTrailer().Get("Private-Oversize") != "" {
			t.Fatal("unbounded stream metadata/error", input, stream.Err())
		}
		stream.Close()
	}
	var events atomic.Int32
	o.outputHooks.Store(&authorityOutputHooks{afterAuthorize: func() {
		if events.Add(1) == 2 {
			ticks[1].Add(uint64(20 * time.Second))
		}
	}})
	stream, err := streamClient.CallServerStream(ctx, connect.NewRequest(wrapperspb.String("go")))
	if err != nil {
		t.Fatal(err)
	}
	if !stream.Receive() || stream.Msg().Value != "first immutable frame" {
		t.Fatal("approved frame did not complete late", stream.Err())
	}
	if stream.Receive() || stream.Err() == nil {
		t.Fatal("next frame inherited stale authorization")
	}
	if events.Load() != 2 {
		t.Fatal("authorization event count", events.Load())
	}
	stream.Close()
	// A refused Write closes the socket before serveOutput's deferred credit
	// release. Client EOF/Close does not join that server-side cleanup. Every
	// request above has entered its handler, and no new requests are started.
	finished := make(chan struct{})
	go func() {
		outputCalls.Wait()
		close(finished)
	}()
	select {
	case <-finished:
	case <-ctx.Done():
		t.Fatal("output handlers did not finish", ctx.Err())
	}
	o.outputs.mu.Lock()
	defer o.outputs.mu.Unlock()
	if o.outputs.active != 0 {
		t.Fatal("synchronous producer retained output credit")
	}
}

// Real TCP/TLS backpressure: the reader deliberately leaves its response body
// unread. The instrumented owned socket records a pending native Write, without
// replacing it or manufacturing a transport completion.
func TestAuthorityOutputBlockedTLSReaderCloseOwnsCredits(t *testing.T) {
	_, owners, _ := authorityTestComposite(t)
	o := owners[1]
	ctx := s3aTestContext(t)
	if err := o.network.renewAuthority(ctx); err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	pending := &atomic.Int32{}
	route := authorityOutputStreaming("/private.Blocked/Call", func(ctx context.Context, r *connect.Request[wrapperspb.StringValue], stream *connect.ServerStream[wrapperspb.StringValue]) error {
		defer close(done)
		for range 128 {
			if err := stream.Send(wrapperspb.String(strings.Repeat("p", 128<<10))); err != nil {
				return err
			}
		}
		return nil
	})
	private, err := o.newOutputServer([]authorityOutputRoute{route}, func(*http.Request) (CurrentCredentialProducer, error) { return authorityFakeCredentialProducer{}, nil }, func(*http.Request) authorityOutputRequirement {
		return authorityOutputRequirement{action: SecurityManage}
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewUnstartedServer(private.Handler)
	server.Config = private
	server.TLS = private.TLSConfig
	server.Listener = authorityObservedListener{server.Listener, pending}
	server.StartTLS()
	defer server.Close()
	client := connect.NewClient[wrapperspb.StringValue, wrapperspb.StringValue](server.Client(), server.URL+"/private.Blocked/Call")
	stream, err := client.CallServerStream(ctx, connect.NewRequest(wrapperspb.String("go")))
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	deadline := time.Now().Add(3 * time.Second)
	stable := 0
	for stable < 10 {
		if pending.Load() > 0 {
			stable++
		} else {
			stable = 0
		}
		if time.Now().After(deadline) {
			t.Fatal("no native socket backpressure")
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case <-done:
		t.Fatal("unread stream completed before Close")
	default:
	}
	o.outputs.mu.Lock()
	active, charge := o.outputs.active, o.outputs.bytes
	o.outputs.mu.Unlock()
	if active != 1 || charge != authorityOutputRequestCharge+authorityOutputConnectionCharge {
		t.Fatal("blocked writer lost credit", active, charge)
	}
	if err := o.network.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Close did not join blocked encoder/socket")
	}
	if pending.Load() != 0 || o.outputs.active != 0 {
		t.Fatal("owned TLS write survived Close")
	}
}

type authorityObservedListener struct {
	net.Listener
	pending *atomic.Int32
}

func (l authorityObservedListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if tcp, ok := c.(*net.TCPConn); ok {
		if err := tcp.SetWriteBuffer(4096); err != nil {
			c.Close()
			return nil, err
		}
	}
	return &authorityObservedConn{c, l.pending}, nil
}

type authorityObservedConn struct {
	net.Conn
	pending *atomic.Int32
}

func (c *authorityObservedConn) Write(raw []byte) (int, error) {
	c.pending.Add(1)
	defer c.pending.Add(-1)
	return c.Conn.Write(raw)
}
