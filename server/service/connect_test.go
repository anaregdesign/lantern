package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/types/known/timestamppb"
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
	srv := httptest.NewUnstartedServer(mux)
	serverProtocols := new(http.Protocols)
	serverProtocols.SetHTTP1(true)
	serverProtocols.SetUnencryptedHTTP2(true)
	srv.Config.Protocols = serverProtocols
	srv.Start()
	t.Cleanup(srv.Close)

	clientProtocols := new(http.Protocols)
	clientProtocols.SetUnencryptedHTTP2(true)
	httpClient := &http.Client{
		Transport: &http.Transport{Protocols: clientProtocols},
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

func TestConnectAdapter_FaultedReceiptRuntimeFailsClosed(t *testing.T) {
	runtime, svc, replication := newActivatedReceiptService(t, 8)
	client := newConnectTestClient(t, svc, replication)
	ctx := context.Background()

	capability, err := client.GetReceiptCapability(
		ctx,
		connect.NewRequest(&pb.GetReceiptCapabilityRequest{}),
	)
	if err != nil || !capability.Msg.GetEnabled() {
		t.Fatalf("initial capability = %+v, %v", capability, err)
	}
	operationID, err := mutationreceipt.NewID(
		runtime.receipt.epoch,
		time.UnixMilli(int64(capability.Msg.GetServerNowUnixMs())).Add(-time.Second),
		[24]byte{0x5a},
	)
	if err != nil {
		t.Fatal(err)
	}
	receiptContext := &pb.MutationReceiptContext{
		OperationIds:  [][]byte{operationID.Bytes()},
		LogicalCallId: []byte{0x6b, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
		Endpoint:      capability.Msg.GetEndpoint(),
	}
	if _, err := client.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{
		Edge: &pb.Edge{
			Tail:       "faulted",
			Head:       "protected",
			Weight:     1,
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		},
	})); err != nil {
		t.Fatal(err)
	}

	svc.replicationCutMu.Lock()
	svc.markReceiptCommitFaultLocked()
	svc.replicationCutMu.Unlock()

	capability, err = client.GetReceiptCapability(
		ctx,
		connect.NewRequest(&pb.GetReceiptCapabilityRequest{}),
	)
	if err != nil || capability.Msg.GetEnabled() || capability.Msg.GetPolicy() != nil ||
		capability.Msg.GetEndpoint() != nil {
		t.Fatalf("faulted capability = %+v, %v", capability, err)
	}
	if _, err := client.GetReceiptStatus(ctx, connect.NewRequest(&pb.GetReceiptStatusRequest{
		OperationId: operationID.Bytes(),
	})); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("faulted status = %v, want Internal", err)
	}
	if _, err := client.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{
		Tail: "faulted", Head: "protected", ReceiptContext: receiptContext,
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("faulted receipt mutation = %v, want FailedPrecondition", err)
	}
	if _, _, ok := runtime.GraphCache().GetEdgeDetail("faulted", "protected"); !ok {
		t.Fatal("faulted receipt mutation changed the graph")
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

func TestUnaryGraphReadOptimistic_InvalidatesOverlappingPublication(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{})
	defer func() { _ = log.Close() }()
	svc := NewLanternService(newFakeBackend()).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	entered := make(chan struct{})
	release := make(chan struct{})
	defer func() {
		select {
		case <-release:
		default:
			close(release)
		}
	}()
	readDone := make(chan error, 1)
	go func() {
		_, err := unaryGraphReadOptimistic(context.Background(), connect.NewRequest(&pb.GetVerticesRequest{}), svc,
			func(context.Context, *pb.GetVerticesRequest) (*pb.GetVerticesResponse, error) {
				close(entered)
				<-release
				return &pb.GetVerticesResponse{}, nil
			})
		readDone <- err
	}()
	<-entered
	writerDone := make(chan struct{})
	go func() {
		svc.replicationCutMu.Lock()
		svc.replicationCutMu.Unlock()
		close(writerDone)
	}()
	select {
	case <-writerDone:
		// An optimistic read does not hold the write gate while it computes.
	case <-time.After(2 * time.Second):
		t.Fatal("publication was blocked by the long read")
	}
	close(release)
	select {
	case err := <-readDone:
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("overlapping read = %v, want retryable Unavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("long read did not finish")
	}
}

type blockingVertexReadBackend struct {
	Backend
	entered chan struct{}
	release chan struct{}
}

func (b *blockingVertexReadBackend) GetVertex(key string) (*pb.Vertex, bool) {
	select {
	case <-b.entered:
	default:
		close(b.entered)
		<-b.release
	}
	return b.Backend.GetVertex(key)
}

func TestConnectAdapter_GetVerticesBatchDoesNotBlockPublication(t *testing.T) {
	log := mutationlog.New(mutationlog.Options{})
	defer func() { _ = log.Close() }()
	backend := &blockingVertexReadBackend{
		Backend: newFakeBackend(), entered: make(chan struct{}), release: make(chan struct{}),
	}
	defer func() {
		select {
		case <-backend.release:
		default:
			close(backend.release)
		}
	}()
	svc := NewLanternService(backend).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
	handler := NewLanternServiceConnectHandler(svc)
	readDone := make(chan error, 1)
	go func() {
		_, err := handler.GetVertices(context.Background(), connect.NewRequest(&pb.GetVerticesRequest{Keys: []string{"a", "b"}}))
		readDone <- err
	}()
	<-backend.entered
	writerDone := make(chan struct{})
	go func() {
		svc.replicationCutMu.Lock()
		svc.replicationCutMu.Unlock()
		close(writerDone)
	}()
	select {
	case <-writerDone:
	case <-time.After(2 * time.Second):
		t.Fatal("plural read blocked publication")
	}
	close(backend.release)
	select {
	case err := <-readDone:
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("overlapping batch read = %v, want retryable Unavailable", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("plural read did not finish")
	}
}

type blockingPrefixCountBackend struct {
	Backend
	entered chan struct{}
	release chan struct{}
}

func (b *blockingPrefixCountBackend) CountByPrefix(prefix string) int {
	close(b.entered)
	<-b.release
	return b.Backend.CountByPrefix(prefix)
}

func TestConnectAdapter_PrefixCountDoesNotBlockPublication(t *testing.T) {
	cases := []struct {
		name string
		read func(graphv1connect.LanternServiceHandler) error
	}{
		{"CountVerticesByPrefix", func(h graphv1connect.LanternServiceHandler) error {
			_, err := h.CountVerticesByPrefix(context.Background(), connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "a"}))
			return err
		}},
		{"DeleteVerticesByPrefixDryRun", func(h graphv1connect.LanternServiceHandler) error {
			_, err := h.DeleteVerticesByPrefix(context.Background(), connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "a", DryRun: true}))
			return err
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			log := mutationlog.New(mutationlog.Options{})
			defer func() { _ = log.Close() }()
			backend := &blockingPrefixCountBackend{
				Backend: newFakeBackend(), entered: make(chan struct{}), release: make(chan struct{}),
			}
			defer func() {
				select {
				case <-backend.release:
				default:
					close(backend.release)
				}
			}()
			svc := NewLanternService(backend).WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
			handler := NewLanternServiceConnectHandler(svc)
			readDone := make(chan error, 1)
			go func() { readDone <- tc.read(handler) }()
			<-backend.entered
			writerDone := make(chan struct{})
			go func() {
				svc.replicationCutMu.Lock()
				svc.replicationCutMu.Unlock()
				close(writerDone)
			}()
			select {
			case <-writerDone:
			case <-time.After(2 * time.Second):
				t.Fatal("prefix count blocked publication")
			}
			close(backend.release)
			select {
			case err := <-readDone:
				if connect.CodeOf(err) != connect.CodeUnavailable {
					t.Fatalf("overlapping prefix count = %v, want retryable Unavailable", err)
				}
			case <-time.After(2 * time.Second):
				t.Fatal("prefix count did not finish")
			}
		})
	}
}

