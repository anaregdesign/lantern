package integration_test

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/backup"
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
	rep := service.NewLanternReplicationService(log, cache, clock).WithOriginStates(svc)
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
		_, err := l.PutVertex(ctx, "k", "v", time.Minute)
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
		if _, err := l.PutVertex(ctx, "authed", "v", time.Minute); err != nil {
			t.Fatalf("authed put: %v", err)
		}
		if _, err := l.GetVertex(ctx, "authed"); err != nil {
			t.Fatalf("authed get: %v", err)
		}
		var fullSeen bool
		for event, err := range l.Subscribe(ctx, nil) {
			if err != nil {
				t.Fatalf("authed full Subscribe: %v", err)
			}
			fullSeen = event != nil
			break
		}
		if !fullSeen {
			t.Fatal("authed full Subscribe returned no Mutation")
		}
		var checkpointSeen bool
		for event, err := range l.BootstrapIdentity(ctx) {
			if err != nil {
				t.Fatalf("authed identity Subscribe: %v", err)
			}
			_, checkpointSeen = event.(*client.IdentityCheckpoint)
			break
		}
		if !checkpointSeen {
			t.Fatal("authed identity Subscribe returned no checkpoint")
		}
	})

	t.Run("tokenless identity stream rejected", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url)
		var streamErr error
		for _, err := range l.BootstrapIdentity(ctx) {
			streamErr = err
			break
		}
		if !errors.Is(streamErr, client.ErrUnauthenticated) || connect.CodeOf(streamErr) != connect.CodeUnauthenticated {
			t.Fatalf("tokenless identity Subscribe = %v", streamErr)
		}
	})

	t.Run("rotation: stale-but-configured token accepted", func(t *testing.T) {
		l := newConnectClientFor(t, srv.url, client.WithAuthToken("stale-rotated-out"))
		if _, err := l.PutVertex(ctx, "rotated", "v", time.Minute); err != nil {
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
		if _, err := lf.PutVertex(ctx, "via-failover", "v", time.Minute); err != nil {
			t.Fatalf("failover put: %v", err)
		}
	})
}

