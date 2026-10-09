package security

import (
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// Actual TLS/HTTP/Connect codecs and compression, with qualified time and
// credential facts from the existing test owner. This is output-seam evidence,
// not the separate production-constructor/native-time public acceptance.
func TestCurrentPublicOutputTLSProtocolMatrix(t *testing.T) {
	o, p, server, _ := currentPublicOutputFixture(t)
	const unaryPath = "/graph.v1.LanternSecurityService/GetCurrentPrincipal"
	const streamPath = "/graph.v1.LanternChangeService/WatchChanges"
	var events atomic.Uint64
	o.origin.outputHooks.Store(&authorityOutputHooks{record: func(authorityCurrentTime, uint64) { events.Add(1) }})
	bind := func(ctx context.Context) (context.Context, error) {
		ctx, a, err := o.WithRequestCredential(ctx, authorityFakeCredentialProducer{})
		if err != nil {
			return ctx, err
		}
		return ctx, a.BindCurrentSelfOutput(ctx)
	}
	mux := http.NewServeMux()
	mux.Handle(unaryPath, p.Handler(func() http.Handler {
		return connect.NewUnaryHandler(unaryPath, func(ctx context.Context, _ *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
			if _, err := bind(ctx); err != nil {
				return nil, connect.NewError(connect.CodeUnavailable, err)
			}
			response := connect.NewResponse(&pb.GetCurrentPrincipalResponse{Identity: &pb.SecurityIdentity{Issuer: "https://idp.example", Subject: strings.Repeat("compressible", 4096)}})
			response.Header().Set("X-Current-Header", "initial")
			response.Trailer().Set("X-Current-Trailer", "terminal")
			return response, nil
		}, p.Options(connect.WithCompressMinBytes(1))...)
	}))
	mux.Handle(streamPath, p.Handler(func() http.Handler {
		return connect.NewServerStreamHandler(streamPath, func(ctx context.Context, _ *connect.Request[pb.WatchChangesRequest], stream *connect.ServerStream[pb.WatchChangesResponse]) error {
			if _, err := bind(ctx); err != nil {
				return connect.NewError(connect.CodeUnavailable, err)
			}
			stream.ResponseHeader().Set("X-Current-Header", "initial")
			if err := stream.Send(&pb.WatchChangesResponse{}); err != nil {
				return err
			}
			stream.ResponseTrailer().Set("X-Current-Trailer", "terminal")
			return nil
		}, p.Options(connect.WithCompressMinBytes(1))...)
	}))
	mux.HandleFunc("/auth/return", func(w http.ResponseWriter, r *http.Request) {
		if _, err := bind(r.Context()); err != nil {
			t.Error(err)
			return
		}
		http.SetCookie(w, &http.Cookie{Name: "__Host-current-output", Value: "bounded-cookie", Path: "/", Secure: true, HttpOnly: true, SameSite: http.SameSiteStrictMode})
		http.Redirect(w, r, "/", http.StatusSeeOther)
	})
	server.Config.Handler = p.Wrap(mux)
	server.StartTLS()
	for _, h2 := range []bool{false, true} {
		transport := server.Client().Transport.(*http.Transport).Clone()
		if !h2 {
			transport.ForceAttemptHTTP2 = false
			transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
		}
		t.Cleanup(transport.CloseIdleConnections)
		client := &http.Client{Transport: transport, Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		redirect, err := client.Get(server.URL + "/auth/return")
		if err != nil {
			t.Fatal("TLS browser GET", err)
		}
		_ = redirect.Body.Close()
		if redirect.StatusCode != http.StatusSeeOther || redirect.Header.Get("Location") != "/" || len(redirect.Cookies()) != 1 || !redirect.Cookies()[0].Secure || redirect.Cookies()[0].Value != "bounded-cookie" {
			t.Fatal("immutable browser redirect/cookie metadata")
		}
		for _, protocol := range []string{"connect-proto", "connect-json", "grpc", "grpc-web"} {
			if !h2 && protocol == "grpc" {
				continue
			}
			name := "http1/" + protocol
			if h2 {
				name = "http2/" + protocol
			}
			t.Run(name, func(t *testing.T) {
				options := []connect.ClientOption{connect.WithSendCompression("gzip"), connect.WithCompressMinBytes(1)}
				switch protocol {
				case "connect-json":
					options = append(options, connect.WithProtoJSON())
				case "grpc":
					options = append(options, connect.WithGRPC())
				case "grpc-web":
					options = append(options, connect.WithGRPCWeb())
				}
				unary := connect.NewClient[pb.GetCurrentPrincipalRequest, pb.GetCurrentPrincipalResponse](client, server.URL+unaryPath, options...)
				response, err := unary.CallUnary(context.Background(), connect.NewRequest(&pb.GetCurrentPrincipalRequest{}))
				if err != nil {
					t.Fatal(err)
				}
				if len(response.Msg.GetIdentity().GetSubject()) != 4096*len("compressible") || response.Header().Get("X-Current-Header") != "initial" || response.Trailer().Get("X-Current-Trailer") != "terminal" {
					t.Fatal("unary bytes/header/trailer changed")
				}
				streamClient := connect.NewClient[pb.WatchChangesRequest, pb.WatchChangesResponse](client, server.URL+streamPath, options...)
				stream, err := streamClient.CallServerStream(context.Background(), connect.NewRequest(&pb.WatchChangesRequest{}))
				if err != nil {
					t.Fatal(err)
				}
				defer stream.Close()
				if !stream.Receive() || stream.Receive() || stream.Err() != nil || stream.ResponseTrailer().Get("X-Current-Trailer") != "terminal" {
					t.Fatal("stream/trailer failure", stream.Err())
				}
			})
		}
	}
	if events.Load() < 14 {
		t.Fatal("public writes bypassed native final events")
	}
}

func TestCurrentPublicOutputHTTP2RequestsCannotShareFinalEvent(t *testing.T) {
	o, p, server, ticks := currentPublicOutputFixture(t)
	const path = "/graph.v1.LanternSecurityService/GetCurrentPrincipal"
	entered, resume := make(chan struct{}), make(chan struct{})
	var paused atomic.Bool
	var resumeOnce sync.Once
	t.Cleanup(func() { resumeOnce.Do(func() { close(resume) }) })
	o.origin.outputHooks.Store(&authorityOutputHooks{afterAuthorize: func() {
		if paused.CompareAndSwap(false, true) {
			close(entered)
			<-resume
		}
	}})
	var mu sync.Mutex
	var recipients []*currentOutputRequest
	server.Config.Handler = p.Wrap(p.Handler(func() http.Handler {
		return connect.NewUnaryHandler(path, func(ctx context.Context, req *connect.Request[pb.GetCurrentPrincipalRequest]) (*connect.Response[pb.GetCurrentPrincipalResponse], error) {
			r, _ := currentRequest(ctx, o)
			mu.Lock()
			recipients = append(recipients, r)
			mu.Unlock()
			ctx, a, err := o.WithRequestCredential(ctx, authorityFakeCredentialProducer{expiry: 5 * time.Second})
			if err != nil || a.BindCurrentSelfOutput(ctx) != nil {
				return nil, connect.NewError(connect.CodeUnavailable, errors.New("current admission unavailable"))
			}
			return connect.NewResponse(&pb.GetCurrentPrincipalResponse{Identity: &pb.SecurityIdentity{Subject: req.Header().Get("X-Test-Request")}}), nil
		}, p.Options()...)
	}))
	server.StartTLS()
	client := connect.NewClient[pb.GetCurrentPrincipalRequest, pb.GetCurrentPrincipalResponse](server.Client(), server.URL+path)
	result := make(chan error, 1)
	go func() {
		req := connect.NewRequest(&pb.GetCurrentPrincipalRequest{})
		req.Header().Set("X-Test-Request", "first")
		response, err := client.CallUnary(context.Background(), req)
		if err == nil && response.Msg.GetIdentity().GetSubject() != "first" {
			err = errors.New("wrong recipient payload")
		}
		result <- err
	}()
	select {
	case <-entered:
	case <-time.After(5 * time.Second):
		t.Fatal("first final event not reached")
	}
	ticks.Add(uint64(20 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := client.CallUnary(ctx, connect.NewRequest(&pb.GetCurrentPrincipalRequest{})); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatal("second stream borrowed first stream's old authority", err)
	}
	resumeOnce.Do(func() { close(resume) })
	if err := <-result; err != nil {
		t.Fatal("exact unit authorized before expiry could not complete late", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(recipients) != 2 || recipients[0].connection != recipients[1].connection || recipients[0].sequence == recipients[1].sequence {
		t.Fatal("test did not cover two independent streams on one actual connection")
	}
}

func TestCurrentPublicOutputDecodeRefusalTLSProtocols(t *testing.T) {
	o, p, server, _ := currentPublicOutputFixture(t)
	const path = "/graph.v1.LanternService/PutVertices"
	var entered atomic.Int64
	server.Config.Handler = p.Wrap(p.Handler(func() http.Handler {
		return connect.NewUnaryHandler(path, func(ctx context.Context, req *connect.Request[pb.PutVerticesRequest]) (*connect.Response[pb.PutVerticesResponse], error) {
			entered.Add(1)
			_, a, err := o.WithRequestCredential(ctx, authorityFakeCredentialProducer{})
			if err != nil {
				return nil, err
			}
			if err = a.BindCurrentSelfOutput(ctx); err != nil {
				return nil, err
			}
			return connect.NewResponse(&pb.PutVerticesResponse{}), nil
		}, p.Options(connect.WithReadMaxBytes(p.limits.ReadBytes), connect.WithSendMaxBytes(p.limits.SendBytes))...)
	}))
	server.StartTLS()
	bad := &pb.PutVerticesRequest{Vertices: make([]*pb.Vertex, 65536)}
	for i := range bad.Vertices {
		bad.Vertices[i] = &pb.Vertex{}
	}
	before, err := o.ExportFloors()
	if err != nil {
		t.Fatal(err)
	}
	waitReleased := func() {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for {
			p.mu.Lock()
			active, held := p.active, p.bytes
			expected := uint64(len(p.connections)) * p.connectionCharge
			p.mu.Unlock()
			if active == 0 {
				if held != expected {
					t.Fatal("decode failure leaked credit", held, expected)
				}
				return
			}
			if time.Now().After(deadline) {
				t.Fatal("decode failure retained request credit")
			}
			time.Sleep(time.Millisecond)
		}
	}
	for _, h2 := range []bool{false, true} {
		transport := server.Client().Transport.(*http.Transport).Clone()
		if !h2 {
			transport.ForceAttemptHTTP2 = false
			transport.TLSNextProto = map[string]func(string, *tls.Conn) http.RoundTripper{}
			transport.TLSClientConfig.NextProtos = []string{"http/1.1"}
		}
		t.Cleanup(transport.CloseIdleConnections)
		client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
		for _, protocol := range []string{"connect-proto", "connect-json", "grpc", "grpc-web", "grpc-web-json"} {
			if !h2 && protocol == "grpc" {
				continue
			}
			label := "http1/" + protocol
			if h2 {
				label = "http2/" + protocol
			}
			t.Run(label, func(t *testing.T) {
				options := []connect.ClientOption{connect.WithSendCompression("gzip"), connect.WithCompressMinBytes(1)}
				switch protocol {
				case "connect-json":
					options = append(options, connect.WithProtoJSON())
				case "grpc":
					options = append(options, connect.WithGRPC())
				case "grpc-web":
					options = append(options, connect.WithGRPCWeb())
				case "grpc-web-json":
					options = append(options, connect.WithGRPCWeb(), connect.WithProtoJSON())
				}
				rpc := connect.NewClient[pb.PutVerticesRequest, pb.PutVerticesResponse](client, server.URL+path, options...)
				prior := entered.Load()
				if _, err := rpc.CallUnary(context.Background(), connect.NewRequest(bad)); err == nil {
					t.Fatal("dense request reached business handler")
				}
				waitReleased()
				if entered.Load() != prior {
					t.Fatal("predecode refusal called business handler")
				}
				// The actual current output boundary aborts an unauthenticated decode
				// error rather than publishing it with a fabricated authorization grant.
				// A fresh valid request still succeeds after all credit is released.
				if _, err := rpc.CallUnary(context.Background(), connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "orders:valid"}}})); err != nil {
					t.Fatal("valid request after refusal", err)
				}
				waitReleased()
				if entered.Load() != prior+1 {
					t.Fatal("unexpected invocation count")
				}
			})
		}
	}
	after, err := o.ExportFloors()
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("decoder rejection mutated authority", err)
	}
}