// BenchmarkConnectAdapter_GetEdgesPublicationCut isolates the read-side
// publication gate cost from the network and Connect framing overhead.
func BenchmarkConnectAdapter_GetEdgesPublicationCut(b *testing.B) {
	for _, replicated := range []bool{false, true} {
		name := "single-node"
		if replicated {
			name = "replicated"
		}
		for _, parallel := range []bool{false, true} {
			mode := "serial"
			if parallel {
				mode = "parallel"
			}
			b.Run(name+"/"+mode, func(b *testing.B) {
				cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Hour)
				cache.PutEdgeWithExpiration("bench/tail", "bench/head", 1, time.Now().Add(time.Hour))
				svc := NewLanternService(cache)
				if replicated {
					log := mutationlog.New(mutationlog.Options{})
					b.Cleanup(func() { _ = log.Close() })
					svc.WithReplication(log, hlc.New(hlc.NodeID{1}, hlc.Options{}), nil)
				}
				handler := NewLanternServiceConnectHandler(svc)
				req := connect.NewRequest(&pb.GetEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "bench/tail", Head: "bench/head"}}})
				ctx := context.Background()
				read := func() {
					if _, err := handler.GetEdges(ctx, req); err != nil {
						b.Error(err)
					}
				}
				b.ReportAllocs()
				b.ResetTimer()
				if parallel {
					b.RunParallel(func(pb *testing.PB) {
						for pb.Next() {
							read()
						}
					})
				} else {
					for i := 0; i < b.N; i++ {
						read()
					}
				}
			})
		}
	}
}
