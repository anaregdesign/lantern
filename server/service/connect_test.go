package service

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"os"
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

func TestBinaryProtobufContentType(t *testing.T) {
	for _, tc := range []struct {
		contentType string
		binary      bool
	}{
		{"application/connect+proto", true},
		{"application/grpc+proto", true},
		{"application/grpc-web+proto", true},
		{"application/grpc-web-text+proto", false},
		{"application/grpc-web-text", false},
		{"application/connect+json", false},
		{"application/grpc+json", false},
		{"invalid content type", false},
	} {
		t.Run(tc.contentType, func(t *testing.T) {
			if got := binaryProtobufContentType(tc.contentType); got != tc.binary {
				t.Fatalf("binaryProtobufContentType(%q) = %t, want %t",
					tc.contentType, got, tc.binary)
			}
		})
	}
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

func TestConnectAdapter_ReceiptAddPostSyncWALFaultRecoversAtomically(t *testing.T) {
	runtime, svc, replication := newActivatedReceiptService(t, 8)
	client := newConnectTestClient(t, svc, replication)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	capability, err := client.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || !capability.Msg.GetEnabled() {
		t.Fatalf("initial capability = %+v, %v", capability, err)
	}
	issued := time.UnixMilli(int64(capability.Msg.GetServerNowUnixMs())).Add(-time.Second)
	ids := make([][]byte, 2)
	for i := range ids {
		id, err := mutationreceipt.NewID(runtime.receipt.epoch, issued, [24]byte{0x71, byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		ids[i] = id.Bytes()
	}
	request := &pb.AddEdgesRequest{
		Edges: []*pb.Edge{
			{Tail: "tip-fault", Head: "first", Weight: 2, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
			{Tail: "tip-fault", Head: "second", Weight: 3, Expiration: timestamppb.New(time.Now().Add(time.Hour))},
		},
		ContribIds: [][]byte{
			bytes.Repeat([]byte{0x71}, len(graphcache.ContribID{})),
			bytes.Repeat([]byte{0x72}, len(graphcache.ContribID{})),
		},
		ReceiptContext: &pb.MutationReceiptContext{
			OperationIds:  ids,
			LogicalCallId: bytes.Repeat([]byte{0x71}, 16),
			Endpoint:      capability.Msg.GetEndpoint(),
		},
	}
	walPath := runtime.receipt.owner.lease.Path()
	before, err := os.Stat(walPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := runtime.receipt.owner.tip.Close(); err != nil {
		t.Fatalf("close tip journal before WAL append: %v", err)
	}
	if _, err := client.AddEdges(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeUnavailable {
		t.Fatalf("post-Sync tip failure = %v, want Unavailable", err)
	}
	after, err := os.Stat(walPath)
	if err != nil || after.Size() <= before.Size() {
		t.Fatalf("post-Sync fault did not leave a complete WAL frame: before=%d after=%v error=%v",
			before.Size(), after, err)
	}
	if runtime.ReceiptStats().Entries != 0 || svc.LocalSeq(runtime.clock.NodeID()) != 0 {
		t.Fatalf("fault published Store or origin: stats=%+v seq=%d",
			runtime.ReceiptStats(), svc.LocalSeq(runtime.clock.NodeID()))
	}
	if length, _, _ := runtime.MutationLogStats(); length != 0 {
		t.Fatalf("fault published in-memory log length %d, want 0", length)
	}
	for _, edge := range request.GetEdges() {
		if weight, live := runtime.graph.GetWeight(edge.GetTail(), edge.GetHead()); live {
			t.Fatalf("fault published graph edge (%q, %q) weight %v", edge.GetTail(), edge.GetHead(), weight)
		}
	}
	if _, err := client.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
		Tail: "tip-fault", Head: "first",
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("faulted graph read = %v, want fail-closed", err)
	}
	if _, err := client.GetReceiptStatuses(ctx, connect.NewRequest(&pb.GetReceiptStatusesRequest{
		OperationIds: ids,
	})); connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("faulted receipt status = %v, want fail-closed", err)
	}
	if _, err := client.AddEdges(ctx, connect.NewRequest(request)); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("same-endpoint retry during fault = %v, want FailedPrecondition", err)
	}
	disabled, err := client.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || disabled.Msg.GetEnabled() {
		t.Fatalf("faulted capability = %+v, %v, want disabled", disabled, err)
	}

	config := durableRuntimeTestConfig(walPath)
	config.Receipt.MaxEntries = 8
	if err := runtime.Close(); err != nil {
		t.Fatalf("close faulted runtime: %v", err)
	}
	reopened, err := OpenDurableReceiptWALServingRuntime(config)
	if err != nil {
		t.Fatalf("restart from synced frame and stale tip: %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	recovered := reopened.NewLanternService(nil).WithTombstoneTTL(time.Hour)
	recoveredReplication, err := reopened.NewLanternReplicationService(recovered)
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.CertifyInstallation(recovered, recoveredReplication); err != nil {
		t.Fatal(err)
	}
	if err := reopened.CertifyReceiptBackup(recovered, recoveredReplication); err != nil {
		t.Fatal(err)
	}
	if err := reopened.ActivatePublicReceipts(recovered, recoveredReplication); err != nil {
		t.Fatal(err)
	}
	recoveredClient := newConnectTestClient(t, recovered, recoveredReplication)
	recoveredCapability, err := recoveredClient.GetReceiptCapability(ctx, connect.NewRequest(&pb.GetReceiptCapabilityRequest{}))
	if err != nil || !recoveredCapability.Msg.GetEnabled() ||
		!bytes.Equal(recoveredCapability.Msg.GetEndpoint().GetNodeId(), capability.Msg.GetEndpoint().GetNodeId()) ||
		!bytes.Equal(recoveredCapability.Msg.GetEndpoint().GetGeneration(), capability.Msg.GetEndpoint().GetGeneration()) {
		t.Fatalf("recovered endpoint = %+v, %v, want original enabled endpoint", recoveredCapability, err)
	}
	statuses, err := recoveredClient.GetReceiptStatuses(ctx, connect.NewRequest(&pb.GetReceiptStatusesRequest{
		OperationIds: ids,
	}))
	if err != nil || len(statuses.Msg.GetStatuses()) != 2 {
		t.Fatalf("recovered receipt statuses = %+v, %v", statuses, err)
	}
	for i, want := range []float32{2, 3} {
		status := statuses.Msg.GetStatuses()[i]
		if status.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
			!bytes.Equal(status.GetOperationId(), ids[i]) ||
			status.GetReceipt().GetOriginalResult().GetAddEdgeEffectiveWeight() != want {
			t.Fatalf("recovered receipt[%d] = %+v, want confirmed weight %v", i, status, want)
		}
		edge := request.GetEdges()[i]
		got, err := recoveredClient.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
			Tail: edge.GetTail(), Head: edge.GetHead(),
		}))
		if err != nil || got.Msg.GetEdge().GetWeight() != want {
			t.Fatalf("recovered edge[%d] = %+v, %v, want weight %v", i, got, err, want)
		}
	}
	replay, err := recoveredClient.AddEdges(ctx, connect.NewRequest(request))
	if err != nil || replay.Msg.GetWritten() != 2 ||
		len(replay.Msg.GetEffectiveWeights()) != 2 ||
		replay.Msg.GetEffectiveWeights()[0] != 2 || replay.Msg.GetEffectiveWeights()[1] != 3 {
		t.Fatalf("replay after attested recovery = %+v, %v, want original [2 3]", replay, err)
	}
	if recovered.LocalSeq(reopened.clock.NodeID()) != 1 {
		t.Fatalf("duplicate Add appended origin seq %d, want 1", recovered.LocalSeq(reopened.clock.NodeID()))
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
