package integration_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
)

const testToken = "integration-s3cret"

// newAuthedServer stands up an in-process Lantern (data plane + replication
// service) with the #850 bearer-token interceptor armed, mirroring how the
// production listener mounts it.
func newAuthedServer(t *testing.T) (*connectTestServer, *graphcache.GraphCache[string, *pb.Vertex], *service.LanternService) {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	log := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	var nid hlc.NodeID
	copy(nid[:], "auth-node-000000")
	clock := hlc.New(nid, hlc.Options{})
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock)
	auth := provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{"stale-rotated-out", testToken}})
	srv := newConnectTestServer(t, svc, rep, auth)
	return srv, cache, svc
}

// TestAuth_SDKRoundTrip drives the #850 contract through the SDK: a
// tokenless client is rejected with Unauthenticated on unary AND the
// replication streams; WithAuthToken fixes both; rotation admits any
// configured token; failover inherits the option.
func TestAuth_SDKRoundTrip(t *testing.T) {
	srv, _, _ := newAuthedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	t.Run("tokenless unary rejected", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url)
		err := l.PutVertex(ctx, "k", "v", time.Minute)
		if !errors.Is(err, client.ErrUnauthenticated) && connectCode(err) != connect.CodeUnauthenticated {
			t.Fatalf("tokenless put: got %v, want Unauthenticated", err)
		}
	})

	t.Run("tokenless replication stream rejected", func(t *testing.T) {
		// The replication service must NOT ride the health exemption: a
		// tokenless Subscribe fails at first receive.
		raw := graphv1connect.NewLanternReplicationServiceClient(h2cClient(), srv.url)
		stream, err := raw.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
		if err == nil {
			if stream.Receive() {
				t.Fatal("tokenless Subscribe delivered a message")
			}
			err = stream.Err()
			_ = stream.Close()
		}
		if connect.CodeOf(err) != connect.CodeUnauthenticated {
			t.Fatalf("tokenless Subscribe: got %v (err=%v), want Unauthenticated", connect.CodeOf(err), err)
		}
	})

	t.Run("token accepted end to end", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url, client.WithAuthToken(testToken))
		if err := l.PutVertex(ctx, "authed", "v", time.Minute); err != nil {
			t.Fatalf("authed put: %v", err)
		}
		if _, err := l.GetVertex(ctx, "authed"); err != nil {
			t.Fatalf("authed get: %v", err)
		}
	})

	t.Run("rotation: stale-but-configured token accepted", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url, client.WithAuthToken("stale-rotated-out"))
		if err := l.PutVertex(ctx, "rotated", "v", time.Minute); err != nil {
			t.Fatalf("rotation token put: %v", err)
		}
	})

	t.Run("failover inherits the token", func(t *testing.T) {
		lf, err := client.NewLanternFailover([]string{srv.url},
			client.WithHTTPClient(h2cClient()), client.WithAuthToken(testToken))
		if err != nil {
			t.Fatalf("NewLanternFailover: %v", err)
		}
		t.Cleanup(func() { _ = lf.Close() })
		if err := lf.PutVertex(ctx, "via-failover", "v", time.Minute); err != nil {
			t.Fatalf("failover put: %v", err)
		}
	})
}

// TestAuth_PumpReplicatesAgainstAuthedPeer pins the peer-credential path:
// a pump configured with AuthToken replicates from an auth-enabled peer,
// while a tokenless pump cannot.
func TestAuth_PumpReplicatesAgainstAuthedPeer(t *testing.T) {
	srcSrv, _, srcSvc := newAuthedServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Local (destination) node — plain, no auth needed on its own surface.
	dstCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	dstLog := mutationlog.New(mutationlog.Options{Capacity: 1024, SubscriberBuffer: 1024})
	var dstID hlc.NodeID
	copy(dstID[:], "auth-node-dst000")
	dstClock := hlc.New(dstID, hlc.Options{})
	dstSvc := service.NewLanternService(dstCache).WithReplication(dstLog, dstClock, nil)

	p := replication.NewPump(replication.Config{
		NodeID:     dstID,
		Peers:      []string{srcSrv.url},
		BackoffMin: 20 * time.Millisecond,
		BackoffMax: 200 * time.Millisecond,
		HTTPClient: h2cClient(),
		AuthToken:  testToken,
	}, dstSvc, dstCache)
	pumpCtx, pumpCancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() { _ = p.Run(pumpCtx); close(done) }()
	t.Cleanup(func() {
		pumpCancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
		}
	})

	// Write on the source THROUGH its authed surface; the pump must carry
	// it to the destination.
	if _, err := srcSvc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "replicated", Value: &pb.Vertex_Nil{Nil: true},
	}}); err != nil {
		t.Fatalf("source put: %v", err)
	}
	if !waitForVertex(t, dstCache, "replicated", 5*time.Second) {
		t.Fatal("authed pump did not replicate the vertex")
	}
}

// connectCode unwraps the connect error code from an SDK error chain.
func connectCode(err error) connect.Code {
	var ce *connect.Error
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return 0
}
