package client_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
)

// stubLanternHandler is an in-test implementation of
// graphv1connect.LanternServiceHandler that owns just enough state to
// round-trip a couple of methods. Embedding the Unimplemented* base
// satisfies the rest of the interface, so the test does not need to
// touch any RPC it does not exercise.
//
// Why a stub instead of importing server/service: pulling
// server/service into sdks/go's test module would bloat the SDK
// dependency graph (server pulls in mutationlog, replication, otel,
// prometheus, etc.) just to exercise a happy-path round trip. The
// stub keeps sdks/go free of any server-only transitive imports.
type stubLanternHandler struct {
	graphv1connect.UnimplementedLanternServiceHandler

	mu    sync.Mutex
	store map[string]*pb.Vertex
}

func newStubLanternHandler() *stubLanternHandler {
	return &stubLanternHandler{store: map[string]*pb.Vertex{}}
}

func (h *stubLanternHandler) PutVertex(_ context.Context, req *connect.Request[pb.PutVertexRequest]) (*connect.Response[pb.PutVertexResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if req.Msg.Vertex == nil {
		return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("vertex is nil"))
	}
	h.store[req.Msg.Vertex.Key] = req.Msg.Vertex
	return connect.NewResponse(&pb.PutVertexResponse{}), nil
}

func (h *stubLanternHandler) PutVertices(_ context.Context, req *connect.Request[pb.PutVerticesRequest]) (*connect.Response[pb.PutVerticesResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, v := range req.Msg.Vertices {
		if v == nil {
			continue
		}
		h.store[v.Key] = v
	}
	return connect.NewResponse(&pb.PutVerticesResponse{}), nil
}

func (h *stubLanternHandler) GetVertex(_ context.Context, req *connect.Request[pb.GetVertexRequest]) (*connect.Response[pb.GetVertexResponse], error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	v, ok := h.store[req.Msg.Key]
	if !ok {
		return nil, connect.NewError(connect.CodeNotFound, errors.New("not found"))
	}
	return connect.NewResponse(&pb.GetVertexResponse{Vertex: v}), nil
}

func (h *stubLanternHandler) GetServerStatus(_ context.Context, _ *connect.Request[pb.GetServerStatusRequest]) (*connect.Response[pb.GetServerStatusResponse], error) {
	return connect.NewResponse(&pb.GetServerStatusResponse{Version: "test"}), nil
}

// newConnectSDKServer stands up an in-process httptest server speaking
// h2c that mounts the supplied stub handler. The returned URL is the
// baseURL ready to feed into NewLanternConnect; teardown is registered
// via t.Cleanup.
func newConnectSDKServer(t *testing.T, h graphv1connect.LanternServiceHandler) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternServiceHandler(h))
	srv := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(srv.Close)
	return srv
}

// TestNewLanternConnect_PutAndGetRoundTrip is the smoke test for the
// additive Connect SDK constructor (#338): a real PutVertex / GetVertex
// pair travels from the SDK's connectClientAdapter, through h2c, into
// the stub handler, back out, and is verified at the SDK boundary.
//
// If any of the 21 connectClientAdapter forwarders is wired wrong this
// test fails — the round trip explicitly exercises PutVertices (used by
// the PutVertex SDK helper under the hood, via runBatchWrite) and
// GetVertex (direct one-line forwarder), which together cover the two
// most common forwarder patterns.
func TestNewLanternConnect_PutAndGetRoundTrip(t *testing.T) {
	srv := newConnectSDKServer(t, newStubLanternHandler())
	c, err := client.NewLanternConnect(srv.URL)
	if err != nil {
		t.Fatalf("NewLanternConnect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	ctx := context.Background()
	const key = "users/42"
	const value = "hello-connect-sdk"

	if err := c.PutVertex(ctx, key, value, time.Hour); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}
	got, err := c.GetVertex(ctx, key)
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	if got == nil {
		t.Fatal("GetVertex: nil vertex")
	}
	if got.Key != key {
		t.Errorf("Key = %q, want %q", got.Key, key)
	}
	if s, _ := client.StringValue(got); s != value {
		t.Errorf("StringValue = %q, want %q", s, value)
	}
}

// TestNewLanternConnect_GetServerStatus exercises a second forwarder
// (GetServerStatus) so a copy-paste typo in any of the 21 connect
// adapter methods does not slip past the GetVertex test alone.
func TestNewLanternConnect_GetServerStatus(t *testing.T) {
	srv := newConnectSDKServer(t, newStubLanternHandler())
	c, err := client.NewLanternConnect(srv.URL)
	if err != nil {
		t.Fatalf("NewLanternConnect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	status, err := c.GetServerStatus(context.Background())
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}
	if status == nil || status.Version != "test" {
		t.Errorf("Version = %q, want \"test\"", status.GetVersion())
	}
}

// TestNewLanternConnect_NotFoundSurface verifies the Connect→gRPC error
// bridge: a Connect CodeNotFound error from the server must surface
// through wrapStatus (in client.go) as an SDK ErrNotFound that
// `errors.Is(err, client.ErrNotFound)` catches. This is what keeps
// existing consumer code working after the transport switch.
func TestNewLanternConnect_NotFoundSurface(t *testing.T) {
	srv := newConnectSDKServer(t, newStubLanternHandler())
	c, err := client.NewLanternConnect(srv.URL)
	if err != nil {
		t.Fatalf("NewLanternConnect: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })

	_, err = c.GetVertex(context.Background(), "definitely/missing")
	if err == nil {
		t.Fatal("GetVertex(missing): want error, got nil")
	}
	if !errors.Is(err, client.ErrNotFound) {
		t.Errorf("err not ErrNotFound: %v", err)
	}
}

// TestNewLanternConnect_BaseURLValidation covers the constructor's
// argument-shape guards: empty baseURL, missing scheme. These are the
// most common misuses for users coming from NewLantern (which accepts
// bare host:port) and the SDK should fail loudly rather than producing
// a *Lantern that 404s on every call.
func TestNewLanternConnect_BaseURLValidation(t *testing.T) {
	cases := []struct {
		name    string
		baseURL string
	}{
		{name: "empty", baseURL: ""},
		{name: "missing scheme", baseURL: "lantern:6381"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := client.NewLanternConnect(tc.baseURL); err == nil {
				t.Fatalf("NewLanternConnect(%q): want error, got nil", tc.baseURL)
			}
		})
	}
}

// TestPingConnect_OK verifies the metrics-server-based health probe
// returns nil for a 200 response.
func TestPingConnect_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/healthz" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}))
	t.Cleanup(srv.Close)

	if err := client.PingConnect(context.Background(), srv.Client(), srv.URL); err != nil {
		t.Fatalf("PingConnect OK: %v", err)
	}
}

// TestPingConnect_NotServing verifies a non-200 response surfaces as an
// error rather than silently passing.
func TestPingConnect_NotServing(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	t.Cleanup(srv.Close)

	if err := client.PingConnect(context.Background(), srv.Client(), srv.URL); err == nil {
		t.Fatal("PingConnect 503: want error, got nil")
	}
}
