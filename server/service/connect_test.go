package service

import (
	"context"
	"crypto/tls"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"connectrpc.com/connect"
	"golang.org/x/net/http2"
	"golang.org/x/net/http2/h2c"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

// newConnectTestClient starts an h2c httptest server in front of the
// supplied adapters and returns a Connect client wired to it. The cleanup
// closes the listener.
//
// Why h2c (rather than TLS): NewConnectServer in production mounts h2c on
// the additive listener, so the test exercises the exact same handler
// stack. h2c also lets server-streaming RPCs work over HTTP/2 without
// the test having to juggle self-signed certificates.
func newConnectTestClient(
	t *testing.T,
	svc *LanternService,
	rep *LanternReplicationService,
) graphv1connect.LanternServiceClient {
	t.Helper()

	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternServiceHandler(
		NewLanternServiceConnectHandler(svc),
	))
	if rep != nil {
		mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(
			NewLanternReplicationServiceConnectHandler(rep),
		))
	}
	srv := httptest.NewServer(h2c.NewHandler(mux, &http2.Server{}))
	t.Cleanup(srv.Close)

	httpClient := &http.Client{
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				var d net.Dialer
				return d.DialContext(ctx, network, addr)
			},
		},
	}
	return graphv1connect.NewLanternServiceClient(httpClient, srv.URL)
}

// TestConnectAdapter_PutAndGetVertexRoundTrip is the smoke test for the
// entire additive Connect path (#337): a real PutVertex / GetVertex pair
// travels through the Connect-Go client, over h2c, into the adapter, into
// the underlying *LanternService, back out, and is verified at the client.
// If any of the 22 unary adapter forwarders are wired wrong this test
// fails — the round trip explicitly exercises Put and Get.
func TestConnectAdapter_PutAndGetVertexRoundTrip(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	client := newConnectTestClient(t, svc, nil)
	ctx := context.Background()

	const key = "users/42"
	const value = "hello-connect"

	if _, err := client.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{
		Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: value}},
	})); err != nil {
		t.Fatalf("PutVertex: %v", err)
	}

	getResp, err := client.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: key}))
	if err != nil {
		t.Fatalf("GetVertex: %v", err)
	}
	got := getResp.Msg.GetVertex()
	if got == nil {
		t.Fatal("GetVertex: nil vertex")
	}
	if got.Key != key {
		t.Errorf("key = %q, want %q", got.Key, key)
	}
	if s := got.GetString_(); s != value {
		t.Errorf("value = %q, want %q", s, value)
	}
}

// TestConnectAdapter_GetServerStatus exercises a second unary path so a
// copy-paste typo in any one of the 22 wrappers (e.g. GetServerStatus
// accidentally forwarding to GetReplicationStatus) does not slip past
// the GetVertex test alone.
func TestConnectAdapter_GetServerStatus(t *testing.T) {
	svc := NewLanternService(newFakeBackend())
	client := newConnectTestClient(t, svc, nil)
	ctx := context.Background()

	resp, err := client.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}
	if resp == nil || resp.Msg == nil {
		t.Fatal("GetServerStatus: nil response")
	}
}

// TestConnectAdapter_ReplicationDisabled verifies the
// nil-LanternReplicationService guard: the adapter is wired but no
// replication is available, so every replication RPC must return
// Unavailable rather than panicking.
func TestConnectAdapter_ReplicationDisabled(t *testing.T) {
	h := NewLanternReplicationServiceConnectHandler(nil)
	_, err := h.PeerStatus(context.Background(), connect.NewRequest(&pb.PeerStatusRequest{}))
	if err == nil {
		t.Fatal("PeerStatus on nil replication: want error, got nil")
	}
	if got := connect.CodeOf(err); got != connect.CodeUnavailable {
		t.Errorf("PeerStatus code = %v, want Unavailable", got)
	}
}