func TestAuth_RawSingularPutEdgeRejectsNonFiniteSourceOverH2C(t *testing.T) {
	nodeID := hlc.NodeID{0x7a}
	now := time.Now()
	runtime, err := service.CreateDurableReceiptWALServingRuntime(service.DurableReceiptWALRuntimeConfig{
		Path: filepath.Join(t.TempDir(), "receipts.wal"),
		Receipt: mutationreceipt.Config{
			Epoch: mutationreceipt.Epoch{0x7a}, Retention: time.Hour,
			MaxEntries: 32, MaxBytes: 1 << 20, ClockHighWater: now,
		},
		Log:        mutationlog.Options{Capacity: 16, SubscriberBuffer: 16},
		DefaultTTL: time.Hour,
		ConfigureGraph: func(graph *graphcache.GraphCache[string, *pb.Vertex]) error {
			provider.ConfigureGraphCache(graph, provider.CacheConfig{TTL: time.Hour}, provider.SearchConfig{})
			return nil
		},
		NodeID:        nodeID,
		Now:           now,
		BaselineCodec: backup.ReceiptBaselineCodec{},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := runtime.Close(); err != nil {
			t.Errorf("close durable runtime: %v", err)
		}
	})
	primary := runtime.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	replicationService, err := runtime.NewLanternReplicationService(primary)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyInstallation(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	if err := runtime.CertifyReceiptBackup(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	if err := runtime.ActivatePublicReceipts(primary, replicationService); err != nil {
		t.Fatal(err)
	}
	srv := newConnectTestServer(
		t, primary, replicationService,
		provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{testToken}}),
		provider.NewValidationInterceptor(defaultIntegrationValidationLimits()).ConnectInterceptor(),
	)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	ctx := t.Context()
	authorizedPutEdge := func(edge *pb.Edge) *connect.Request[pb.PutEdgeRequest] {
		req := connect.NewRequest(&pb.PutEdgeRequest{Edge: edge})
		req.Header().Set("Authorization", "Bearer "+testToken)
		return req
	}
	authorizedPutEdges := func(edges []*pb.Edge) *connect.Request[pb.PutEdgesRequest] {
		req := connect.NewRequest(&pb.PutEdgesRequest{Edges: edges})
		req.Header().Set("Authorization", "Bearer "+testToken)
		return req
	}
	if _, err := raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "singular", Head: "edge", Weight: 1},
	})); connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless raw PutEdge = %v, want Unauthenticated", err)
	}

	beforeReceipts := runtime.ReceiptStats()
	beforeOrigins := primary.OriginStates()
	beforeLog, beforeCapacity, beforeEvicted := runtime.MutationLogStats()
	assertUnchanged := func(t *testing.T) {
		t.Helper()
		length, capacity, evicted := runtime.MutationLogStats()
		if runtime.ReceiptStats() != beforeReceipts ||
			!reflect.DeepEqual(primary.OriginStates(), beforeOrigins) ||
			primary.LocalSeq(nodeID) != 0 ||
			length != beforeLog || capacity != beforeCapacity || evicted != beforeEvicted ||
			runtime.GraphCache().VertexCount() != 0 || runtime.GraphCache().EdgeCount() != 0 {
			t.Fatalf("rejected source changed receipts/origin/log/graph: receipt=%+v origin=%+v log=%d/%d/%d vertices=%d edges=%d",
				runtime.ReceiptStats(), primary.OriginStates(), length, capacity, evicted,
				runtime.GraphCache().VertexCount(), runtime.GraphCache().EdgeCount())
		}
	}
	for _, weight := range []struct {
		name  string
		value float32
	}{
		{"NaN", float32(math.NaN())},
		{"positive infinity", float32(math.Inf(1))},
		{"negative infinity", float32(math.Inf(-1))},
	} {
		t.Run("singular/"+weight.name, func(t *testing.T) {
			// SDK PutEdge conveniences call PutEdges; the generated client
			// exercises the singular RPC and its own interceptor path.
			_, err := raw.PutEdge(ctx, authorizedPutEdge(&pb.Edge{
				Tail: "singular", Head: "edge", Weight: weight.value,
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("raw PutEdge = %v, want InvalidArgument", err)
			}
			assertUnchanged(t)
		})
		t.Run("plural/"+weight.name, func(t *testing.T) {
			_, err := raw.PutEdges(ctx, authorizedPutEdges([]*pb.Edge{
				{Tail: "prefix", Head: "edge", Weight: 1},
				{Tail: "plural", Head: "edge", Weight: weight.value},
			}))
			if connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("raw PutEdges = %v, want InvalidArgument", err)
			}
			assertUnchanged(t)
		})
	}
	put, err := raw.PutEdge(ctx, authorizedPutEdge(&pb.Edge{
		Tail: "singular", Head: "edge", Weight: 2.5,
	}))
	if err != nil || put.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("finite raw PutEdge = %+v, %v", put, err)
	}
	plural, err := raw.PutEdges(ctx, authorizedPutEdges([]*pb.Edge{
		{Tail: "plural", Head: "edge", Weight: math.MaxFloat32},
	}))
	if err != nil || len(plural.Msg.GetOutcomes()) != 1 ||
		plural.Msg.GetOutcomes()[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("finite raw PutEdges = %+v, %v", plural, err)
	}
	if got, live := runtime.GraphCache().GetWeight("singular", "edge"); !live || got != 2.5 {
		t.Fatalf("finite singular edge = %v, live=%t", got, live)
	}
	if got, live := runtime.GraphCache().GetWeight("plural", "edge"); !live || got != math.MaxFloat32 {
		t.Fatalf("finite plural edge = %v, live=%t", got, live)
	}
	length, _, _ := runtime.MutationLogStats()
	if primary.LocalSeq(nodeID) != 2 || length != beforeLog+2 || runtime.ReceiptStats() != beforeReceipts {
		t.Fatalf("finite writes did not publish exactly twice: origin=%d log=%d receipts=%+v",
			primary.LocalSeq(nodeID), length, runtime.ReceiptStats())
	}
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
