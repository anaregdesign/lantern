package integration_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
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
	"github.com/anaregdesign/lantern/server/readiness"
	"github.com/anaregdesign/lantern/server/replication"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// pumpNode is a tiny harness that stands up LanternService +
// LanternReplicationService on an h2c httptest.Server reachable by
// host:port (so other nodes' Pump instances can dial it via
// graphv1connect's HTTP client). Mirrors the legacy bufconn-flavoured
// helper the pre-#363 suite used; the migration to Connect is
// observationally equivalent at the wire level.
type pumpNode struct {
	url    string // full http://host:port URL (used by pump dialer + replication client)
	server *connectTestServer
	cache  *graphcache.GraphCache[string, *pb.Vertex]
	clock  *hlc.Clock
	log    *mutationlog.Log
	svc    *service.LanternService
	sdk    *client.Lantern
	raw    graphv1connect.LanternServiceClient
	pump   *replication.Pump
	nodeID hlc.NodeID
}

// This covers the owned-client bridge in isolation. The complete operator
// mTLS/member/domain boundary is qualified by the separate peer-security suite.
func TestPeerPumpVerifiedTransportUsesOwnedClientAndCurrentEligibility(t *testing.T) {
	source := newPumpNode(t, hlc.NodeID{21})
	peer := service.NewLanternReplicationService(source.log, source.cache, source.clock).WithOriginStates(source.svc)
	_, handler := graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(peer))
	var calls atomic.Uint64
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		calls.Add(1)
		if req.Header.Get("Authorization") != "" || req.Header.Get("Cookie") != "" {
			t.Error("owned transport acquired public credentials")
		}
		handler.ServeHTTP(w, req)
	}))
	t.Cleanup(srv.Close)
	var eligible atomic.Bool
	eligible.Store(true)
	transport, err := replication.NewVerifiedPeerTransport(srv.Client(), func(origin string) bool { return eligible.Load() && origin == srv.URL })
	if err != nil {
		t.Fatal(err)
	}
	for _, origin := range []string{strings.Replace(srv.URL, "https://", "http://", 1), srv.URL + "/unknown", "https://unapproved.example:6391"} {
		if _, err := transport.BaseURL(origin); err == nil {
			t.Fatal("unapproved peer became eligible", origin)
		}
	}
	destination := newPumpNode(t, hlc.NodeID{22})
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	if _, err := source.svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "owned-peer:visible"}}); err != nil {
		t.Fatal(err)
	}
	newPump := func() *replication.Pump {
		return replication.NewPump(replication.Config{NodeID: destination.nodeID, Peers: []string{srv.URL}, PeerTransport: transport, BackoffMin: 10 * time.Millisecond, BackoffMax: 20 * time.Millisecond}, destination.svc, destination.cache)
	}
	run := func(pump *replication.Pump) (context.CancelFunc, <-chan error) {
		pumpCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- pump.Run(pumpCtx) }()
		return stop, done
	}
	stop, done := run(newPump())
	if !waitForVertex(t, destination.cache, "owned-peer:visible", 2*time.Second) {
		stop()
		<-done
		t.Fatal("owned verified TLS client did not replicate")
	}
	stop()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	before := calls.Load()
	if before == 0 {
		t.Fatal("real Connect peer was not called")
	}
	eligible.Store(false)
	if _, err := transport.BaseURL(srv.URL); err == nil {
		t.Fatal("removed member still eligible")
	}
	if _, err := source.svc.PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "owned-peer:after-removal"}}); err != nil {
		t.Fatal(err)
	}
	pump := newPump()
	stop, done = run(pump)
	defer func() {
		stop()
		if err := <-done; err != nil {
			t.Error(err)
		}
	}()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		state := pump.Snapshot()
		if len(state) == 1 && strings.Contains(state[0].LastError, "unapproved workload peer origin") {
			if calls.Load() != before {
				t.Fatal("removed member was dialed")
			}
			if _, live := destination.cache.GetVertex("owned-peer:after-removal"); live {
				t.Fatal("removed member replicated")
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("pump did not report current eligibility failure")
}

func newPumpNode(t *testing.T, nodeID hlc.NodeID) *pumpNode {
	return newPumpNodeWithSearch(t, nodeID, 1024, true)
}

func newPumpNodeWithSearch(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool) *pumpNode {
	return newPumpNodeWithWAL(t, nodeID, logCapacity, positions, nil)
}

func newPumpNodeWithWAL(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool, wal mutationlog.WAL) *pumpNode {
	return newPumpNodeWithWALAndMetrics(t, nodeID, logCapacity, positions, wal, nil)
}

func newPumpNodeWithWALAndMetrics(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool, wal mutationlog.WAL, metrics service.SubscribeMetrics) *pumpNode {
	return newPumpNodeWithToken(t, nodeID, logCapacity, positions, wal, metrics, "")
}

func newAuthedPumpNode(t *testing.T, nodeID hlc.NodeID, logCapacity int) *pumpNode {
	return newPumpNodeWithToken(t, nodeID, logCapacity, true, nil, nil, testToken)
}

func newPumpNodeWithToken(t *testing.T, nodeID hlc.NodeID, logCapacity int, positions bool, wal mutationlog.WAL, metrics service.SubscribeMetrics, token string) *pumpNode {
	t.Helper()
	log := mutationlog.New(mutationlog.Options{Capacity: logCapacity, SubscriberBuffer: 1024, WAL: wal})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(nodeID, hlc.Options{})
	limits := productionSearchLimits(true, positions)
	cache := newProductionSearchCache(time.Minute, true, positions, limits.AnalysisLimits)
	svc := service.NewLanternService(cache).
		WithSearchLimits(limits).
		WithTombstoneTTL(time.Hour).
		WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock).
		WithOriginStates(svc).
		WithSearchConfig(svc).
		WithMetrics(metrics)

	// Connect-on-h2c httptest.Server — same pattern as
	// newConnectTestServer but the URL form is what the pump
	// consumes directly (replication.peerBaseURL accepts both
	// "host:port" and "http://host:port" forms).
	var interceptors []connect.Interceptor
	var sdkOptions []client.Option
	if token != "" {
		interceptors = []connect.Interceptor{
			provider.NewAuthInterceptor(provider.AuthConfig{Tokens: []string{token}}),
			provider.NewValidationInterceptor(defaultIntegrationValidationLimits()).ConnectInterceptor(),
		}
		sdkOptions = append(sdkOptions, client.WithAuthToken(token))
	}
	srv := newConnectTestServer(t, svc, rep, interceptors...)

	return &pumpNode{
		url:    srv.url,
		server: srv,
		cache:  cache,
		clock:  clock,
		log:    log,
		svc:    svc,
		sdk:    newConnectClientFor(t, srv.url, sdkOptions...),
		raw:    graphv1connect.NewLanternServiceClient(h2cClient(), srv.url),
		nodeID: nodeID,
	}
}

func newCertifiedPumpNode(
	t *testing.T,
	nodeID hlc.NodeID,
	maxRecvBytes int,
	maxSendBytes int,
) *pumpNode {
	t.Helper()
	log := mutationlog.New(mutationlog.Options{Capacity: 16, SubscriberBuffer: 16})
	clock := hlc.New(nodeID, hlc.Options{})
	limits := productionSearchLimits(true, true)
	cache := newProductionSearchCache(time.Minute, true, true, limits.AnalysisLimits)
	runtime, err := service.NewGraphOnlyServingRuntime(cache, log, clock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = runtime.Close() })
	svc := runtime.NewLanternService(nil).
		WithSearchLimits(limits).
		WithTombstoneTTL(time.Hour)
	rep, err := runtime.NewLanternReplicationService(svc)
	if err != nil {
		t.Fatal(err)
	}
	rep.WithOriginStates(svc).WithSearchConfig(svc)
	if err := runtime.CertifyInstallationWithReplicationSendLimit(svc, rep, maxSendBytes); err != nil {
		t.Fatal(err)
	}
	validation := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	server := newConnectTestServerWithOptions(
		t,
		svc,
		rep,
		connect.WithReadMaxBytes(maxRecvBytes),
		connect.WithSendMaxBytes(maxSendBytes),
		connect.WithInterceptors(validation.ConnectInterceptor()),
	)
	return &pumpNode{
		url:    server.url,
		cache:  cache,
		clock:  clock,
		log:    log,
		svc:    svc,
		sdk:    newConnectClientFor(t, server.url),
		raw:    graphv1connect.NewLanternServiceClient(h2cClient(), server.url),
		nodeID: nodeID,
	}
}

func readFullMutationFrame(
	t *testing.T,
	ctx context.Context,
	url string,
	fromLocalSeq uint64,
) *pb.SubscribeResponse {
	t.Helper()
	stream, err := newReplicationRawClient(t, url).Subscribe(
		ctx,
		connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: fromLocalSeq}),
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = stream.Close() }()
	if !stream.Receive() {
		t.Fatalf("Subscribe(%d) receive: %v", fromLocalSeq, stream.Err())
	}
	return proto.Clone(stream.Msg()).(*pb.SubscribeResponse)
}

func TestPumpIsNotPinnedByRejectedUnstreamableLocalMutation(t *testing.T) {
	const maxRecvBytes = 8 << 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	largeRequest := &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{
		Key:   "too-large-to-replicate",
		Value: &pb.Vertex_Bytes{Bytes: make([]byte, 2048)},
	}}}
	if size := proto.Size(largeRequest); size >= maxRecvBytes {
		t.Fatalf("oversized regression request size = %d, want below receive cap %d", size, maxRecvBytes)
	}

	reference := newCertifiedPumpNode(t, hlc.NodeID{0x90}, maxRecvBytes, 0)
	if _, err := reference.raw.PutVertices(
		ctx,
		connect.NewRequest(proto.Clone(largeRequest).(*pb.PutVerticesRequest)),
	); err != nil {
		t.Fatalf("reference PutVertices: %v", err)
	}
	frameSize := proto.Size(readFullMutationFrame(t, ctx, reference.url, 1))

	exact := newCertifiedPumpNode(t, hlc.NodeID{0x91}, maxRecvBytes, frameSize)
	if _, err := exact.raw.PutVertices(
		ctx,
		connect.NewRequest(proto.Clone(largeRequest).(*pb.PutVerticesRequest)),
	); err != nil {
		t.Fatalf("exact-fit PutVertices at %d bytes: %v", frameSize, err)
	}
	if got := proto.Size(readFullMutationFrame(t, ctx, exact.url, 1)); got != frameSize {
		t.Fatalf("exact-fit streamed frame size = %d, want %d", got, frameSize)
	}

	sourceID := hlc.NodeID{0x92}
	source := newCertifiedPumpNode(t, sourceID, maxRecvBytes, frameSize-1)
	if _, err := source.raw.PutVertices(
		ctx,
		connect.NewRequest(proto.Clone(largeRequest).(*pb.PutVerticesRequest)),
	); connect.CodeOf(err) != connect.CodeResourceExhausted ||
		!strings.Contains(err.Error(), fmt.Sprintf("LANTERN_MAX_SEND_MSG_BYTES=%d", frameSize-1)) {
		t.Fatalf("unstreamable PutVertices = %v, want explicit ResourceExhausted", err)
	}
	if _, ok := source.cache.GetVertex("too-large-to-replicate"); ok {
		t.Fatal("rejected unstreamable mutation changed source graph")
	}
	if length := source.log.Len(); length != 0 {
		t.Fatalf("rejected unstreamable mutation retained %d log entries", length)
	}
	if seq := source.svc.LocalSeq(sourceID); seq != 0 {
		t.Fatalf("rejected unstreamable mutation advanced source seq to %d", seq)
	}

	if _, err := source.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{{Key: "streamable", Value: &pb.Vertex_String_{String_: "ok"}}},
	})); err != nil {
		t.Fatalf("streamable PutVertices: %v", err)
	}
	target := newPumpNode(t, hlc.NodeID{0x93})
	target.startPump(ctx, t, []string{source.url})
	// Raw cache visibility may precede relay-log and origin-cursor publication.
	// The source peer's event is recorded after ApplyMutation returns; it is a
	// completion fence, while LocalSeq below remains an independent assertion.
	deadline := time.Now().Add(5 * time.Second)
	var applied replication.PeerSnapshot
	for time.Now().Before(deadline) {
		for _, row := range target.pump.Snapshot() {
			if row.Address == source.url && !row.LastEventAt.IsZero() {
				applied = row
				break
			}
		}
		if !applied.LastEventAt.IsZero() {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if applied.LastEventAt.IsZero() {
		t.Fatalf("Pump did not complete the first accepted source frame: %+v", target.pump.Snapshot())
	}
	if applied.AppliedSeq != 1 {
		t.Fatalf("Pump applied source frame = %d, want 1", applied.AppliedSeq)
	}
	if vertex, ok := target.cache.GetVertex("streamable"); !ok || vertex.GetKey() != "streamable" || vertex.GetString_() != "ok" {
		t.Fatalf("Pump accepted source payload = %v, present %v", vertex, ok)
	}
	if _, ok := target.cache.GetVertex("too-large-to-replicate"); ok {
		t.Fatal("Pump observed rejected oversized mutation")
	}
	if seq := target.svc.LocalSeq(sourceID); seq != 1 {
		t.Fatalf("Pump source cursor = %d, want 1", seq)
	}
}

func TestFullSubscribeRejectsJSONBeforeStreamingAtBinarySendBoundary(t *testing.T) {
	const maxRecvBytes = 8 << 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	write := &pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "json-frame-boundary", Value: &pb.Vertex_Bytes{Bytes: make([]byte, 2048)},
	}}
	if size := proto.Size(write); size >= maxRecvBytes {
		t.Fatalf("PutVertex request size %d exceeds receive cap %d", size, maxRecvBytes)
	}

	reference := newCertifiedPumpNode(t, hlc.NodeID{0x94}, maxRecvBytes, 0)
	if _, err := reference.raw.PutVertex(ctx, connect.NewRequest(write)); err != nil {
		t.Fatal(err)
	}
	frame := readFullMutationFrame(t, ctx, reference.url, 1)
	binarySize := proto.Size(frame)
	json, err := protojson.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	if len(json) <= binarySize {
		t.Fatalf("JSON Subscribe size %d must exceed binary size %d", len(json), binarySize)
	}

	node := newCertifiedPumpNode(t, hlc.NodeID{0x95}, maxRecvBytes, binarySize)
	if _, err := node.raw.PutVertex(ctx, connect.NewRequest(write)); err != nil {
		t.Fatalf("exact-fit binary PutVertex: %v", err)
	}
	if got := proto.Size(readFullMutationFrame(t, ctx, node.url, 1)); got != binarySize {
		t.Fatalf("binary Subscribe size = %d, want %d", got, binarySize)
	}

	jsonClient := graphv1connect.NewLanternReplicationServiceClient(h2cClient(), node.url, connect.WithProtoJSON())
	stream, err := jsonClient.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err == nil {
		defer func() { _ = stream.Close() }()
		if stream.Receive() {
			t.Fatal("JSON full Subscribe streamed a mutation beyond its send cap")
		}
		err = stream.Err()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument || !strings.Contains(err.Error(), "binary protobuf") {
		t.Fatalf("JSON full Subscribe = %v, want explicit binary-only InvalidArgument", err)
	}

	identity, err := jsonClient.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true,
	}))
	if err != nil {
		t.Fatalf("JSON identity-only Subscribe: %v", err)
	}
	defer func() { _ = identity.Close() }()
	if !identity.Receive() || identity.Msg().GetCheckpoint() == nil {
		t.Fatalf("JSON identity-only checkpoint = (%v, %v)", identity.Msg(), identity.Err())
	}

	for _, tc := range []struct {
		name   string
		option connect.ClientOption
	}{
		{name: "gRPC", option: connect.WithGRPC()},
		{name: "gRPC-Web", option: connect.WithGRPCWeb()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			binaryClient := graphv1connect.NewLanternReplicationServiceClient(h2cClient(), node.url, tc.option)
			stream, err := binaryClient.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			if !stream.Receive() || proto.Size(stream.Msg()) != binarySize {
				t.Fatalf("binary full Subscribe frame = (%v, %v), want %d bytes", stream.Msg(), stream.Err(), binarySize)
			}
		})
	}

	t.Run("gRPC-Web text", func(t *testing.T) {
		body, err := proto.Marshal(&pb.SubscribeRequest{FromLocalSeq: 1})
		if err != nil {
			t.Fatal(err)
		}
		requestFrame := make([]byte, 5+len(body))
		binary.BigEndian.PutUint32(requestFrame[1:5], uint32(len(body)))
		copy(requestFrame[5:], body)
		request, err := http.NewRequestWithContext(ctx, http.MethodPost,
			node.url+graphv1connect.LanternReplicationServiceSubscribeProcedure,
			strings.NewReader(base64.StdEncoding.EncodeToString(requestFrame)))
		if err != nil {
			t.Fatal(err)
		}
		request.Header.Set("Content-Type", "application/grpc-web-text+proto")
		request.Header.Set("Accept", "application/grpc-web-text+proto")
		request.Header.Set("X-Grpc-Web", "1")
		response, err := h2cClient().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = response.Body.Close() }()
		if base64.StdEncoding.EncodedLen(5+binarySize) <= binarySize {
			t.Fatal("test requires encoded response larger than binary send cap")
		}
		if response.StatusCode != http.StatusUnsupportedMediaType {
			t.Fatalf("gRPC-Web text full Subscribe status %d, want unsupported media type before streaming",
				response.StatusCode)
		}
	})
}

func TestFollowerRelayStreamsExactBoundaryAcrossMultipleHops_RealConnectWire(t *testing.T) {
	const maxRecvBytes = 8 << 10
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	remoteOrigin := hlc.NodeID{0xA0}
	mutation := &pb.Mutation{
		Origin: remoteOrigin[:],
		Seq:    1,
		Hlc: &pb.HLCTimestamp{
			WallNs: time.Now().UnixNano(),
			NodeId: remoteOrigin[:],
		},
		Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
			PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{
				Key:   "multi-hop-exact-frame",
				Value: &pb.Vertex_Bytes{Bytes: make([]byte, 2048)},
			}},
		}},
	}
	frameSize := proto.Size(&pb.SubscribeResponse{
		Event: &pb.SubscribeResponse_Mutation{Mutation: mutation},
	})

	source := newCertifiedPumpNode(t, hlc.NodeID{0xA1}, maxRecvBytes, 0)
	if err := source.svc.ApplyMutation(ctx, proto.Clone(mutation).(*pb.Mutation)); err != nil {
		t.Fatalf("seed source relay frame: %v", err)
	}
	relay := newCertifiedPumpNode(t, hlc.NodeID{0xA2}, maxRecvBytes, frameSize)
	relay.startPump(ctx, t, []string{source.url})
	if !waitForVertex(t, relay.cache, "multi-hop-exact-frame", 5*time.Second) {
		t.Fatal("exact-fit follower did not accept source frame")
	}
	downstream := newCertifiedPumpNode(t, hlc.NodeID{0xA3}, maxRecvBytes, 0)
	downstream.startPump(ctx, t, []string{relay.url})
	if !waitForVertex(t, downstream.cache, "multi-hop-exact-frame", 5*time.Second) {
		t.Fatal("exact-fit follower could not relay the accepted frame")
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && downstream.svc.LocalSeq(remoteOrigin) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if seq := downstream.svc.LocalSeq(remoteOrigin); seq != 1 {
		t.Fatalf("downstream origin cursor = %d, want 1", seq)
	}

	tooSmall := newCertifiedPumpNode(t, hlc.NodeID{0xA4}, maxRecvBytes, frameSize-1)
	err := tooSmall.svc.ApplyMutation(ctx, proto.Clone(mutation).(*pb.Mutation))
	if connect.CodeOf(err) != connect.CodeResourceExhausted {
		t.Fatalf("one-byte-oversize follower apply = %v, want ResourceExhausted", err)
	}
	if _, ok := tooSmall.cache.GetVertex("multi-hop-exact-frame"); ok {
		t.Fatal("one-byte-oversize follower changed graph state")
	}
	if length := tooSmall.log.Len(); length != 0 {
		t.Fatalf("one-byte-oversize follower retained %d log entries", length)
	}
	if seq := tooSmall.svc.LocalSeq(remoteOrigin); seq != 0 {
		t.Fatalf("one-byte-oversize follower advanced origin seq to %d", seq)
	}
}

type retryRelayWAL struct {
	writes       atomic.Int32
	failed       chan struct{}
	retryStarted chan struct{}
	allowRetry   chan struct{}
}

type stickyRelayWAL struct {
	armed    atomic.Bool
	healed   atomic.Bool
	failures atomic.Int32
	faulted  chan struct{}
}

type oneShotLocalWAL struct {
	armed    atomic.Bool
	failures atomic.Int32
}

type blockedFailureWAL struct {
	armed   atomic.Bool
	entered chan struct{}
	release chan struct{}
}

func (w *blockedFailureWAL) Write(mutationlog.Entry) error {
	if w.armed.CompareAndSwap(true, false) {
		close(w.entered)
		<-w.release
		return errors.New("injected blocked WAL failure")
	}
	return nil
}

func (w *oneShotLocalWAL) Write(mutationlog.Entry) error {
	if w.armed.CompareAndSwap(true, false) {
		w.failures.Add(1)
		return errors.New("injected local WAL failure")
	}
	return nil
}

func (w *stickyRelayWAL) Write(mutationlog.Entry) error {
	if w.armed.Load() && !w.healed.Load() {
		if w.failures.Add(1) == 1 {
			close(w.faulted)
		}
		return errors.New("injected persistent relay WAL failure")
	}
	return nil
}

type signalSubscribeMetrics struct{ started chan struct{} }

func (m *signalSubscribeMetrics) OnSubscribeStarted() {
	select {
	case m.started <- struct{}{}:
	default:
	}
}
func (*signalSubscribeMetrics) OnSubscribeEnded()         {}
func (*signalSubscribeMetrics) OnSubscribeDropped(string) {}

func (w *retryRelayWAL) Write(mutationlog.Entry) error {
	switch w.writes.Add(1) {
	case 1:
		close(w.failed)
		return errors.New("injected relay WAL failure")
	case 2:
		close(w.retryStarted)
		<-w.allowRetry
	}
	return nil
}

// assertGraphReadsGapped exercises every ordinary graph-data read RPC over
// Connect/h2c. GetReplicationStatus deliberately remains available for
// diagnosis while the graph publication cut is faulted.
func assertGraphReadsGapped(t *testing.T, ctx context.Context, n *pumpNode) {
	t.Helper()
	checks := []struct {
		name string
		read func() error
	}{
		{"Illuminate", func() error {
			_, err := n.raw.Illuminate(ctx, connect.NewRequest(&pb.IlluminateRequest{Seed: "relay-warmup", Params: &pb.IlluminateRequest_Bfs{Bfs: &pb.BfsParams{Step: 1, FanOut: 1}}}))
			return err
		}},
		{"GetVertex", func() error {
			_, err := n.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "relay-warmup"}))
			return err
		}},
		{"GetVertices", func() error {
			_, err := n.raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{Keys: []string{"relay-warmup"}}))
			return err
		}},
		{"ScanVertices", func() error {
			_, err := n.raw.ScanVertices(ctx, connect.NewRequest(&pb.ScanVerticesRequest{Prefix: "relay"}))
			return err
		}},
		{"ScanVertexKeys", func() error {
			_, err := n.raw.ScanVertexKeys(ctx, connect.NewRequest(&pb.ScanVertexKeysRequest{Prefix: "relay"}))
			return err
		}},
		{"SearchVertices", func() error {
			_, err := n.raw.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "ready"}))
			return err
		}},
		{"CountVerticesByPrefix", func() error {
			_, err := n.raw.CountVerticesByPrefix(ctx, connect.NewRequest(&pb.CountVerticesByPrefixRequest{Prefix: "relay"}))
			return err
		}},
		{"TopVerticesByDegree", func() error {
			_, err := n.raw.TopVerticesByDegree(ctx, connect.NewRequest(&pb.TopVerticesByDegreeRequest{Prefix: "relay"}))
			return err
		}},
		{"GetEdge", func() error {
			_, err := n.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "fault-tail", Head: "fault-head"}))
			return err
		}},
		{"GetEdges", func() error {
			_, err := n.raw.GetEdges(ctx, connect.NewRequest(&pb.GetEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "fault-tail", Head: "fault-head"}}}))
			return err
		}},
		{"ScanEdges", func() error {
			_, err := n.raw.ScanEdges(ctx, connect.NewRequest(&pb.ScanEdgesRequest{TailPrefix: "fault"}))
			return err
		}},
		{"GetServerStatus", func() error {
			_, err := n.raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
			return err
		}},
		{"DeleteVerticesByPrefix dry run", func() error {
			_, err := n.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "relay", DryRun: true}))
			return err
		}},
		{"DeleteEdgesByPrefix dry run", func() error {
			_, err := n.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "fault", DryRun: true}))
			return err
		}},
	}
	for _, check := range checks {
		if err := check.read(); err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
			t.Errorf("%s during publication fault = %v, want gapped FailedPrecondition", check.name, err)
		}
	}
	stream, err := n.raw.BackupSnapshot(ctx, connect.NewRequest(&pb.BackupSnapshotRequest{}))
	if err == nil {
		if stream.Receive() {
			t.Error("BackupSnapshot exposed a graph record during publication fault")
		}
		err = stream.Err()
		_ = stream.Close()
	}
	if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
		t.Errorf("BackupSnapshot during publication fault = %v, want gapped FailedPrecondition", err)
	}
	if _, err := n.raw.GetReplicationStatus(ctx, connect.NewRequest(&pb.GetReplicationStatusRequest{})); err != nil {
		t.Errorf("GetReplicationStatus must remain available during publication fault: %v", err)
	}
}

func assertGraphWritesGapped(t *testing.T, ctx context.Context, n *pumpNode, remote hlc.NodeID) {
	t.Helper()
	beforeVertices, beforeEdges := n.cache.VertexCount(), n.cache.EdgeCount()
	beforeLog := n.log.Len()
	beforeLocalSeq, beforeRemoteSeq := n.svc.LocalSeq(n.nodeID), n.svc.LocalSeq(remote)
	checks := []struct {
		name  string
		write func() error
	}{
		{"PutVertex", func() error {
			_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "not-admitted"}}))
			return err
		}},
		{"PutVertices if_absent", func() error {
			_, err := n.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{
				IfAbsent: true, Vertices: []*pb.Vertex{{Key: "not-admitted"}},
			}))
			return err
		}},
		{"PutEdge", func() error {
			_, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "fault-tail", Head: "fault-head", Weight: 3}}))
			return err
		}},
		{"PutEdges outcomes", func() error {
			_, err := n.raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: []*pb.Edge{{Tail: "fault-tail", Head: "fault-head", Weight: 3}}}))
			return err
		}},
		{"AddEdge effective", func() error {
			_, err := n.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "fault-tail", Head: "fault-head", Weight: 3}}))
			return err
		}},
		{"AddEdges effective", func() error {
			_, err := n.raw.AddEdges(ctx, connect.NewRequest(&pb.AddEdgesRequest{Edges: []*pb.Edge{{Tail: "fault-tail", Head: "fault-head", Weight: 3}}}))
			return err
		}},
		{"DeleteVertex", func() error {
			_, err := n.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "relay-warmup"}))
			return err
		}},
		{"DeleteVertices outcomes", func() error {
			_, err := n.raw.DeleteVertices(ctx, connect.NewRequest(&pb.DeleteVerticesRequest{Keys: []string{"relay-warmup"}}))
			return err
		}},
		{"DeleteEdge", func() error {
			_, err := n.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "fault-tail", Head: "fault-head"}))
			return err
		}},
		{"DeleteEdges outcomes", func() error {
			_, err := n.raw.DeleteEdges(ctx, connect.NewRequest(&pb.DeleteEdgesRequest{Edges: []*pb.EdgeKey{{Tail: "fault-tail", Head: "fault-head"}}}))
			return err
		}},
		{"DeleteVerticesByPrefix", func() error {
			_, err := n.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "relay"}))
			return err
		}},
		{"DeleteEdgesByPrefix", func() error {
			_, err := n.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "fault"}))
			return err
		}},
		{"empty PutEdges", func() error {
			_, err := n.raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{}))
			return err
		}},
	}
	for _, check := range checks {
		if err := check.write(); connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
			t.Errorf("%s during Snapshot fault = %v, want gapped FailedPrecondition", check.name, err)
		}
		if n.cache.VertexCount() != beforeVertices || n.cache.EdgeCount() != beforeEdges {
			t.Fatalf("%s changed graph counts from %d/%d to %d/%d",
				check.name, beforeVertices, beforeEdges, n.cache.VertexCount(), n.cache.EdgeCount())
		}
		if got, live := n.cache.GetWeight("fault-tail", "fault-head"); !live || got != 1 {
			t.Fatalf("%s changed partially installed edge to (%v, %t)", check.name, got, live)
		}
		if value, live := n.cache.GetVertex("relay-warmup"); !live || value.GetString_() != "ready" {
			t.Fatalf("%s changed partially installed vertex to (%v, %t)", check.name, value, live)
		}
		if n.svc.LocalSeq(n.nodeID) != beforeLocalSeq || n.svc.LocalSeq(remote) != beforeRemoteSeq || n.log.Len() != beforeLog {
			t.Fatalf("%s changed local/remote seq or log to %d/%d/%d",
				check.name, n.svc.LocalSeq(n.nodeID), n.svc.LocalSeq(remote), n.log.Len())
		}
	}
}

// startPump attaches a Pump to the node aimed at the supplied peer
// URLs (or "host:port" — replication.peerBaseURL coerces both).
func (n *pumpNode) startPump(ctx context.Context, t *testing.T, peers []string) {
	n.startPumpWithMetrics(ctx, t, peers, nil)
}

func (n *pumpNode) startPumpWithMetrics(ctx context.Context, t *testing.T, peers []string, metrics replication.Metrics) {
	n.startPumpWithMetricsAndTransport(ctx, t, peers, metrics, nil)
}

func (n *pumpNode) startPumpWithMetricsAndTransport(ctx context.Context, t *testing.T, peers []string, metrics replication.Metrics, transport *replication.PeerTransport) {
	t.Helper()
	p := replication.NewPump(replication.Config{
		NodeID:                  n.nodeID,
		Peers:                   peers,
		BackoffMin:              20 * time.Millisecond,
		BackoffMax:              200 * time.Millisecond,
		HTTPClient:              h2cClient(),
		SearchConfigFingerprint: n.svc.SearchConfigFingerprint(),
		Metrics:                 metrics,
		PeerTransport:           transport,
	}, n.svc, n.cache)
	n.pump = p
	pumpCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	go func() {
		_ = p.Run(pumpCtx)
		close(done)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Logf("pump %x did not stop within 2s", n.nodeID[:4])
		}
	})
}

type observedSearchConfigMetrics struct {
	gate         *readiness.Gate
	observations chan bool
	snapshots    atomic.Int32
}

func (m *observedSearchConfigMetrics) OnPumpConnect(peer string) {
	m.gate.OnPumpConnect(peer)
}
func (m *observedSearchConfigMetrics) OnPumpDisconnect(peer, reason string) {
	m.gate.OnPumpDisconnect(peer, reason)
}
func (m *observedSearchConfigMetrics) OnPumpApply(peer string) { m.gate.OnPumpApply(peer) }
func (m *observedSearchConfigMetrics) OnPumpDropSelfEcho(peer string) {
	m.gate.OnPumpDropSelfEcho(peer)
}
func (m *observedSearchConfigMetrics) OnPumpSnapshotReplayed(peer string, vertices, edges uint64, duration time.Duration) {
	m.snapshots.Add(1)
	m.gate.OnPumpSnapshotReplayed(peer, vertices, edges, duration)
}
func (m *observedSearchConfigMetrics) OnSearchConfig(peer string, matched bool) {
	m.gate.OnSearchConfig(peer, matched)
	select {
	case m.observations <- matched:
	default:
	}
}

// waitForVertex polls cache.GetVertex until the key appears or the
// deadline elapses.
func waitForVertex(t *testing.T, cache *graphcache.GraphCache[string, *pb.Vertex], key string, timeout time.Duration) bool {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if _, ok := cache.GetVertex(key); ok {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return false
}

// waitForEdge polls cache.GetWeight until the (tail,head) appears with
// at least the expected weight, or the deadline elapses.
func waitForEdge(t *testing.T, cache *graphcache.GraphCache[string, *pb.Vertex], tail, head string, want float32, timeout time.Duration) (float32, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if w, ok := cache.GetWeight(tail, head); ok && w >= want-1e-6 {
			return w, true
		}
		time.Sleep(10 * time.Millisecond)
	}
	w, ok := cache.GetWeight(tail, head)
	return w, ok
}

// waitForLogSeq polls until the log reaches or exceeds the expected
// frontier, returning the final observation for strict caller checks.
func waitForLogSeq(t *testing.T, log *mutationlog.Log, want uint64, timeout time.Duration) (uint64, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if last, ok := log.LastSeq(); ok && last >= want {
			return last, true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return log.LastSeq()
}

// waitForWireEdge is the real Connect/h2c sibling used by gap-recovery tests
// whose contract is externally observable through GetEdge. NotFound, a
// Snapshot gap, and a publication change during a read are transient; an
// error still present at the deadline is terminal.
func waitForWireEdge(t *testing.T, ctx context.Context, raw graphv1connect.LanternServiceClient, tail, head string, want float32, timeout time.Duration) (float32, bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		response, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: tail, Head: head}))
		if err == nil {
			weight := response.Msg.GetEdge().GetWeight()
			if weight == want {
				return weight, true
			}
		} else if code := connect.CodeOf(err); code != connect.CodeNotFound &&
			(code != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped:")) {
			var connectErr *connect.Error
			if code != connect.CodeUnavailable || !errors.As(err, &connectErr) ||
				connectErr.Message() != "graph publication changed during read; retry" {
				t.Fatalf("GetEdge %s->%s: %v", tail, head, err)
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	response, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: tail, Head: head}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("GetEdge %s->%s: %v", tail, head, err)
		}
		return 0, false
	}
	return response.Msg.GetEdge().GetWeight(), true
}

type onePublicationChangeClient struct {
	graphv1connect.LanternServiceClient
	calls int
}

func (c *onePublicationChangeClient) GetEdge(context.Context, *connect.Request[pb.GetEdgeRequest]) (*connect.Response[pb.GetEdgeResponse], error) {
	c.calls++
	if c.calls == 1 {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("graph publication changed during read; retry"))
	}
	return connect.NewResponse(&pb.GetEdgeResponse{Edge: &pb.Edge{Weight: 3}}), nil
}

func TestWaitForWireEdgeRetriesPublicationChange(t *testing.T) {
	raw := &onePublicationChangeClient{}
	got, ok := waitForWireEdge(t, context.Background(), raw, "tail", "head", 3, time.Second)
	if !ok || got != 3 || raw.calls != 2 {
		t.Fatalf("GetEdge after publication change = (%g, %v), calls=%d; want (3, true) after two calls", got, ok, raw.calls)
	}
}

type collidingReadBackend struct {
	service.Backend
	reads      atomic.Int32
	collisions chan chan struct{}
	done       <-chan struct{}
}

func (b *collidingReadBackend) GetVertex(key string) (*pb.Vertex, bool) {
	if key == "contended-read" && b.reads.Add(1) <= 3 {
		resume := make(chan struct{})
		select {
		case b.collisions <- resume:
			select {
			case <-resume:
			case <-b.done:
			}
		case <-b.done:
		}
	}
	return b.Backend.GetVertex(key)
}

func TestHAReadContentionFallsBackToCommittedWireCapture(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	log := mutationlog.New(mutationlog.Options{Capacity: 16})
	t.Cleanup(func() { _ = log.Close() })
	backend := &collidingReadBackend{
		Backend:    graphcache.NewGraphCache[string, *pb.Vertex](time.Minute),
		collisions: make(chan chan struct{}),
		done:       ctx.Done(),
	}
	svc := service.NewLanternService(backend).
		WithReplication(log, hlc.New(hlc.NodeID{0x75}, hlc.Options{}), nil)
	srv := newConnectTestServer(t, svc, nil)
	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	writes := make(chan error, 1)
	go func() {
		for i, value := range []string{"first", "second", "third"} {
			select {
			case resume := <-backend.collisions:
				_, err := raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{
					Vertex: &pb.Vertex{Key: "contended-read", Value: &pb.Vertex_String_{String_: value}},
				}))
				close(resume)
				if err != nil {
					writes <- fmt.Errorf("overlapping PutVertex %d: %w", i, err)
					return
				}
			case <-ctx.Done():
				writes <- ctx.Err()
				return
			}
		}
		writes <- nil
	}()
	response, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "contended-read"}))
	if err != nil {
		t.Fatalf("committed h2c GetVertex: %v", err)
	}
	if response.Msg.GetVertex().GetString_() != "third" || backend.reads.Load() != 4 {
		t.Fatalf("committed h2c GetVertex = (%v, %v), reads=%d; want third after 4 captures",
			response, err, backend.reads.Load())
	}
	if err := <-writes; err != nil {
		t.Fatal(err)
	}
	finish, err := svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{
		Keys: []string{"contended-read"},
	})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		finish(false)
		t.Fatalf("h2c read during Snapshot fault = %v, want FailedPrecondition", err)
	}
	finish(true)
}

// TestPeerPump_E2E_ThreeNodeConvergence wires three peers in a full
// mesh (A↔B↔C) and asserts that a write to any one node is observed
// on every other node within a short polling window. This exercises
// the Subscribe path of the Pump (#185) plus the self-echo filter
// (no mutation should bounce back to its origin).
func TestPeerPump_E2E_ThreeNodeConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-peer pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	a := newPumpNode(t, hlc.NodeID{0x0A})
	b := newPumpNode(t, hlc.NodeID{0x0B})
	c := newPumpNode(t, hlc.NodeID{0x0C})

	a.startPump(ctx, t, []string{b.url, c.url})
	b.startPump(ctx, t, []string{a.url, c.url})
	c.startPump(ctx, t, []string{a.url, b.url})

	// Give the pumps a moment to attach Subscribe streams before we
	// start writing — without this small head-start a fast write
	// could race the very first Subscribe handshake and the receiver
	// would have to fall through the gap → snapshot recovery path,
	// which is also tested below but adds latency to this assertion.
	time.Sleep(150 * time.Millisecond)

	// Write to A; expect convergence on B and C.
	if _, err := a.sdk.PutVertex(ctx, "from-a", "va", time.Minute); err != nil {
		t.Fatalf("a.PutVertex: %v", err)
	}
	if _, err := a.sdk.AddEdge(ctx, "from-a", "target", 1.5, time.Minute); err != nil {
		t.Fatalf("a.AddEdge: %v", err)
	}

	// Write to B; expect convergence on A and C.
	if _, err := b.sdk.PutVertex(ctx, "from-b", "vb", time.Minute); err != nil {
		t.Fatalf("b.PutVertex: %v", err)
	}

	// Write to C; expect convergence on A and B.
	if _, err := c.sdk.PutVertex(ctx, "from-c", "vc", time.Minute); err != nil {
		t.Fatalf("c.PutVertex: %v", err)
	}

	for _, tc := range []struct {
		name, key string
		cache     *graphcache.GraphCache[string, *pb.Vertex]
	}{
		{"b sees from-a", "from-a", b.cache},
		{"c sees from-a", "from-a", c.cache},
		{"a sees from-b", "from-b", a.cache},
		{"c sees from-b", "from-b", c.cache},
		{"a sees from-c", "from-c", a.cache},
		{"b sees from-c", "from-c", b.cache},
	} {
		if !waitForVertex(t, tc.cache, tc.key, 3*time.Second) {
			t.Errorf("%s: vertex %q not visible within 3s", tc.name, tc.key)
		}
	}

	// Edge convergence.
	if w, ok := waitForEdge(t, b.cache, "from-a", "target", 1.5, 3*time.Second); !ok || w != 1.5 {
		t.Errorf("b edge from-a->target: got w=%v ok=%v want 1.5/true", w, ok)
	}
	if w, ok := waitForEdge(t, c.cache, "from-a", "target", 1.5, 3*time.Second); !ok || w != 1.5 {
		t.Errorf("c edge from-a->target: got w=%v ok=%v want 1.5/true", w, ok)
	}

	// Reading B (#415): every node's mutation log retains all four
	// cluster-wide writes (A's PutVertex + AddEdge, B's PutVertex,
	// C's PutVertex). Per-origin contiguous commit (ApplyMutation) +
	// origin-anchored mu.Seq (logMutation allocates it; Subscribe relay
	// preserves it across hops) guarantee each (origin, seq) lands
	// at most once on every replica, so the monotonic LastSeq is
	// exactly the count of distinct cluster mutations.
	const wantClusterWrites = 4
	const logConvergenceTimeout = 3 * time.Second
	for _, node := range []struct {
		name string
		log  *mutationlog.Log
	}{
		{"a", a.log},
		{"b", b.log},
		{"c", c.log},
	} {
		if last, ok := waitForLogSeq(t, node.log, wantClusterWrites, logConvergenceTimeout); !ok || last != wantClusterWrites {
			t.Errorf("%s.log.LastSeq=%d ok=%v want exactly %d within %s (leaderless Subscribe contract)", node.name, last, ok, wantClusterWrites, logConvergenceTimeout)
		}
	}
	waitForSearchConvergence(t, ctx, "from", nil, a.raw, b.raw, c.raw)
}

type coldSelfEchoIntegrationMetrics struct {
	upgrades         chan struct{}
	snapshotReplayed chan struct{}
	echoes           atomic.Int32
	snapshots        atomic.Int32
}

type coldStreamStartMetrics struct{ started chan struct{} }

func (m *coldStreamStartMetrics) OnSubscribeStarted() {
	select {
	case m.started <- struct{}{}:
	default:
	}
}
func (*coldStreamStartMetrics) OnSubscribeEnded()         {}
func (*coldStreamStartMetrics) OnSubscribeDropped(string) {}

func (*coldSelfEchoIntegrationMetrics) OnPumpConnect(string)        {}
func (*coldSelfEchoIntegrationMetrics) OnPumpApply(string)          {}
func (*coldSelfEchoIntegrationMetrics) OnSearchConfig(string, bool) {}
func (m *coldSelfEchoIntegrationMetrics) OnPumpDropSelfEcho(string) { m.echoes.Add(1) }
func (m *coldSelfEchoIntegrationMetrics) OnPumpDisconnect(_ string, reason string) {
	if reason == "self_echo_upgrade" {
		select {
		case m.upgrades <- struct{}{}:
		default:
		}
	}
}
func (m *coldSelfEchoIntegrationMetrics) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) {
	m.snapshots.Add(1)
	select {
	case m.snapshotReplayed <- struct{}{}:
	default:
	}
}

func TestCertifiedSubscribeFiltersCoveredOriginsAndRejectsMissingTailOverH2C(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	node := newCertifiedPumpNode(t, hlc.NodeID{0x78}, 2<<20, 0)
	remoteID := hlc.NodeID{0x79}
	remoteClock := hlc.New(remoteID, hlc.Options{})
	putLocal := func(key string) {
		t.Helper()
		if _, err := node.sdk.PutVertex(ctx, key, "value", time.Minute); err != nil {
			t.Fatalf("local PutVertex(%s): %v", key, err)
		}
	}
	putRemote := func(seq uint64) {
		t.Helper()
		stamp := remoteClock.Now()
		if err := node.svc.ApplyMutation(ctx, &pb.Mutation{
			Origin: remoteID[:], Seq: seq,
			Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, Logical: stamp.Logical, NodeId: remoteID[:]},
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
				Vertex: &pb.Vertex{Key: fmt.Sprintf("remote-%d", seq)},
			}}},
		}); err != nil {
			t.Fatalf("remote ApplyMutation(%d): %v", seq, err)
		}
	}
	putLocal("local-1")
	putRemote(1)
	putLocal("local-2")
	putRemote(2)

	cursor := map[string]uint64{
		hex.EncodeToString(node.nodeID[:]): math.MaxUint64,
		hex.EncodeToString(remoteID[:]):    1,
	}
	replication := newReplicationRawClient(t, node.url)
	for _, projection := range []pb.SubscribeProjection{
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION,
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
	} {
		stream, err := replication.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
			Projection: projection, FromSeqPerOrigin: cursor,
		}))
		if err != nil {
			t.Fatalf("Subscribe(%s): %v", projection, err)
		}
		for want := uint64(1); want <= 2; want++ {
			if !stream.Receive() {
				t.Fatalf("Subscribe(%s) remote seq %d: %v", projection, want, stream.Err())
			}
			frame := stream.Msg()
			var origin []byte
			var seq uint64
			if projection == pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY {
				origin, seq = frame.GetIdentityChunk().GetOrigin(), frame.GetIdentityChunk().GetSeq()
			} else {
				origin, seq = frame.GetMutation().GetOrigin(), frame.GetMutation().GetSeq()
			}
			if !bytes.Equal(origin, remoteID[:]) || seq != want {
				t.Fatalf("Subscribe(%s) frame = %v, want remote seq %d", projection, frame, want)
			}
		}
		_ = stream.Close()
	}

	for i := 0; i < 16; i++ {
		putLocal(fmt.Sprintf("evict-%d", i))
	}
	for _, projection := range []pb.SubscribeProjection{
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_FULL_MUTATION,
		pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
	} {
		stream, err := replication.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
			Projection: projection, FromSeqPerOrigin: cursor,
		}))
		if err == nil {
			if stream.Receive() {
				t.Fatalf("Subscribe(%s) streamed across missing remote history", projection)
			}
			err = stream.Err()
			_ = stream.Close()
		}
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("Subscribe(%s) evicted remote tail = %v, want FailedPrecondition", projection, err)
		}
	}
}

func TestPeerPumpColdSelfEchoReopensFilteredStreamOverH2C(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	aStreams := &coldStreamStartMetrics{started: make(chan struct{}, 1)}
	bStreams := &coldStreamStartMetrics{started: make(chan struct{}, 1)}
	a := newPumpNodeWithWALAndMetrics(t, hlc.NodeID{0x74}, 1024, false, nil, aStreams)
	b := newPumpNodeWithWALAndMetrics(t, hlc.NodeID{0x75}, 1024, false, nil, bStreams)
	metrics := &coldSelfEchoIntegrationMetrics{upgrades: make(chan struct{}, 1)}
	a.startPumpWithMetrics(ctx, t, []string{b.url}, metrics)
	b.startPump(ctx, t, []string{a.url})

	for _, started := range []<-chan struct{}{aStreams.started, bStreams.started} {
		select {
		case <-started:
		case <-ctx.Done():
			t.Fatal("cold bidirectional Subscribe streams were not established")
		}
	}
	if _, err := a.sdk.PutVertex(ctx, "cold-own-first", "a1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !waitForVertex(t, b.cache, "cold-own-first", 3*time.Second) {
		t.Fatal("B did not receive A's first write")
	}
	select {
	case <-metrics.upgrades:
	case <-ctx.Done():
		t.Fatal("A did not reopen after its committed self-echo")
	}
	if metrics.echoes.Load() == 0 {
		t.Fatal("A upgraded without receiving a self-echo")
	}

	if _, err := a.sdk.PutVertex(ctx, "cold-own-second", "a2", time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := b.sdk.PutVertex(ctx, "cold-remote", "b1", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !waitForVertex(t, b.cache, "cold-own-second", 3*time.Second) ||
		!waitForVertex(t, a.cache, "cold-remote", 3*time.Second) {
		t.Fatal("filtered stream did not preserve bidirectional writes")
	}
	if metrics.snapshots.Load() != 0 {
		t.Fatal("committed self-echo incorrectly forced Snapshot repair")
	}
}

func TestPeerPumpFiltersSelfEchoAndRepairsOwnHistoryAfterRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNodeWithSearch(t, hlc.NodeID{0x76}, 1024, false)
	b := newPumpNodeWithSearch(t, hlc.NodeID{0x77}, 1024, false)
	if _, err := a.sdk.PutVertex(ctx, "own-before", "a1", time.Minute); err != nil {
		t.Fatal(err)
	}
	bCtx, stopB := context.WithCancel(ctx)
	defer stopB()
	b.startPump(bCtx, t, []string{a.url})
	if !waitForVertex(t, b.cache, "own-before", 3*time.Second) {
		t.Fatal("B did not apply A's first write")
	}
	if _, err := b.sdk.PutVertex(ctx, "remote-before", "b1", time.Minute); err != nil {
		t.Fatal(err)
	}
	aCtx, stopA := context.WithCancel(ctx)
	defer stopA()
	a.startPump(aCtx, t, []string{b.url})
	if !waitForVertex(t, a.cache, "remote-before", 3*time.Second) {
		t.Fatal("A did not attach to B's full-mutation stream")
	}
	if _, err := a.sdk.PutVertex(ctx, "own-after", "a2", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !waitForVertex(t, b.cache, "own-after", 3*time.Second) {
		t.Fatal("B did not apply A's second write")
	}
	if _, err := b.sdk.PutVertex(ctx, "remote-after", "b2", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !waitForVertex(t, a.cache, "remote-after", 3*time.Second) {
		t.Fatal("A did not receive B's write after its filtered self-echo")
	}
	for _, node := range []*pumpNode{a, b} {
		if last, ok := waitForLogSeq(t, node.log, 4, 3*time.Second); !ok || last != 4 {
			t.Fatalf("node %x retained %d entries (ok=%v), want 4 unique mutations", node.nodeID, last, ok)
		}
	}

	// The same h2c endpoint must retain all origins for a CDC consumer,
	// while a caller that already owns A's committed history may exclude it.
	keyA, keyB := hex.EncodeToString(a.nodeID[:]), hex.EncodeToString(b.nodeID[:])
	feed, err := newReplicationRawClient(t, b.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		FromSeqPerOrigin: map[string]uint64{keyA: math.MaxUint64, keyB: 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	for seq := uint64(1); seq <= 2; seq++ {
		if !feed.Receive() || !bytes.Equal(feed.Msg().GetMutation().GetOrigin(), b.nodeID[:]) ||
			feed.Msg().GetMutation().GetSeq() != seq {
			t.Fatalf("filtered h2c Subscribe frame %d = %v (err=%v), want B origin", seq, feed.Msg(), feed.Err())
		}
	}
	_ = feed.Close()

	stopA()
	stopB()
	a.server.srv.Close()
	restarted := newPumpNodeWithSearch(t, a.nodeID, 1024, false)
	if got := restarted.svc.LocalSeq(a.nodeID); got != 0 {
		t.Fatalf("fresh same-NodeID replica has local-origin cursor %d, want 0", got)
	}
	replayed := make(chan struct{}, 1)
	restarted.startPumpWithMetrics(ctx, t, []string{b.url}, &coldSelfEchoIntegrationMetrics{
		snapshotReplayed: replayed,
	})
	// Snapshot graph frames precede their origin watermark; wait for verified completion.
	select {
	case <-replayed:
	case <-ctx.Done():
		t.Fatalf("same-NodeID restart did not complete peer Snapshot: %v", ctx.Err())
	}
	if !waitForVertex(t, restarted.cache, "own-after", 5*time.Second) ||
		!waitForVertex(t, restarted.cache, "remote-after", 5*time.Second) {
		t.Fatal("same-NodeID restart did not restore committed graph from peer Snapshot")
	}
	if got := restarted.svc.LocalSeq(a.nodeID); got != 2 {
		t.Fatalf("restored local-origin cursor = %d, want 2", got)
	}
}

// TestPeerPump_ReverseOriginSeqRelay exercises the Connect/h2c Subscribe path
// across A -> B -> C. A's log deliberately delivers one origin in reverse
// seq order. B must keep the future entries out of its graph, Snapshot, and
// relay log until seq 1 arrives; C must then observe the contiguous tail.
func TestPeerPump_ReverseOriginSeqRelay(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-peer pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNode(t, hlc.NodeID{0xA1})
	b := newPumpNode(t, hlc.NodeID{0xB1})
	c := newPumpNode(t, hlc.NodeID{0xC1})
	appendSource := func(seq uint64) {
		t.Helper()
		stamp := hlc.Timestamp{WallNs: int64(seq), NodeID: a.nodeID}
		mutation := &pb.Mutation{
			Origin: a.nodeID[:], Seq: seq,
			Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: a.nodeID[:]},
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{
				Vertex: &pb.Vertex{Key: fmt.Sprintf("reverse-wire-%d", seq)},
			}}},
		}
		if _, err := a.log.Append(mutation, stamp); err != nil {
			t.Fatal(err)
		}
	}
	for _, seq := range []uint64{4, 3, 2} {
		appendSource(seq)
	}
	b.startPump(ctx, t, []string{a.url})
	c.startPump(ctx, t, []string{b.url})
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		rows := b.pump.Snapshot()
		if len(rows) == 1 && rows[0].AppliedSeq == 2 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	rows := b.pump.Snapshot()
	if len(rows) != 1 || rows[0].AppliedSeq != 2 {
		t.Fatalf("B did not receive reverse source prefix over h2c: %+v", rows)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 0 {
		t.Fatalf("B advertised missing origin prefix as seq %d", got)
	}
	if got := b.log.Len(); got != 0 {
		t.Fatalf("B relayed %d future entries before seq 1", got)
	}
	for _, node := range []*pumpNode{b, c} {
		if _, ok := node.cache.GetVertex("reverse-wire-4"); ok {
			t.Fatalf("node %x exposed future mutation before gap closed", node.nodeID)
		}
	}
	stream, err := newReplicationRawClient(t, b.url).Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var header *pb.SnapshotHeader
	for stream.Receive() {
		switch entry := stream.Msg().GetEntry().(type) {
		case *pb.SnapshotResponse_Header:
			header = entry.Header
		case *pb.SnapshotResponse_Vertex:
			if entry.Vertex.GetVertex().GetKey() == "reverse-wire-4" {
				t.Fatal("Snapshot contained an unpublished future mutation")
			}
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if header == nil || header.GetCutoffSeqPerOrigin()[hex.EncodeToString(a.nodeID[:])] != 0 {
		t.Fatalf("Snapshot crossed pending origin gap: %+v", header)
	}

	appendSource(1)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (b.svc.LocalSeq(a.nodeID) < 4 || c.svc.LocalSeq(a.nodeID) < 4) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, node := range []*pumpNode{b, c} {
		if got := node.svc.LocalSeq(a.nodeID); got != 4 {
			t.Fatalf("node %x origin cursor = %d, want 4", node.nodeID, got)
		}
		if got := node.log.Len(); got != 4 {
			t.Fatalf("node %x relay log length = %d, want 4", node.nodeID, got)
		}
		for seq := uint64(1); seq <= 4; seq++ {
			if _, ok := node.cache.GetVertex(fmt.Sprintf("reverse-wire-%d", seq)); !ok {
				t.Fatalf("node %x missing graph seq %d", node.nodeID, seq)
			}
		}
	}
	for _, node := range []*pumpNode{b, c} {
		feed, err := newReplicationRawClient(t, node.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
		if err != nil {
			t.Fatal(err)
		}
		for seq := uint64(1); seq <= 4; seq++ {
			if !feed.Receive() || feed.Msg().GetMutation().GetSeq() != seq {
				t.Fatalf("node %x Subscribe origin seq %d: msg=%+v err=%v", node.nodeID, seq, feed.Msg(), feed.Err())
			}
		}
		_ = feed.Close()
	}
}

// TestPeerPump_RelayAppendFailureReconnects confirms a failed relay append
// closes the h2c Subscribe session and the pump's next session re-delivers
// the same origin seq. The graph-applied frontier is not applied twice.
func TestPeerPump_RelayAppendFailureReconnects(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-peer pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNode(t, hlc.NodeID{0xA2})
	wal := &retryRelayWAL{
		failed: make(chan struct{}), retryStarted: make(chan struct{}), allowRetry: make(chan struct{}),
	}
	t.Cleanup(func() {
		select {
		case <-wal.allowRetry:
		default:
			close(wal.allowRetry)
		}
	})
	b := newPumpNodeWithWAL(t, hlc.NodeID{0xB2}, 1024, true, wal)
	c := newPumpNode(t, hlc.NodeID{0xC2})
	b.startPump(ctx, t, []string{a.url})
	c.startPump(ctx, t, []string{b.url})
	if _, err := a.sdk.AddEdge(ctx, "retry-a", "retry-b", 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	for _, signal := range []<-chan struct{}{wal.failed, wal.retryStarted} {
		select {
		case <-signal:
		case <-ctx.Done():
			t.Fatalf("pump did not reconnect after failed relay append: %v", ctx.Err())
		}
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 0 {
		t.Fatalf("B advanced origin cutoff across failed append: %d", got)
	}
	if weight, ok := b.cache.GetWeight("retry-a", "retry-b"); !ok || weight != 2 {
		t.Fatalf("B graph after first apply = (%v,%v), want (2,true)", weight, ok)
	}
	if _, ok := c.cache.GetWeight("retry-a", "retry-b"); ok {
		t.Fatal("C observed mutation before B published relay log")
	}
	close(wal.allowRetry)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && (b.svc.LocalSeq(a.nodeID) < 1 || c.svc.LocalSeq(a.nodeID) < 1) {
		time.Sleep(10 * time.Millisecond)
	}
	for _, node := range []*pumpNode{b, c} {
		if got := node.svc.LocalSeq(a.nodeID); got != 1 {
			t.Fatalf("node %x origin cursor = %d, want 1", node.nodeID, got)
		}
		// C may receive the mutation through Snapshot after B gaps its
		// existing Subscribe stream. Bootstrap state has a cutoff but does
		// not synthesize historical local log entries.
		if got := node.log.Len(); node == b && got != 1 {
			t.Fatalf("B relay log length = %d, want 1", got)
		}
		if weight, ok := node.cache.GetWeight("retry-a", "retry-b"); !ok || weight != 2 {
			t.Fatalf("node %x AddEdge replayed twice: (%v,%v)", node.nodeID, weight, ok)
		}
	}
	if got := wal.writes.Load(); got != 2 {
		t.Fatalf("B WAL writes = %d, want initial failure and one reconnect retry", got)
	}
}

// TestPeerPump_RelayAppendFaultGapsCDC verifies that a graph-visible remote
// mutation with a failed relay WAL append never looks like an uninterrupted
// Subscribe/Snapshot history. The old stream stays gapped after repair.
func TestPeerPump_RelayAppendFaultGapsCDC(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping h2c peer-pump test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	a := newPumpNode(t, hlc.NodeID{0xA3})
	wal := &stickyRelayWAL{faulted: make(chan struct{})}
	metrics := &signalSubscribeMetrics{started: make(chan struct{}, 1)}
	b := newPumpNodeWithWALAndMetrics(t, hlc.NodeID{0xB3}, 1024, true, wal, metrics)
	b.startPump(ctx, t, []string{a.url})
	if _, err := a.sdk.PutVertex(ctx, "relay-warmup", "ready", time.Hour); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && b.svc.LocalSeq(a.nodeID) < 1 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 1 {
		t.Fatalf("warmup origin seq = %d, want 1", got)
	}

	rep := newReplicationRawClient(t, b.url)
	oldFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = oldFeed.Close() }()
	if !oldFeed.Receive() || oldFeed.Msg().GetMutation().GetSeq() != 1 {
		t.Fatalf("old Subscribe warmup = (%+v,%v), want origin seq 1", oldFeed.Msg(), oldFeed.Err())
	}
	oldStreamEnded := make(chan error, 1)
	go func() {
		if oldFeed.Receive() {
			oldStreamEnded <- fmt.Errorf("old Subscribe unexpectedly received %+v", oldFeed.Msg())
			return
		}
		oldStreamEnded <- oldFeed.Err()
	}()
	select {
	case <-metrics.started:
	case <-ctx.Done():
		t.Fatal("old Subscribe did not start")
	}
	wal.armed.Store(true)
	if _, err := a.sdk.AddEdge(ctx, "fault-tail", "fault-head", 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	select {
	case streamErr := <-oldStreamEnded:
		if connect.CodeOf(streamErr) != connect.CodeFailedPrecondition || !strings.Contains(streamErr.Error(), "gapped") {
			t.Fatalf("old Subscribe error = %v, want gapped FailedPrecondition", streamErr)
		}
	case <-ctx.Done():
		t.Fatal("old Subscribe did not close on relay WAL failure")
	}
	if weight, ok := b.cache.GetWeight("fault-tail", "fault-head"); !ok || weight != 2 {
		t.Fatalf("graph-visible failed publication = (%v,%v), want (2,true)", weight, ok)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 1 {
		t.Fatalf("failed publication advanced origin cursor to %d", got)
	}
	if got := b.log.Len(); got != 1 {
		t.Fatalf("failed publication added %d relay log entries, want only warmup", got)
	}
	assertGraphReadsGapped(t, ctx, b)

	faultCtx, faultCancel := context.WithTimeout(ctx, 2*time.Second)
	defer faultCancel()
	newFeed, err := rep.Subscribe(faultCtx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 2}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("new Subscribe error = %v, want FailedPrecondition", err)
		}
	} else {
		if newFeed.Receive() || connect.CodeOf(newFeed.Err()) != connect.CodeFailedPrecondition {
			t.Fatalf("new Subscribe during fault = (%+v,%v), want gapped", newFeed.Msg(), newFeed.Err())
		}
		_ = newFeed.Close()
	}
	snapshot, err := rep.Snapshot(faultCtx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		if connect.CodeOf(err) != connect.CodeFailedPrecondition {
			t.Fatalf("Snapshot during fault error = %v, want FailedPrecondition", err)
		}
	} else {
		if snapshot.Receive() || connect.CodeOf(snapshot.Err()) != connect.CodeFailedPrecondition {
			t.Fatalf("Snapshot during fault = (%+v,%v), want gapped", snapshot.Msg(), snapshot.Err())
		}
		_ = snapshot.Close()
	}

	wal.healed.Store(true)
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && b.svc.LocalSeq(a.nodeID) < 2 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := b.svc.LocalSeq(a.nodeID); got != 2 {
		t.Fatalf("repaired origin cursor = %d, want 2", got)
	}
	if got := b.log.Len(); got != 2 {
		t.Fatalf("repaired relay log length = %d, want 2", got)
	}
	if wal.failures.Load() == 0 {
		t.Fatal("WAL failure was not exercised")
	}
	healthyFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 2}))
	if err != nil {
		t.Fatal(err)
	}
	if !healthyFeed.Receive() || healthyFeed.Msg().GetMutation().GetSeq() != 2 {
		t.Fatalf("repaired Subscribe = (%+v,%v), want origin seq 2", healthyFeed.Msg(), healthyFeed.Err())
	}
	_ = healthyFeed.Close()
	healthySnapshot, err := rep.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	if !healthySnapshot.Receive() || healthySnapshot.Msg().GetHeader().GetCutoffSeqPerOrigin()[hex.EncodeToString(a.nodeID[:])] != 2 {
		t.Fatalf("repaired Snapshot header = (%+v,%v), want origin cutoff 2", healthySnapshot.Msg(), healthySnapshot.Err())
	}
	_ = healthySnapshot.Close()
	if _, err := b.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "fault-tail", Head: "fault-head"})); err != nil {
		t.Fatalf("graph read after relay repair: %v", err)
	}
}

// A local graph-first write may have changed read-visible state when the WAL
// rejects its append. Across the real Connect/h2c service and replication
// handlers, every such path must gap the old feed, hold its origin seq, and
// repair the exact original mutation before a later local write proceeds.
func TestLocalWritePublication_WALFaultGapsCDCAndRepairs(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping h2c publication fault test in short mode")
	}
	type scenario struct {
		name   string
		seed   func(context.Context, *pumpNode) error
		write  func(context.Context, *pumpNode) error
		verify func(*testing.T, *pumpNode, *pb.Mutation)
	}
	putVertex := func(ctx context.Context, n *pumpNode, key string) error {
		_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: key, Value: &pb.Vertex_String_{String_: key}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
		}}))
		return err
	}
	putEdge := func(ctx context.Context, n *pumpNode, tail, head string) error {
		_, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
			Tail: tail, Head: head, Weight: 3, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
		}}))
		return err
	}
	cases := []scenario{
		{
			name: "conditional PutVertex",
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{IfAbsent: true, Vertex: &pb.Vertex{
					Key: "conditional", Value: &pb.Vertex_String_{String_: "original"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
				}}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				live := m.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetLive()
				if live.GetKey() != "conditional" || live.GetString_() != "original" {
					t.Fatalf("conditional Put payload = %v", m.GetOp())
				}
				if v, ok := n.cache.GetVertex("conditional"); !ok || v.GetString_() != "original" {
					t.Fatalf("conditional graph effect = (%v,%v)", v, ok)
				}
			},
		},
		{
			name: "born-expired PutVertex",
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
					Key: "born-expired", Expiration: timestamppb.New(time.Now().Add(-time.Second)),
				}}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				if got := m.GetOp().GetReplicatedPutVertices().GetEntries()[0].GetCausalBarrier().GetKey(); got != "born-expired" {
					t.Fatalf("born-expired barrier key = %q", got)
				}
				if _, ok := n.cache.GetVertex("born-expired"); ok {
					t.Fatal("born-expired vertex is live")
				}
			},
		},
		{
			name:  "PutEdge",
			write: func(ctx context.Context, n *pumpNode) error { return putEdge(ctx, n, "put/a", "put/b") },
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				live := m.GetOp().GetReplicatedPutEdges().GetEntries()[0].GetLive()
				if live.GetTail() != "put/a" || live.GetHead() != "put/b" || live.GetWeight() != 3 {
					t.Fatalf("PutEdge payload = %v", m.GetOp())
				}
				if weight, ok := n.cache.GetWeight("put/a", "put/b"); !ok || weight != 3 {
					t.Fatalf("PutEdge graph effect = (%v,%v)", weight, ok)
				}
			},
		},
		{
			name: "born-expired PutEdge",
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
					Tail: "expired/a", Head: "expired/b", Weight: 3, Expiration: timestamppb.New(time.Now().Add(-time.Second)),
				}}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				barrier := m.GetOp().GetReplicatedPutEdges().GetEntries()[0].GetCausalBarrier()
				if barrier.GetTail() != "expired/a" || barrier.GetHead() != "expired/b" {
					t.Fatalf("born-expired edge barrier = %v", m.GetOp())
				}
				if _, ok := n.cache.GetWeight("expired/a", "expired/b"); ok {
					t.Fatal("born-expired edge is live")
				}
			},
		},
		{
			name: "DeleteVertex",
			seed: func(ctx context.Context, n *pumpNode) error { return putVertex(ctx, n, "delete-vertex") },
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "delete-vertex"}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				if keys := m.GetOp().GetDeleteVertices().GetKeys(); len(keys) != 1 || keys[0] != "delete-vertex" {
					t.Fatalf("DeleteVertex exact keys = %v", keys)
				}
				if _, ok := n.cache.GetVertex("delete-vertex"); ok {
					t.Fatal("DeleteVertex graph effect missing")
				}
			},
		},
		{
			name: "DeleteEdge",
			seed: func(ctx context.Context, n *pumpNode) error { return putEdge(ctx, n, "delete/a", "delete/b") },
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "delete/a", Head: "delete/b"}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				edges := m.GetOp().GetDeleteEdges().GetEdges()
				if len(edges) != 1 || edges[0].GetTail() != "delete/a" || edges[0].GetHead() != "delete/b" {
					t.Fatalf("DeleteEdge exact edges = %v", edges)
				}
				if _, ok := n.cache.GetWeight("delete/a", "delete/b"); ok {
					t.Fatal("DeleteEdge graph effect missing")
				}
			},
		},
		{
			name: "capped DeleteVerticesByPrefix",
			seed: func(ctx context.Context, n *pumpNode) error {
				if err := putVertex(ctx, n, "cap/one"); err != nil {
					return err
				}
				return putVertex(ctx, n, "cap/two")
			},
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "cap/", Limit: 1}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				keys := m.GetOp().GetDeleteVertices().GetKeys()
				if len(keys) != 1 || (keys[0] != "cap/one" && keys[0] != "cap/two") {
					t.Fatalf("capped vertex victims = %v", keys)
				}
				if _, ok := n.cache.GetVertex(keys[0]); ok {
					t.Fatal("exact vertex victim is still live")
				}
				other := "cap/one"
				if keys[0] == other {
					other = "cap/two"
				}
				if _, ok := n.cache.GetVertex(other); !ok {
					t.Fatal("capped vertex survivor was deleted")
				}
			},
		},
		{
			name: "capped DeleteEdgesByPrefix",
			seed: func(ctx context.Context, n *pumpNode) error {
				if err := putEdge(ctx, n, "edge/one", "target"); err != nil {
					return err
				}
				return putEdge(ctx, n, "edge/two", "target")
			},
			write: func(ctx context.Context, n *pumpNode) error {
				_, err := n.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "edge/", Limit: 1}))
				return err
			},
			verify: func(t *testing.T, n *pumpNode, m *pb.Mutation) {
				edges := m.GetOp().GetDeleteEdges().GetEdges()
				if len(edges) != 1 || (edges[0].GetTail() != "edge/one" && edges[0].GetTail() != "edge/two") || edges[0].GetHead() != "target" {
					t.Fatalf("capped edge victims = %v", edges)
				}
				if _, ok := n.cache.GetWeight(edges[0].GetTail(), "target"); ok {
					t.Fatal("exact edge victim is still live")
				}
				other := "edge/one"
				if edges[0].GetTail() == other {
					other = "edge/two"
				}
				if _, ok := n.cache.GetWeight(other, "target"); !ok {
					t.Fatal("capped edge survivor was deleted")
				}
			},
		},
	}
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			wal := &oneShotLocalWAL{}
			nodeID := hlc.NodeID{0xC3, byte(i + 1)}
			node := newPumpNodeWithWAL(t, nodeID, 32, true, wal)
			if err := putVertex(ctx, node, "stream/warmup"); err != nil {
				t.Fatal(err)
			}
			if tc.seed != nil {
				if err := tc.seed(ctx, node); err != nil {
					t.Fatal(err)
				}
			}
			beforeOrigin := node.svc.LocalSeq(nodeID)
			beforeLocal, _ := node.log.LastSeq()
			rep := newReplicationRawClient(t, node.url)
			oldFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: beforeLocal}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = oldFeed.Close() }()
			if !oldFeed.Receive() || oldFeed.Msg().GetMutation().GetSeq() != beforeOrigin {
				t.Fatalf("old Subscribe warmup = (%v,%v)", oldFeed.Msg(), oldFeed.Err())
			}
			wal.armed.Store(true)
			if code := connect.CodeOf(tc.write(ctx, node)); code != connect.CodeUnavailable {
				t.Fatalf("failed local write code = %v, want Unavailable", code)
			}
			if wal.failures.Load() != 1 || node.svc.LocalSeq(nodeID) != beforeOrigin || node.log.Len() != int(beforeLocal) {
				t.Fatalf("failed publication: WAL failures=%d origin seq=%d log len=%d", wal.failures.Load(), node.svc.LocalSeq(nodeID), node.log.Len())
			}
			if tc.name == "DeleteEdge" {
				assertGraphReadsGapped(t, ctx, node)
			}
			if oldFeed.Receive() || connect.CodeOf(oldFeed.Err()) != connect.CodeFailedPrecondition || !strings.Contains(oldFeed.Err().Error(), "gapped") {
				t.Fatalf("old Subscribe after fault = (%v,%v), want gapped", oldFeed.Msg(), oldFeed.Err())
			}
			newFeed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: beforeLocal + 1}))
			if err == nil {
				if newFeed.Receive() || connect.CodeOf(newFeed.Err()) != connect.CodeFailedPrecondition {
					t.Fatalf("new Subscribe during fault = (%v,%v)", newFeed.Msg(), newFeed.Err())
				}
				_ = newFeed.Close()
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("new Subscribe error = %v", err)
			}
			snapshot, err := rep.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
			if err == nil {
				if snapshot.Receive() || connect.CodeOf(snapshot.Err()) != connect.CodeFailedPrecondition {
					t.Fatalf("Snapshot during fault = (%v,%v)", snapshot.Msg(), snapshot.Err())
				}
				_ = snapshot.Close()
			} else if connect.CodeOf(err) != connect.CodeFailedPrecondition {
				t.Fatalf("Snapshot during fault error = %v", err)
			}
			if err := putVertex(ctx, node, "repair/marker"); err != nil {
				t.Fatalf("repairing next PutVertex: %v", err)
			}
			if got := node.svc.LocalSeq(nodeID); got != beforeOrigin+2 {
				t.Fatalf("repaired origin seq = %d, want %d", got, beforeOrigin+2)
			}
			if got := node.log.Len(); got != int(beforeLocal)+2 {
				t.Fatalf("repaired local log len = %d, want %d", got, beforeLocal+2)
			}
			feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: beforeLocal + 1}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = feed.Close() }()
			if !feed.Receive() {
				t.Fatalf("repaired pending mutation missing: %v", feed.Err())
			}
			pending := feed.Msg().GetMutation()
			if pending.GetSeq() != beforeOrigin+1 {
				t.Fatalf("pending seq = %d, want %d", pending.GetSeq(), beforeOrigin+1)
			}
			tc.verify(t, node, pending)
			if tc.name == "DeleteEdge" {
				if _, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "delete/a", Head: "delete/b"})); connect.CodeOf(err) != connect.CodeNotFound {
					t.Fatalf("graph read after local repair = %v, want NotFound", err)
				}
			}
			if !feed.Receive() || feed.Msg().GetMutation().GetSeq() != beforeOrigin+2 {
				t.Fatalf("next mutation = (%v,%v), want seq %d", feed.Msg(), feed.Err(), beforeOrigin+2)
			}
			healthySnapshot, err := rep.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = healthySnapshot.Close() }()
			if !healthySnapshot.Receive() || healthySnapshot.Msg().GetHeader().GetCutoffSeqPerOrigin()[hex.EncodeToString(nodeID[:])] != beforeOrigin+2 {
				t.Fatalf("repaired Snapshot header = (%v,%v)", healthySnapshot.Msg(), healthySnapshot.Err())
			}
		})
	}
}

func TestGraphReadPublicationCut_BlockedWALThenFault(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping h2c publication cut test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	wal := &blockedFailureWAL{entered: make(chan struct{}), release: make(chan struct{})}
	released := false
	defer func() {
		if !released {
			close(wal.release)
		}
	}()
	n := newPumpNodeWithWAL(t, hlc.NodeID{0xD1}, 32, true, wal)
	if _, err := n.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "cut/a", Head: "cut/b", Weight: 1, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
	}})); err != nil {
		t.Fatal(err)
	}
	beforeSeq := n.svc.LocalSeq(n.nodeID)
	beforeLen := n.log.Len()
	wal.armed.Store(true)
	writeDone := make(chan error, 1)
	go func() {
		_, err := n.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "cut/a", Head: "cut/b"}))
		writeDone <- err
	}()
	select {
	case <-wal.entered:
	case <-ctx.Done():
		t.Fatal("DeleteEdge never reached the blocked WAL")
	}
	readDone := make(chan error, 1)
	go func() {
		_, err := n.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "cut/a", Head: "cut/b"}))
		readDone <- err
	}()
	select {
	case err := <-readDone:
		t.Fatalf("read crossed an in-flight publication cut: %v", err)
	case <-time.After(80 * time.Millisecond):
	}
	close(wal.release)
	released = true
	select {
	case err := <-writeDone:
		if connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatalf("failed DeleteEdge = %v, want Unavailable", err)
		}
	case <-ctx.Done():
		t.Fatal("failed DeleteEdge did not return")
	}
	select {
	case err := <-readDone:
		if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
			t.Fatalf("read after failed publication = %v, want gapped FailedPrecondition", err)
		}
	case <-ctx.Done():
		t.Fatal("blocked GetEdge did not return after WAL failure")
	}
	if got := n.svc.LocalSeq(n.nodeID); got != beforeSeq || n.log.Len() != beforeLen {
		t.Fatalf("failed publication changed origin/log cut: seq=%d log=%d, want %d/%d", got, n.log.Len(), beforeSeq, beforeLen)
	}
	if _, err := n.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "repair", Value: &pb.Vertex_String_{String_: "done"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
	}})); err != nil {
		t.Fatalf("repairing write: %v", err)
	}
	if _, err := n.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "cut/a", Head: "cut/b"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("read after repair = %v, want NotFound", err)
	}
}

// TestPeerPump_SearchPartitionHealConvergence starts a follower only after a
// production-search corpus has changed on the primary, then exercises the live
// Subscribe tail across overwrite, singular/batch/prefix delete, tombstones,
// TTL, structured/typed values, and implicit edge endpoints. Homogeneous
// replicas must converge to identical key order and score bits.
func TestPeerPump_SearchPartitionHealConvergence(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping search partition-heal test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	primary := newPumpNode(t, hlc.NodeID{0xD1})
	follower := newPumpNode(t, hlc.NodeID{0xD2})
	if _, err := primary.sdk.PutVertices(ctx, []client.VertexInput{
		{Key: "search/keep", Value: "common lantern alpha", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/overwrite", Value: "common retiredterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-one", Value: "common deletedterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-batch-a", Value: "common batchterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-batch-b", Value: "common batchterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-prefix/a", Value: "common prefixterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/delete-prefix/b", Value: "common prefixterm", Expiration: time.Now().Add(time.Hour)},
		{Key: "search/json", Value: `{"title":"common structuredvalue","ignored":42}`, Expiration: time.Now().Add(time.Hour)},
		{Key: "search/int", Value: int64(4242), Expiration: time.Now().Add(time.Hour)},
		{Key: "search/ttl", Value: "common expiredterm", Expiration: time.Now().Add(250 * time.Millisecond)},
	}); err != nil {
		t.Fatalf("seed PutVertices: %v", err)
	}
	if _, err := primary.sdk.AddEdge(ctx, "search/implicit-tail", "search/implicit-head", 1, time.Hour); err != nil {
		t.Fatalf("seed implicit endpoints: %v", err)
	}
	// A born-expired input is an accepted delete-like overwrite. This absent
	// key remains absent on both replicas and must never enter the derived
	// index, while the SDK still exposes the authoritative EXPIRED outcome.
	outcome, err := primary.sdk.PutVertexAt(ctx, "search/born-expired", "bornexpiredterm", time.Now().Add(-time.Second))
	if err != nil {
		t.Fatalf("born-expired PutVertexAt: %v", err)
	}
	if outcome != client.PutOutcomeExpired {
		t.Fatalf("born-expired PutVertexAt outcome = %s, want EXPIRED", outcome)
	}

	// The follower was partitioned for every seed mutation. Starting its pump
	// now heals through the retained Subscribe tail rather than shared memory.
	follower.startPump(ctx, t, []string{primary.url})
	waitForSearchConvergence(t, ctx, "common", nil, primary.raw, follower.raw)

	if _, err := primary.sdk.PutVertex(ctx, "search/overwrite", "common currentterm", time.Hour); err != nil {
		t.Fatalf("overwrite: %v", err)
	}
	if _, err := primary.sdk.DeleteVertex(ctx, "search/delete-one"); err != nil {
		t.Fatalf("DeleteVertex: %v", err)
	}
	if _, err := primary.sdk.DeleteVertices(ctx, []string{"search/delete-batch-a", "search/delete-batch-b"}); err != nil {
		t.Fatalf("DeleteVertices: %v", err)
	}
	if _, err := primary.sdk.DeleteVerticesByPrefix(ctx, "search/delete-prefix/"); err != nil {
		t.Fatalf("DeleteVerticesByPrefix: %v", err)
	}
	time.Sleep(300 * time.Millisecond)

	exactAll := &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}
	for _, retired := range []string{"retiredterm", "deletedterm", "batchterm", "prefixterm", "expiredterm", "bornexpiredterm"} {
		if got := waitForSearchConvergence(t, ctx, retired, exactAll, primary.raw, follower.raw); len(got) != 0 {
			t.Errorf("retired query %q still matched: %+v", retired, got)
		}
	}
	for _, live := range []string{"currentterm", "structuredvalue", "4242", "implicit"} {
		if got := waitForSearchConvergence(t, ctx, live, exactAll, primary.raw, follower.raw); len(got) == 0 {
			t.Errorf("live query %q returned no hits", live)
		}
	}
	if got := waitForSearchConvergence(t, ctx, "common", exactAll, primary.raw, follower.raw); len(got) != 3 {
		t.Fatalf("final common corpus = %+v, want three live documents", got)
	}
}

func TestPeerPump_DeletePrefixKeepsOriginDeadlineAcrossWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	origin := newPumpNode(t, hlc.NodeID{0xD5})
	follower := newPumpNode(t, hlc.NodeID{0xD6})
	if _, err := origin.sdk.PutVertex(ctx, "deadline/vertex", "value", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := origin.sdk.PutEdge(ctx, "deadline/tail", "deadline/head", 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	before, ok := origin.log.LastSeq()
	if !ok {
		t.Fatal("seed did not publish")
	}
	if _, err := origin.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "deadline/vertex"})); err != nil {
		t.Fatalf("DeleteVerticesByPrefix over h2c: %v", err)
	}
	if _, err := origin.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "deadline/tail"})); err != nil {
		t.Fatalf("DeleteEdgesByPrefix over h2c: %v", err)
	}
	feed, err := newReplicationRawClient(t, origin.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: before + 1}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	for i := 0; i < 2; i++ {
		if !feed.Receive() {
			t.Fatalf("Delete mutation %d missing from h2c Subscribe: %v", i, feed.Err())
		}
		mutation := feed.Msg().GetMutation()
		if mutation == nil || mutation.GetTombstoneExpiration() == nil || mutation.GetTombstoneExpiration().CheckValid() != nil {
			t.Fatalf("Delete mutation %d has no valid absolute deadline: %+v", i, mutation)
		}
	}
	originTombstones := origin.cache.SnapshotReplication().Tombstones
	if len(originTombstones.Vertices) != 1 || len(originTombstones.Edges) != 1 {
		t.Fatalf("origin tombstones = %+v", originTombstones)
	}
	follower.startPump(ctx, t, []string{origin.url})
	wantSeq := origin.svc.LocalSeq(origin.nodeID)
	for follower.svc.LocalSeq(origin.nodeID) != wantSeq {
		select {
		case <-ctx.Done():
			t.Fatalf("follower did not apply Delete mutations: %v", ctx.Err())
		case <-time.After(10 * time.Millisecond):
		}
	}
	followerTombstones := follower.cache.SnapshotReplication().Tombstones
	if len(followerTombstones.Vertices) != 1 || len(followerTombstones.Edges) != 1 ||
		!followerTombstones.Vertices[0].Expiration.Equal(originTombstones.Vertices[0].Expiration) ||
		!followerTombstones.Edges[0].Expiration.Equal(originTombstones.Edges[0].Expiration) {
		t.Fatalf("follower renewed or lost Delete deadlines: origin=%+v follower=%+v", originTombstones, followerTombstones)
	}
}

func TestPeerPump_ForgedDeleteDeadlineRejectedAcrossWire(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	origin := newPumpNode(t, hlc.NodeID{0xD7})
	follower := newPumpNode(t, hlc.NodeID{0xD8})
	forgedWall := time.Now().Add(2 * time.Hour)
	mutation := &pb.Mutation{
		Origin: origin.nodeID[:], Seq: 1,
		Hlc: &pb.HLCTimestamp{WallNs: forgedWall.UnixNano(), NodeId: origin.nodeID[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertex{
			DeleteVertex: &pb.DeleteVertexRequest{Key: "forged/deadline"},
		}},
		TombstoneExpiration: timestamppb.New(forgedWall.Add(time.Hour)),
	}
	if _, err := origin.log.Append(mutation, hlc.Timestamp{WallNs: forgedWall.UnixNano(), NodeID: origin.nodeID}); err != nil {
		t.Fatal(err)
	}
	feed, err := newReplicationRawClient(t, origin.url).Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() {
		t.Fatalf("forged Delete was not delivered over h2c: %v", feed.Err())
	}
	if err := follower.svc.ApplyMutation(ctx, feed.Msg().GetMutation()); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("forged future Delete deadline applied: %v", err)
	}
	if seq := follower.svc.LocalSeq(origin.nodeID); seq != 0 || follower.log.Len() != 0 {
		t.Fatalf("forged Delete advanced follower origin/log to %d/%d", seq, follower.log.Len())
	}
	if tombs := follower.cache.SnapshotReplication().Tombstones; len(tombs.Vertices) != 0 {
		t.Fatalf("forged Delete installed a tombstone: %+v", tombs.Vertices)
	}
}

func TestPeerPump_SearchConfigMismatchBlocksReadiness(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xE1}, 1024, true)
	follower := newPumpNodeWithSearch(t, hlc.NodeID{0xE2}, 1024, false)
	if _, err := primary.sdk.PutVertex(ctx, "mismatch-proof", "graph still converges", time.Hour); err != nil {
		t.Fatalf("primary PutVertex: %v", err)
	}

	peerStatus, err := newReplicationRawClient(t, primary.url).PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
	if err != nil {
		t.Fatalf("PeerStatus: %v", err)
	}
	remoteFingerprint := peerStatus.Msg.GetSearchConfigFingerprint()
	if remoteFingerprint == "" || remoteFingerprint != primary.svc.SearchConfigFingerprint() {
		t.Fatalf("peer search fingerprint = %q, primary = %q", remoteFingerprint, primary.svc.SearchConfigFingerprint())
	}
	if remoteFingerprint == follower.svc.SearchConfigFingerprint() {
		t.Fatal("positions mismatch produced identical search fingerprints")
	}

	gate := readiness.NewGate(100, true, nil)
	metrics := &observedSearchConfigMetrics{gate: gate, observations: make(chan bool, 1)}
	follower.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)
	select {
	case matched := <-metrics.observations:
		if matched {
			t.Fatal("pump reported mismatched search configs as compatible")
		}
	case <-ctx.Done():
		t.Fatal("pump did not publish search config comparison")
	}
	if !waitForVertex(t, follower.cache, "mismatch-proof", 3*time.Second) {
		t.Fatal("graph replication stopped on a search config mismatch")
	}
	if gate.Ready() {
		t.Fatal("heterogeneous search config masqueraded as ready")
	}
}

// TestPeerPump_EmptyPeers_NoOp asserts that an empty peer list yields
// a Pump.Run that returns immediately, so the single-instance
// deployment path remains a no-op (no goroutine leak, no dial
// attempts, no error).
func TestPeerPump_EmptyPeers_NoOp(t *testing.T) {
	n := newPumpNode(t, hlc.NodeID{0x01})
	p := replication.NewPump(replication.Config{NodeID: n.nodeID}, n.svc, n.cache)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- p.Run(ctx) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Pump.Run returned err for empty peers: %v", err)
		}
	case <-time.After(1 * time.Second):
		t.Fatal("Pump.Run did not return promptly for empty peers")
	}
}

// TestPeerPump_GapRecoverySnapshot verifies that a follower attaching
// to a primary that has already advanced past the follower's known
// next-seq triggers the snapshot-then-resubscribe fallback path. This
// indirectly exercises Pump.session's FailedPrecondition handler
// (#184/#185 stitching).
func TestPeerPump_GapRecoverySnapshot(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping snapshot-recovery test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xA1}, 4, true)
	// Pre-populate the primary BEFORE the follower attaches. With a
	// small ring buffer the follower's effective request (empty
	// cursor → server starts at oldest = 1) will be below firstSeq
	// → service returns FailedPrecondition reason=gapped → Pump runs
	// a snapshot to catch up, then resubscribes.
	for i := 0; i < 16; i++ {
		if _, err := primary.sdk.PutVertex(ctx, fmt.Sprintf("pre-%d", i), "v", time.Minute); err != nil {
			t.Fatalf("primary.PutVertex[%d]: %v", i, err)
		}
	}
	// These accepted-expired HLC20 overwrites have no live payload and no
	// retained log record in this storage-level fixture. Gap recovery therefore
	// has to bootstrap their explicit causal-barrier frames from Snapshot.
	barrierTS := hlc.Timestamp{WallNs: 20, NodeID: hlc.NodeID{0xB0}}
	if !primary.cache.PutVertexWithExpirationHLC(
		"snapshot-barrier-vertex", &pb.Vertex{Key: "snapshot-barrier-vertex"},
		time.Now().Add(-time.Hour), barrierTS,
	) {
		t.Fatal("primary accepted-expired vertex barrier was rejected")
	}
	if !primary.cache.PutEdgeWithExpirationHLC(
		"snapshot-barrier-tail", "snapshot-barrier-head", 1,
		time.Now().Add(-time.Hour), barrierTS,
	) {
		t.Fatal("primary accepted-expired edge barrier was rejected")
	}

	follower := newPumpNode(t, hlc.NodeID{0xA2})
	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	follower.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)

	// Snapshot should replay all 16 pre-existing vertices.
	for i := 0; i < 16; i++ {
		key := fmt.Sprintf("pre-%d", i)
		if !waitForVertex(t, follower.cache, key, 5*time.Second) {
			t.Errorf("follower did not receive %q via snapshot fallback", key)
		}
	}

	// After the snapshot, a fresh write on the primary must arrive
	// via the resubscribed stream — proves that Pump correctly
	// resubscribed after the snapshot bootstrap (#415, B-4: cutoff is
	// per-origin and lives in the server's watermark tracker; the
	// pump just sends an empty cursor again and the local
	// ApplyMutation CAS dedups whatever the snapshot already covered).
	if _, err := primary.sdk.PutVertex(ctx, "post-snapshot", "v", time.Minute); err != nil {
		t.Fatalf("primary.PutVertex post: %v", err)
	}
	if !waitForVertex(t, follower.cache, "post-snapshot", 3*time.Second) {
		t.Errorf("follower did not receive post-snapshot vertex via resubscribe")
	}
	time.Sleep(100 * time.Millisecond)
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("snapshot replay count = %d, want exactly 1 before live tail", got)
	}
	olderTS := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0xA0}}
	if follower.cache.PutVertexWithExpirationHLC(
		"snapshot-barrier-vertex", &pb.Vertex{Key: "snapshot-barrier-vertex"},
		time.Now().Add(time.Hour), olderTS,
	) {
		t.Fatal("pump bootstrap lost vertex barrier: HLC10 live Put was accepted")
	}
	if follower.cache.PutEdgeWithExpirationHLC(
		"snapshot-barrier-tail", "snapshot-barrier-head", 9,
		time.Now().Add(time.Hour), olderTS,
	) {
		t.Fatal("pump bootstrap lost edge barrier: HLC10 live Put was accepted")
	}
	if _, ok := follower.cache.GetVertex("snapshot-barrier-vertex"); ok {
		t.Fatal("vertex barrier became visible after pump bootstrap")
	}
	if _, _, ok := follower.cache.GetEdgeDetail("snapshot-barrier-tail", "snapshot-barrier-head"); ok {
		t.Fatal("edge barrier became visible after pump bootstrap")
	}
	if _, ok := follower.cache.GetVertex("snapshot-barrier-tail"); ok {
		t.Fatal("edge barrier materialized its tail endpoint during pump bootstrap")
	}
	if _, ok := follower.cache.GetVertex("snapshot-barrier-head"); ok {
		t.Fatal("edge barrier materialized its head endpoint during pump bootstrap")
	}
	waitForSearchConvergence(t, ctx, "pre", nil, primary.raw, follower.raw)
	waitForSearchConvergence(t, ctx, "post snapshot", nil, primary.raw, follower.raw)
	exactAll := &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}
	if got := waitForSearchConvergence(t, ctx, "snapshot barrier", exactAll, primary.raw, follower.raw); len(got) != 0 {
		t.Fatalf("causal barrier identities became searchable: %+v", got)
	}
}

// A peer can advance an origin after capturing its Snapshot while the
// receiver already has that origin from another peer. The resumed stream
// must not request a prefix that the receiver has committed and the source
// has since evicted.
func TestPeerPump_SnapshotResumesFromLatestCommittedOriginOverH2C(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	source := newPumpNodeWithSearch(t, hlc.NodeID{0xA1}, 4, true)
	other := newPumpNodeWithSearch(t, hlc.NodeID{0xA3}, 64, true)
	receiver := newPumpNode(t, hlc.NodeID{0xA2})
	for i := 0; i < 8; i++ {
		if _, err := other.sdk.PutVertex(ctx, fmt.Sprintf("other-%d", i), "value", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	receiver.startPump(ctx, t, []string{other.url})
	waitForOrigin := func(node *pumpNode, want uint64) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for node.svc.LocalSeq(other.nodeID) < want && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
		if got := node.svc.LocalSeq(other.nodeID); got != want {
			t.Fatalf("origin on node %x = %d, want %d", node.nodeID, got, want)
		}
	}
	waitForOrigin(receiver, 8)

	for i := 0; i < 16; i++ {
		if _, err := source.sdk.PutVertex(ctx, fmt.Sprintf("source-%d", i), "value", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	upstream := newReplicationRawClient(t, source.url)
	assertGap := func(cursor map[string]uint64) {
		t.Helper()
		tail, err := upstream.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
			FromSeqPerOrigin: cursor,
		}))
		if err == nil {
			if tail.Receive() {
				t.Fatal("Subscribe delivered a mutation despite missing retained history")
			}
			err = tail.Err()
			_ = tail.Close()
		}
		if err == nil || connect.CodeOf(err) != connect.CodeFailedPrecondition ||
			!strings.Contains(err.Error(), "gapped: origin") {
			t.Fatalf("Subscribe(%v) = %v, want a real retained-origin gap", cursor, err)
		}
	}
	assertGap(map[string]uint64{hex.EncodeToString(other.nodeID[:]): 9})
	snapshot, err := upstream.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
	}))
	if err != nil {
		t.Fatal(err)
	}
	var frames []*pb.SnapshotResponse
	for snapshot.Receive() {
		frames = append(frames, proto.Clone(snapshot.Msg()).(*pb.SnapshotResponse))
	}
	if err := snapshot.Err(); err != nil && !errors.Is(err, io.EOF) {
		t.Fatal(err)
	}
	if err := snapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if len(frames) < 2 || frames[0].GetHeader() == nil ||
		frames[0].GetHeader().GetCutoffSeqPerOrigin()[hex.EncodeToString(other.nodeID[:])] != 0 {
		t.Fatal("source Snapshot unexpectedly included the other origin")
	}

	headerSent := make(chan struct{}, 1)
	releaseBody := make(chan struct{})
	resumed := make(chan *pb.SubscribeRequest, 1)
	peer := &scriptedGraphSnapshotPeer{frames: frames}
	peer.afterSend = func(ctx context.Context, frame *pb.SnapshotResponse) error {
		if frame.GetHeader() == nil {
			return nil
		}
		select {
		case headerSent <- struct{}{}:
		default:
		}
		select {
		case <-releaseBody:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	peer.afterFirstSubscribe = func(ctx context.Context, req *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
		select {
		case resumed <- proto.Clone(req.Msg).(*pb.SubscribeRequest):
		default:
		}
		tail, err := upstream.Subscribe(ctx, connect.NewRequest(req.Msg))
		if err != nil {
			return err
		}
		defer func() { _ = tail.Close() }()
		for tail.Receive() {
			if err := stream.Send(proto.Clone(tail.Msg()).(*pb.SubscribeResponse)); err != nil {
				return err
			}
		}
		return tail.Err()
	}
	mux := http.NewServeMux()
	mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
	proxy := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetUnencryptedHTTP2(true)
	proxy.Config.Protocols = protocols
	proxy.Start()
	t.Cleanup(proxy.Close)

	receiver.startPump(ctx, t, []string{proxy.URL})
	select {
	case <-headerSent:
	case <-ctx.Done():
		t.Fatal("receiver did not request a Snapshot after missing source history")
	}
	source.startPump(ctx, t, []string{other.url})
	waitForOrigin(source, 8)
	assertGap(map[string]uint64{hex.EncodeToString(source.nodeID[:]): 17})
	close(releaseBody)

	select {
	case request := <-resumed:
		cursor := request.GetFromSeqPerOrigin()
		if got := cursor[hex.EncodeToString(other.nodeID[:])]; got != 9 {
			t.Fatalf("resumed other-origin cursor = %d, want committed next seq 9", got)
		}
		if got := cursor[hex.EncodeToString(source.nodeID[:])]; got != 17 {
			t.Fatalf("resumed source-origin cursor = %d, want Snapshot next seq 17", got)
		}
	case <-ctx.Done():
		t.Fatal("receiver did not resume after Snapshot")
	}
	if _, err := source.sdk.PutVertex(ctx, "post-snapshot-live-tail", "value", time.Minute); err != nil {
		t.Fatal(err)
	}
	if !waitForVertex(t, receiver.cache, "post-snapshot-live-tail", 3*time.Second) ||
		!waitForVertex(t, receiver.cache, "other-7", 3*time.Second) {
		t.Fatal("receiver lost a committed origin or failed to consume the live tail")
	}
	if got := peer.snapshotCalls.Load(); got != 1 {
		t.Fatalf("Snapshot requests = %d, want exactly one after retained-vector resume", got)
	}
}

func TestPeerPump_ActiveLocalSnapshotDefersCompetingRepairOnRealWire(t *testing.T) {
	for _, verified := range []bool{true, false} {
		name := "verified install"
		if !verified {
			name = "failed install needs repair"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			source := newPumpNode(t, hlc.NodeID{0xA3})
			receiver := newPumpNode(t, hlc.NodeID{0xA4})
			key := fmt.Sprintf("installed-%t", verified)
			if _, err := source.sdk.PutVertex(ctx, key, "value", time.Minute); err != nil {
				t.Fatal(err)
			}
			finish, err := receiver.svc.BeginSnapshotInstall()
			if err != nil {
				t.Fatal(err)
			}
			released := false
			defer func() {
				if !released {
					finish(false)
				}
			}()
			metrics := &observedSearchConfigMetrics{
				gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
			}
			receiver.startPumpWithMetrics(ctx, t, []string{source.url}, metrics)
			for {
				rows := receiver.pump.Snapshot()
				if len(rows) == 1 && rows[0].State == replication.PeerStateBackoff &&
					strings.Contains(rows[0].LastError, "gapped") {
					if strings.Contains(rows[0].LastError, "Snapshot install already in progress") ||
						metrics.snapshots.Load() != 0 {
						t.Fatalf("competing Snapshot during active install: %+v", rows[0])
					}
					break
				}
				select {
				case <-ctx.Done():
					t.Fatalf("pump did not observe active local install: %+v", rows)
				case <-time.After(5 * time.Millisecond):
				}
			}
			finish(verified)
			released = true
			if !verified {
				// Private cache visibility can precede public Snapshot publication.
				deadline := time.Now().Add(5 * time.Second)
				for metrics.snapshots.Load() == 0 && time.Now().Before(deadline) {
					select {
					case <-ctx.Done():
						t.Fatal("failed local install did not trigger verified Snapshot repair")
					case <-time.After(5 * time.Millisecond):
					}
				}
				if metrics.snapshots.Load() == 0 {
					t.Fatal("failed local install did not trigger verified Snapshot repair")
				}
			}
			if !waitForVertex(t, receiver.cache, key, 5*time.Second) {
				t.Fatal("receiver did not apply source mutation after install")
			}
			resp, err := receiver.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: key}))
			if err != nil || resp.Msg.GetVertex().GetString_() != "value" {
				t.Fatalf("verified source value after retry = (%v, %v)", resp, err)
			}
			if verified && metrics.snapshots.Load() != 0 {
				t.Fatalf("verified local install triggered redundant Snapshot: %d", metrics.snapshots.Load())
			}
		})
	}
}

type observedSnapshotInstall struct {
	finished chan bool
	resume   chan struct{}
	started  time.Time
	ended    time.Time
}

type observedSnapshotApplier struct {
	*service.LanternService
	ctx        context.Context
	began      chan *observedSnapshotInstall
	afterApply func(*pb.Mutation, error)
}

func (a *observedSnapshotApplier) ApplyMutation(ctx context.Context, mutation *pb.Mutation) error {
	err := a.LanternService.ApplyMutation(ctx, mutation)
	if a.afterApply != nil {
		a.afterApply(mutation, err)
	}
	return err
}

func (a *observedSnapshotApplier) BeginSnapshotInstall() (func(bool), error) {
	finish, err := a.LanternService.BeginSnapshotInstall()
	if err != nil {
		return nil, err
	}
	stage := &observedSnapshotInstall{finished: make(chan bool, 1), resume: make(chan struct{}), started: time.Now()}
	select {
	case a.began <- stage:
	case <-a.ctx.Done():
		finish(false)
		return nil, a.ctx.Err()
	}
	return func(verified bool) {
		finish(verified)
		stage.ended = time.Now()
		stage.finished <- verified
		select {
		case <-stage.resume:
		case <-a.ctx.Done():
		}
	}, nil
}

type observedSnapshotHeader struct {
	header  *pb.SnapshotHeader
	release chan error
}

type observedSnapshotPeer struct {
	graphv1connect.LanternReplicationServiceHandler
	source    *service.LanternReplicationService
	headers   chan observedSnapshotHeader
	snapshots atomic.Int32
	gaps      atomic.Int32
	results   chan error
}

func (p *observedSnapshotPeer) Subscribe(ctx context.Context, req *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
	err := p.LanternReplicationServiceHandler.Subscribe(ctx, req, stream)
	if connect.CodeOf(err) == connect.CodeFailedPrecondition && strings.Contains(err.Error(), "gapped: origin") {
		p.gaps.Add(1)
	}
	return err
}

func (p *observedSnapshotPeer) Snapshot(ctx context.Context, req *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	p.snapshots.Add(1)
	ctx = service.WithReplicationTailMetadata(ctx, req.Header().Values(service.SnapshotTailHeader), stream.ResponseHeader())
	err := p.source.Snapshot(ctx, req.Msg, observedSnapshotSender{ctx: ctx, stream: stream, headers: p.headers})
	if p.results != nil {
		select {
		case p.results <- err:
		case <-ctx.Done():
		}
	}
	return err
}

type observedSnapshotSender struct {
	ctx     context.Context
	stream  *connect.ServerStream[pb.SnapshotResponse]
	headers chan<- observedSnapshotHeader
}

func (s observedSnapshotSender) Send(frame *pb.SnapshotResponse) error {
	if err := s.stream.Send(frame); err != nil {
		return err
	}
	if frame.GetHeader() == nil {
		return nil
	}
	header := observedSnapshotHeader{header: proto.Clone(frame.GetHeader()).(*pb.SnapshotHeader), release: make(chan error, 1)}
	select {
	case s.headers <- header:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	select {
	case err := <-header.release:
		return err
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

func TestPeerPump_SnapshotRetentionDuringInstall(t *testing.T) {
	for _, tc := range []struct {
		name      string
		cycles    int
		advance   int
		interrupt bool
		keepsTail bool
	}{
		{name: "retained tail", cycles: 1, advance: 2, keepsTail: true},
		{name: "evicted tail", cycles: 3, advance: 8},
		{name: "interrupted stream control", cycles: 2, advance: 2, interrupt: true, keepsTail: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			source := newPumpNodeWithSearch(t, hlc.NodeID{0xC1}, 4, false)
			receiver := newPumpNode(t, hlc.NodeID{0xC2})
			put := func(key string) {
				t.Helper()
				if _, err := source.sdk.PutVertex(ctx, key, "value", time.Minute); err != nil {
					t.Fatal(err)
				}
			}
			for index := range 8 {
				put(fmt.Sprintf("seed-%d", index))
			}
			rep := service.NewLanternReplicationService(source.log, source.cache, source.clock).WithOriginStates(source.svc)
			peer := &observedSnapshotPeer{
				LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(rep),
				source:                           rep, headers: make(chan observedSnapshotHeader, 1), results: make(chan error, 1),
			}
			mux := http.NewServeMux()
			mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
			server := httptest.NewUnstartedServer(mux)
			protocols := new(http.Protocols)
			protocols.SetUnencryptedHTTP2(true)
			server.Config.Protocols = protocols
			server.Start()
			t.Cleanup(server.Close)
			apply := &observedSnapshotApplier{LanternService: receiver.svc, ctx: ctx, began: make(chan *observedSnapshotInstall, 1)}
			pump := replication.NewPump(replication.Config{
				NodeID: receiver.nodeID, Peers: []string{server.URL}, HTTPClient: h2cClient(),
				BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
			}, apply, receiver.cache)
			done := make(chan error, 1)
			go func() { done <- pump.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(time.Second):
					t.Error("owned Pump did not join")
				}
			}()
			for cycle := range tc.cycles {
				var header observedSnapshotHeader
				var stage *observedSnapshotInstall
				select {
				case header = <-peer.headers:
				case <-ctx.Done():
					t.Fatal("Snapshot header not observed", ctx.Err())
				}
				if receiver.svc.SnapshotInstallActive() || receiver.svc.SnapshotRepairRequired() {
					t.Fatal("incomplete Snapshot changed healthy publication")
				}
				if _, err := receiver.raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{Keys: []string{"seed-0"}})); err != nil {
					t.Fatalf("healthy read during Snapshot collection = %v", err)
				}
				interrupted := tc.interrupt && cycle == 0
				if !interrupted {
					for index := range tc.advance {
						put(fmt.Sprintf("tail-%d-%d", cycle, index))
					}
				}
				var end error
				if interrupted {
					end = connect.NewError(connect.CodeUnavailable, errors.New("controlled Snapshot stream interruption"))
				}
				header.release <- end
				select {
				case err := <-peer.results:
					if interrupted && connect.CodeOf(err) != connect.CodeUnavailable || !interrupted && err != nil {
						t.Fatalf("Snapshot source interrupted=%t: %v", interrupted, err)
					}
				case <-ctx.Done():
					t.Fatal("Snapshot source completion not observed", ctx.Err())
				}
				if interrupted {
					select {
					case stage := <-apply.began:
						close(stage.resume)
						t.Fatal("interrupted Snapshot entered live replay")
					default:
					}
					if receiver.svc.SnapshotRepairRequired() || receiver.svc.SnapshotInstallActive() || receiver.svc.LocalSeq(source.nodeID) != 0 {
						t.Fatal("interrupted collection changed the receiver or publication")
					}
					t.Logf("snapshot_rejected cycle=%d captured=%d live_begin=false receiver_healthy=true", cycle+1, header.header.GetCutoffLocalSeq())
					continue
				}
				select {
				case stage = <-apply.began:
				case <-ctx.Done():
					t.Fatal("verified Snapshot admission not observed", ctx.Err())
				}
				t.Logf("snapshot_begin cycle=%d utc=%s", cycle+1, stage.started.UTC().Format(time.RFC3339Nano))
				var verified bool
				select {
				case verified = <-stage.finished:
				case <-ctx.Done():
					t.Fatal("Snapshot completion not observed", ctx.Err())
				}
				if !verified {
					t.Fatal("complete Snapshot did not verify")
				}
				_, err := receiver.raw.GetVertices(ctx, connect.NewRequest(&pb.GetVerticesRequest{Keys: []string{"seed-0"}}))
				if verified && err != nil || !verified && connect.CodeOf(err) != connect.CodeFailedPrecondition {
					t.Fatalf("read after verified=%t = %v", verified, err)
				}
				publicRead := "OK"
				if err != nil {
					publicRead = connect.CodeOf(err).String()
				}
				t.Logf("snapshot_end cycle=%d utc=%s span=%s captured=%d source=%d receiver=%d verified=%t public_read=%s retained_gaps=%d", cycle+1,
					stage.ended.UTC().Format(time.RFC3339Nano), stage.ended.Sub(stage.started), header.header.GetCutoffSeqPerOrigin()[hex.EncodeToString(source.nodeID[:])], source.svc.LocalSeq(source.nodeID), receiver.svc.LocalSeq(source.nodeID), verified, publicRead, peer.gaps.Load())
				if !tc.keepsTail && cycle == tc.cycles-1 {
					cancel()
				}
				close(stage.resume)
			}
			if tc.keepsTail && !waitForVertex(t, receiver.cache, fmt.Sprintf("tail-%d-%d", tc.cycles-1, tc.advance-1), time.Second) {
				t.Fatal("retained tail did not converge after verified Snapshot")
			}
			if got := peer.snapshots.Load(); got != int32(tc.cycles) {
				t.Fatalf("Snapshot requests=%d, want %d", got, tc.cycles)
			}
			if !tc.keepsTail && peer.gaps.Load() < int32(tc.cycles-1) {
				t.Fatal("repeated Snapshots were not driven by actual retained-origin gaps")
			}
		})
	}
}

// Identical native wire traffic exposes the retention boundary with and without
// a reservation. Complete counted Snapshot framing is checked before either
// Subscribe; the Pump test below additionally proves verified installation.
func TestPeerPump_SnapshotTailWireContinuity(t *testing.T) {
	for _, reserve := range []bool{false, true} {
		name := "unreserved gap"
		if reserve {
			name = "reserved continuity"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var peer *observedSnapshotPeer
			fixture := newPeerSecurityFixture(t, peerSecurityOptions{
				LogOptions: func(int) mutationlog.Options { return mutationlog.Options{Capacity: 4} },
				Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
					if index != 0 {
						return
					}
					source, err := runtime.NewLanternReplicationService(f.primary[index])
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(source.CloseSnapshotTails)
					peer = &observedSnapshotPeer{
						LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(source),
						source:                           source, headers: make(chan observedSnapshotHeader, 1),
					}
					_, handler := graphv1connect.NewLanternReplicationServiceHandler(peer)
					mux.Handle("/graph.v1.LanternReplicationService/Snapshot", handler)
					mux.Handle("/graph.v1.LanternReplicationService/Subscribe", handler)
				},
			})
			put := func(prefix string) {
				t.Helper()
				for index := range 8 {
					if _, err := fixture.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
						Key: fmt.Sprintf("data:%s-%d", prefix, index), Value: &pb.Vertex_String_{String_: "value"},
					}}); err != nil {
						t.Fatal(err)
					}
				}
			}
			put("seed")
			client := graphv1connect.NewLanternReplicationServiceClient(fixture.runtimes[1].HTTPClient(), fixture.servers[0].URL)
			request := connect.NewRequest(&pb.SnapshotRequest{NamespaceFormat: "namespaced-v1", RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1})
			if reserve {
				request.Header().Set(service.SnapshotTailHeader, "reserve")
			}
			stream, err := client.Snapshot(ctx, request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = stream.Close() }()
			if !stream.Receive() || stream.Msg().GetHeader() == nil {
				t.Fatalf("native Snapshot header = (%v,%v)", stream.Msg(), stream.Err())
			}
			header := proto.Clone(stream.Msg().GetHeader()).(*pb.SnapshotHeader)
			sourceID := hlc.NodeID{3}
			origin := hex.EncodeToString(sourceID[:])
			if header.GetCutoffLocalSeq() != 8 || header.GetCutoffSeqPerOrigin()[origin] != 8 {
				t.Fatalf("unexpected native Snapshot cut: %+v", header)
			}
			token := stream.ResponseHeader().Get(service.SnapshotTailHeader)
			if (token != "") != reserve {
				t.Fatalf("reservation metadata present=%t, requested=%t", token != "", reserve)
			}
			var hold observedSnapshotHeader
			select {
			case hold = <-peer.headers:
			case <-ctx.Done():
				t.Fatal("native source header barrier not reached", ctx.Err())
			}
			put("tail")
			first, _ := fixture.logs[0].FirstSeq()
			if first != 13 {
				t.Fatalf("native retained first=%d, want 13", first)
			}
			hold.release <- nil
			counts := &pb.SnapshotFooter{}
			seen := make(map[string]bool)
			var footer *pb.SnapshotFooter
			for stream.Receive() {
				if footer != nil {
					t.Fatal("native Snapshot has a frame after its footer")
				}
				switch frame := stream.Msg().GetEntry().(type) {
				case *pb.SnapshotResponse_Vertex:
					vertex := frame.Vertex.GetVertex()
					if !strings.HasPrefix(vertex.GetKey(), "data:seed-") || seen[vertex.GetKey()] || vertex.GetString_() != "value" {
						t.Fatalf("native Snapshot vertex=%+v, want unique captured seed", vertex)
					}
					seen[vertex.GetKey()] = true
					counts.VertexCount++
				case *pb.SnapshotResponse_VertexCausalBarrier:
					counts.VertexCausalBarrierCount++
				case *pb.SnapshotResponse_Footer:
					footer = proto.Clone(frame.Footer).(*pb.SnapshotFooter)
				default:
					t.Fatalf("unexpected native Snapshot frame %T", frame)
				}
			}
			if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
				t.Fatal("native Snapshot did not complete", err)
			}
			if counts.GetVertexCount() != 8 || footer == nil || !proto.Equal(footer, counts) {
				t.Fatalf("native Snapshot count mismatch: observed=%+v footer=%+v", counts, footer)
			}
			_ = stream.Close()
			subscribe := connect.NewRequest(&pb.SubscribeRequest{NamespaceFormat: "namespaced-v1", FromLocalSeq: 9, FromSeqPerOrigin: map[string]uint64{origin: 9}, AcceptGraphMutations: true})
			if reserve {
				subscribe.Header().Set(service.SnapshotTailHeader, token)
			}
			feed, err := client.Subscribe(ctx, subscribe)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = feed.Close() }()
			if !reserve {
				if feed.Receive() || connect.CodeOf(feed.Err()) != connect.CodeFailedPrecondition || !strings.Contains(feed.Err().Error(), "gapped:") {
					t.Fatalf("unreserved native Subscribe=(%v,%v), want retained gap", feed.Msg(), feed.Err())
				}
				t.Logf("complete Snapshot cut=8 source=16 retained_first=%d; unreserved Subscribe9 rejected", first)
				return
			}
			for want := uint64(9); want <= 16; want++ {
				if !feed.Receive() {
					t.Fatalf("reserved native Subscribe missing seq%d: %v", want, feed.Err())
				}
				mutation := feed.Msg().GetMutation()
				if mutation.GetSeq() != want || !bytes.Equal(mutation.GetOrigin(), sourceID[:]) {
					t.Fatalf("reserved native mutation=%+v, want origin3 seq%d", mutation, want)
				}
			}
			t.Logf("complete Snapshot cut=8 source=16 retained_first=%d; reserved Subscribe delivered9..16", first)
		})
	}
}

// This is the native counterpart to the retained/evicted history control above.
// Real operator-signed membership and workload TLS admit both RPCs. Only the
// Snapshot body is held: writes continue normally and evict the captured tail.
func TestPeerPump_SnapshotTailHandoffDuringInstall(t *testing.T) {
	for _, interrupted := range []bool{false, true} {
		name := "evicted tail"
		if interrupted {
			name = "interrupted Snapshot then evicted tail"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 8*time.Second)
			defer cancel()
			sourceID := hlc.NodeID{3}
			var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
			var peer *observedSnapshotPeer
			fixture := newPeerSecurityFixture(t, peerSecurityOptions{
				LogOptions: func(int) mutationlog.Options { return mutationlog.Options{Capacity: 4} },
				Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
					graphs[index] = runtime.GraphCache()
					if index != 0 {
						return
					}
					source, err := runtime.NewLanternReplicationService(f.primary[index])
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(source.CloseSnapshotTails)
					peer = &observedSnapshotPeer{
						LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(source),
						source:                           source, headers: make(chan observedSnapshotHeader, 1), results: make(chan error, 1),
					}
					_, handler := graphv1connect.NewLanternReplicationServiceHandler(peer)
					mux.Handle("/graph.v1.LanternReplicationService/Snapshot", handler)
					mux.Handle("/graph.v1.LanternReplicationService/Subscribe", handler)
				},
			})
			put := func(key string) {
				t.Helper()
				if _, err := fixture.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
					Key: key, Value: &pb.Vertex_String_{String_: "value"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
				}}); err != nil {
					t.Fatal(err)
				}
			}
			for index := range 8 {
				put(fmt.Sprintf("data:seed-%d", index))
			}
			transport, err := provider.NewWorkloadPeerTransport(fixture.runtimes[1])
			if err != nil {
				t.Fatal(err)
			}
			apply := &observedSnapshotApplier{LanternService: fixture.primary[1], ctx: ctx, began: make(chan *observedSnapshotInstall, 1)}
			pump := replication.NewPump(replication.Config{
				NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{fixture.servers[0].URL},
				PeerTransport: transport, BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
			}, apply, graphs[1])
			done := make(chan error, 1)
			go func() { done <- pump.Run(ctx) }()
			joined := false
			defer func() {
				cancel()
				if joined {
					return
				}
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(time.Second):
					t.Error("native Pump did not join")
				}
			}()
			cycles := 1
			if interrupted {
				cycles++
			}
			for cycle := range cycles {
				var header observedSnapshotHeader
				var stage *observedSnapshotInstall
				select {
				case header = <-peer.headers:
				case <-ctx.Done():
					t.Fatal("native Snapshot header not observed", ctx.Err())
				}
				if fixture.primary[1].SnapshotInstallActive() || fixture.primary[1].SnapshotRepairRequired() {
					t.Fatal("native Header changed healthy receiver publication")
				}
				if _, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:seed-0"}); connect.CodeOf(err) != connect.CodeNotFound {
					t.Fatalf("read during native Snapshot collection = %v, want healthy NotFound", err)
				}
				fail := interrupted && cycle == 0
				if fail {
					header.release <- connect.NewError(connect.CodeUnavailable, errors.New("controlled native Snapshot interruption"))
				} else {
					if got := header.header.GetCutoffSeqPerOrigin()[hex.EncodeToString(sourceID[:])]; got != 8 {
						t.Fatalf("native Snapshot cutoff = %d, want 8", got)
					}
					for index := range 8 {
						put(fmt.Sprintf("data:tail-%d", index))
					}
					if first, _ := fixture.logs[0].FirstSeq(); first <= header.header.GetCutoffLocalSeq()+1 {
						t.Fatalf("tail was not actually evicted: first=%d cut=%d", first, header.header.GetCutoffLocalSeq())
					}
					header.release <- nil
				}
				select {
				case err := <-peer.results:
					if fail && connect.CodeOf(err) != connect.CodeUnavailable || !fail && err != nil {
						t.Fatalf("native source interrupted=%t: %v", fail, err)
					}
				case <-ctx.Done():
					t.Fatal("native source completion not observed", ctx.Err())
				}
				if fail {
					select {
					case stage := <-apply.began:
						close(stage.resume)
						t.Fatal("interrupted native Snapshot entered live replay")
					default:
					}
					if fixture.primary[1].SnapshotInstallActive() || fixture.primary[1].SnapshotRepairRequired() || fixture.primary[1].LocalSeq(sourceID) != 0 {
						t.Fatal("interrupted native collection changed healthy receiver")
					}
					continue
				}
				select {
				case stage = <-apply.began:
				case <-ctx.Done():
					t.Fatal("verified native Snapshot admission not observed", ctx.Err())
				}
				select {
				case verified := <-stage.finished:
					if !verified {
						t.Fatal("complete native Snapshot did not verify")
					}
					t.Logf("native snapshot verified=%t cut=%d source=%d receiver=%d", verified, header.header.GetCutoffLocalSeq(), fixture.primary[0].LocalSeq(sourceID), fixture.primary[1].LocalSeq(sourceID))
				case <-ctx.Done():
					t.Fatal("native Snapshot completion not observed", ctx.Err())
				}
				close(stage.resume)
			}
			if !waitForVertex(t, graphs[1], "data:tail-7", time.Second) {
				t.Fatalf("reserved native tail did not converge: snapshots=%d receiver=%d, want 16", peer.snapshots.Load(), fixture.primary[1].LocalSeq(sourceID))
			}
			// Direct cache visibility can precede relay-log publication and the
			// origin commit. A public read fences that publication before we
			// inspect the committed cursor.
			if _, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:seed-0"}); err != nil {
				t.Fatal("verified native install did not restore public read", err)
			}
			if got := fixture.primary[1].LocalSeq(sourceID); got != 16 {
				t.Fatalf("native receiver origin cursor=%d, want 16", got)
			}
			cancel()
			select {
			case err := <-done:
				joined = true
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("converged native Pump did not join")
			}
			if got := peer.snapshots.Load(); got != int32(cycles) {
				t.Fatalf("native Snapshot requests=%d, want %d", got, cycles)
			}
			if got := peer.gaps.Load(); got != 0 {
				t.Fatalf("post-Snapshot retained-origin gaps=%d, want none", got)
			}
		})
	}
}

func TestPeerPump_FailedSnapshotRecoversWithoutNewMutation(t *testing.T) {
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	source := newPumpNode(t, hlc.NodeID{0xB1})
	receiver := newPumpNode(t, hlc.NodeID{0xB2})
	finish, err := receiver.svc.BeginSnapshotInstall()
	if err != nil {
		t.Fatal(err)
	}
	// Simulate a failed live replay after complete transport validation. No
	// new mutation exists for a reconnecting Subscribe to deliver.
	finish(false)
	if _, err := receiver.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "absent"})); connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("failed install public read = %v, want fail-closed", err)
	}
	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	receiver.startPumpWithMetrics(ctx, t, []string{source.url}, metrics)
	ticker := time.NewTicker(5 * time.Millisecond)
	defer ticker.Stop()
	for metrics.snapshots.Load() == 0 {
		select {
		case <-ctx.Done():
			t.Fatal("failed Snapshot did not repair without a new mutation")
		case <-ticker.C:
		}
	}
	if _, err := receiver.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "absent"})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("verified empty Snapshot public read = %v, want NotFound", err)
	}
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("verified Snapshot repairs = %d, want one", got)
	}
}

func TestPeerPump_GraphOnlyBackupAggregateRelaysAcrossAuthenticatedSnapshots(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-node graph-only restore and relay")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	baseExpiration := time.Now().Add(50 * time.Minute)
	addExpiration := baseExpiration.Add(-time.Minute)

	source := newPumpNode(t, hlc.NodeID{0xD1})
	for i := range 2 {
		result, err := source.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
			Edge: &pb.Edge{
				Tail: "tail", Head: "head", Weight: math.MaxFloat32,
				Expiration: timestamppb.New(baseExpiration),
			},
		}))
		if err != nil || result == nil || (i == 1 && !math.IsInf(float64(result.Msg.GetEffectiveWeight()), 1)) {
			t.Fatalf("finite source Add[%d] = (%+v, %v), want derived +Inf on second Add", i, result, err)
		}
	}
	backupVertices, backupEdges := drainBackup(t, ctx, source.raw, &pb.BackupSnapshotRequest{})
	folded := backupEdges["tail->head"]
	if folded == nil || !math.IsInf(float64(folded.GetWeight()), 1) ||
		!folded.GetExpiration().AsTime().Equal(baseExpiration) {
		t.Fatalf("finite sources did not fold into the graph-only backup: %+v", folded)
	}

	// Public data requests retain their Bearer fixture. Private replication
	// uses separately signed workload membership and mutual TLS, because a
	// public-token TLS client cannot reserve a native Snapshot tail.
	newNode := func(id hlc.NodeID) *pumpNode {
		n := newAuthedPumpNode(t, id, 2)
		n.svc.WithDataNamespace()
		n.server.rep.WithOriginStates(n.svc)
		t.Cleanup(n.server.rep.CloseSnapshotTails)
		return n
	}
	nativePeers := make(map[*pumpNode]replicationTestPeer)
	nativeClients := make(map[*pumpNode]graphv1connect.LanternReplicationServiceClient)
	nativePeer := func(n *pumpNode) replicationTestPeer {
		if peer, present := nativePeers[n]; present {
			return peer
		}
		fixture := newPeerSecurityFixture(t, peerSecurityOptions{
			Mount: func(_ *peerSecurityFixture, index int, _ *service.ServingRuntime, mux *http.ServeMux) {
				if index != 0 {
					return
				}
				_, handler := graphv1connect.NewLanternReplicationServiceHandler(service.NewLanternReplicationServiceConnectHandler(n.server.rep))
				for _, procedure := range []string{"PeerStatus", "Snapshot", "Subscribe"} {
					mux.Handle("/graph.v1.LanternReplicationService/"+procedure, handler)
				}
			},
		})
		transport, err := provider.NewWorkloadPeerTransport(fixture.runtimes[1])
		if err != nil {
			t.Fatal(err)
		}
		peer := replicationTestPeer{url: fixture.servers[0].URL, transport: transport}
		nativePeers[n] = peer
		nativeClients[n] = graphv1connect.NewLanternReplicationServiceClient(fixture.runtimes[1].HTTPClient(), peer.url)
		return peer
	}
	startNativePump := func(n *pumpNode, peer replicationTestPeer, metrics replication.Metrics) {
		pump := replication.NewPump(replication.Config{
			NodeID: n.nodeID, NamespaceFormat: n.svc.DataNamespaceFormat(),
			Peers: []string{peer.url}, PeerTransport: peer.transport, Metrics: metrics,
			SearchConfigFingerprint: n.svc.SearchConfigFingerprint(),
			BackoffMin:              20 * time.Millisecond, BackoffMax: 200 * time.Millisecond,
		}, n.svc, n.cache)
		pumpCtx, stop := context.WithCancel(ctx)
		done := make(chan error, 1)
		go func() { done <- pump.Run(pumpCtx) }()
		t.Cleanup(func() {
			stop()
			select {
			case err := <-done:
				if err != nil {
					t.Error(err)
				}
			case <-time.After(2 * time.Second):
				t.Error("native aggregate relay Pump did not join")
			}
		})
	}
	a := newNode(hlc.NodeID{0xD2})
	var restoredVertices []*pb.Vertex
	for _, key := range []string{"tail", "head"} {
		vertex := backupVertices[key]
		if vertex == nil {
			t.Fatalf("backup omitted endpoint %q", key)
		}
		physical := proto.Clone(vertex).(*pb.Vertex)
		physical.Key = "data:" + key
		restoredVertices = append(restoredVertices, physical)
	}
	if _, err := a.svc.RestoreVertices(ctx, &pb.PutVerticesRequest{Vertices: restoredVertices}); err != nil {
		t.Fatalf("startup RestoreVertices: %v", err)
	}
	physicalFolded := proto.Clone(folded).(*pb.Edge)
	physicalFolded.Tail, physicalFolded.Head = "data:tail", "data:head"
	if _, err := a.svc.RestoreEdges(ctx, &pb.PutEdgesRequest{Edges: []*pb.Edge{physicalFolded}}); err != nil {
		t.Fatalf("startup RestoreEdges: %v", err)
	}
	restored := a.cache.SnapshotEdges()
	if len(restored) != 1 || len(restored[0].Contributions) != 1 ||
		!restored[0].Contributions[0].DerivedAggregate {
		t.Fatalf("startup restore lost derived provenance: %+v", restored)
	}
	put := connect.NewRequest(&pb.PutEdgeRequest{Edge: folded})
	put.Header().Set("Authorization", "Bearer "+testToken)
	if _, err := a.raw.PutEdge(ctx, put); connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("public PutEdge of folded aggregate = %v, want InvalidArgument", err)
	}

	unauthenticated := newReplicationRawClient(t, a.url)
	stream, err := unauthenticated.Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{
		RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, NamespaceFormat: a.svc.DataNamespaceFormat(),
	}))
	if err == nil {
		if stream.Receive() {
			t.Fatal("tokenless Snapshot exposed the restored graph")
		}
		err = stream.Err()
		_ = stream.Close()
	}
	if connect.CodeOf(err) != connect.CodeUnauthenticated {
		t.Fatalf("tokenless Snapshot = %v, want Unauthenticated", err)
	}

	add := connect.NewRequest(&pb.AddEdgeRequest{Edge: &pb.Edge{
		Tail: "tail", Head: "head", Weight: 7, Expiration: timestamppb.New(addExpiration),
	}})
	add.Header().Set("Authorization", "Bearer "+testToken)
	result, err := a.raw.AddEdge(ctx, add)
	if err != nil || !math.IsInf(float64(result.Msg.GetEffectiveWeight()), 1) {
		t.Fatalf("finite Add to restored aggregate = (%+v, %v), want effective +Inf", result, err)
	}
	for i := range 4 {
		if _, err := a.sdk.PutVertex(ctx, fmt.Sprintf("a-gap-%d", i), "value", time.Minute); err != nil {
			t.Fatalf("source gap write %d: %v", i, err)
		}
	}

	snapshotEdge := func(n *pumpNode) *pb.SnapshotEdge {
		t.Helper()
		_ = nativePeer(n)
		req := connect.NewRequest(&pb.SnapshotRequest{
			RequiredFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1, NamespaceFormat: n.svc.DataNamespaceFormat(),
		})
		stream, err := nativeClients[n].Snapshot(ctx, req)
		if err != nil {
			t.Fatalf("authenticated Snapshot from %x: %v", n.nodeID[:1], err)
		}
		defer func() { _ = stream.Close() }()
		var edge *pb.SnapshotEdge
		var footer *pb.SnapshotFooter
		for stream.Receive() {
			if item := stream.Msg().GetEdge(); item != nil && item.GetTail() == "data:tail" && item.GetHead() == "data:head" {
				edge = proto.Clone(item).(*pb.SnapshotEdge)
			}
			if item := stream.Msg().GetFooter(); item != nil {
				footer = item
			}
		}
		if err := stream.Err(); err != nil && !errors.Is(err, io.EOF) {
			t.Fatalf("Snapshot from %x: %v", n.nodeID[:1], err)
		}
		if edge == nil || footer.GetEdgeCount() != 1 {
			t.Fatalf("Snapshot from %x omitted the restored edge or footer: edge=%+v footer=%+v",
				n.nodeID[:1], edge, footer)
		}
		return edge
	}
	assertAggregate := func(n *pumpNode, adds int) {
		t.Helper()
		edge := snapshotEdge(n)
		base := edge.GetDerivedAggregate()
		if base == nil || len(edge.GetContributions()) != 0 ||
			!math.IsInf(float64(base.GetWeight()), 1) ||
			!base.GetExpiration().AsTime().Equal(baseExpiration) || len(base.GetAdds()) != adds {
			t.Fatalf("Snapshot from %x lost exclusive aggregate variant: %+v", n.nodeID[:1], edge)
		}
		first := base.GetAdds()[0]
		if first.GetWeight() != 7 || !first.GetExpiration().AsTime().Equal(addExpiration) ||
			len(first.GetContribId()) != 24 || first.GetHlc().GetWallNs() <= 0 {
			t.Fatalf("Snapshot from %x lost independently timed Add: %+v", n.nodeID[:1], first)
		}
	}
	assertLive := func(n *pumpNode) {
		t.Helper()
		req := connect.NewRequest(&pb.GetEdgeRequest{Tail: "tail", Head: "head"})
		req.Header().Set("Authorization", "Bearer "+testToken)
		got, err := n.raw.GetEdge(ctx, req)
		if err != nil || !math.IsInf(float64(got.Msg.GetEdge().GetWeight()), 1) ||
			!got.Msg.GetEdge().GetExpiration().AsTime().Equal(baseExpiration) {
			t.Fatalf("public GetEdge on %x = (%+v, %v), want +Inf with backup TTL", n.nodeID[:1], got, err)
		}
	}
	awaitSnapshot := func(n *pumpNode, metrics *observedSearchConfigMetrics, origin hlc.NodeID, want uint64) {
		t.Helper()
		deadline := time.Now().Add(8 * time.Second)
		for time.Now().Before(deadline) {
			if metrics.snapshots.Load() > 0 && n.svc.LocalSeq(origin) >= want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("node %x did not Snapshot origin %x through seq %d (snapshots=%d, seq=%d)",
			n.nodeID[:1], origin[:1], want, metrics.snapshots.Load(), n.svc.LocalSeq(origin))
	}

	assertAggregate(a, 1)
	assertLive(a)
	b := newNode(hlc.NodeID{0xD3})
	bMetrics := &observedSearchConfigMetrics{gate: readiness.NewGate(100, true, nil)}
	peerA := nativePeer(a)
	startNativePump(b, peerA, bMetrics)
	awaitSnapshot(b, bMetrics, a.nodeID, 5)
	assertAggregate(b, 1)
	assertLive(b)

	for i := range 4 {
		if _, err := b.sdk.PutVertex(ctx, fmt.Sprintf("b-gap-%d", i), "value", time.Minute); err != nil {
			t.Fatalf("relay gap write %d: %v", i, err)
		}
	}
	c := newNode(hlc.NodeID{0xD4})
	cMetrics := &observedSearchConfigMetrics{gate: readiness.NewGate(100, true, nil)}
	peerB := nativePeer(b)
	startNativePump(c, peerB, cMetrics)
	awaitSnapshot(c, cMetrics, b.nodeID, 4)
	if got := c.svc.LocalSeq(a.nodeID); got != 5 {
		t.Fatalf("relay Snapshot lost original origin cutoff: %d, want 5", got)
	}
	assertAggregate(c, 1)
	assertLive(c)

	later := connect.NewRequest(&pb.AddEdgeRequest{Edge: &pb.Edge{
		Tail: "tail", Head: "head", Weight: 9,
		Expiration: timestamppb.New(addExpiration.Add(-time.Minute)),
	}})
	later.Header().Set("Authorization", "Bearer "+testToken)
	if result, err := a.raw.AddEdge(ctx, later); err != nil || !math.IsInf(float64(result.Msg.GetEffectiveWeight()), 1) {
		t.Fatalf("post-Snapshot Add = (%+v, %v), want effective +Inf", result, err)
	}
	deadline := time.Now().Add(8 * time.Second)
	for c.svc.LocalSeq(a.nodeID) < 6 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if got := c.svc.LocalSeq(a.nodeID); got != 6 {
		t.Fatalf("live tail did not pass through B to C: origin seq %d, want 6", got)
	}
	assertAggregate(c, 2)
	assertLive(c)

	beforeLocalSeq, beforeLog := c.svc.LocalSeq(c.nodeID), c.log.Len()
	invalid := connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "tail", Head: "head", Weight: float32(math.NaN()),
	}})
	invalid.Header().Set("Authorization", "Bearer "+testToken)
	if _, err := c.raw.PutEdge(ctx, invalid); connect.CodeOf(err) != connect.CodeInvalidArgument ||
		c.svc.LocalSeq(c.nodeID) != beforeLocalSeq || c.log.Len() != beforeLog {
		t.Fatalf("non-finite source reached relay graph/log: %v, seq=%d log=%d", err, c.svc.LocalSeq(c.nodeID), c.log.Len())
	}
	assertAggregate(c, 2)

	finite := connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "tail", Head: "head", Weight: 11, Expiration: timestamppb.New(baseExpiration),
	}})
	finite.Header().Set("Authorization", "Bearer "+testToken)
	if _, err := c.raw.PutEdge(ctx, finite); err != nil {
		t.Fatalf("finite Put after relayed aggregate: %v", err)
	}
	replaced := snapshotEdge(c)
	if replaced.GetDerivedAggregate() != nil || len(replaced.GetContributions()) != 1 ||
		replaced.GetContributions()[0].GetWeight() != 11 {
		t.Fatalf("finite Put did not clear derived provenance: %+v", replaced)
	}
}

// TestPeerPump_SnapshotCarriesDeleteTombstones covers a three-peer partition:
// A deletes identities while B holds older writes, then C bootstraps from A
// after A's log has gapped. The pre-cutoff Deletes cannot arrive in C's tail;
// when B reconnects later its older Put/Add must still be fenced for D4.
func TestPeerPump_SnapshotCarriesDeleteTombstones(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-peer snapshot recovery test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	primary := newPumpNodeWithSearch(t, hlc.NodeID{0xD1}, 4, true)
	stale := newPumpNode(t, hlc.NodeID{0xD2})
	const victim, tail, head = "deleted-v", "deleted-tail", "deleted-head"
	if _, err := stale.sdk.PutVertex(ctx, victim, "old", time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.sdk.PutEdge(ctx, tail, head, 1, time.Hour); err != nil {
		t.Fatal(err)
	}
	if _, err := stale.sdk.AddEdge(ctx, tail, head, 2, time.Hour); err != nil {
		t.Fatal(err)
	}
	staleSeq, _ := stale.log.LastSeq()
	primary.clock.Update(stale.clock.Now())
	if _, err := primary.sdk.DeleteVertex(ctx, victim); err != nil {
		t.Fatal(err)
	}
	if _, err := primary.sdk.DeleteEdge(ctx, tail, head); err != nil {
		t.Fatal(err)
	}
	// Evict both Deletes so C can only learn their floors from Snapshot.
	for i := 0; i < 16; i++ {
		if _, err := primary.sdk.PutVertex(ctx, fmt.Sprintf("delete-gap-%d", i), "live", time.Minute); err != nil {
			t.Fatal(err)
		}
	}
	// Verify the actual Connect Snapshot advertises both exact source
	// deadlines and counts. These Deletes precede its cutoff, so the tail
	// cannot repair an omitted frame.
	deadlines := primary.cache.CausalMetadataStats()
	stream, err := newReplicationRawClient(t, primary.url).Snapshot(ctx, connect.NewRequest(&pb.SnapshotRequest{}))
	if err != nil {
		t.Fatal(err)
	}
	var vertexFrames, edgeFrames uint64
	var footer *pb.SnapshotFooter
	for stream.Receive() {
		switch e := stream.Msg().GetEntry().(type) {
		case *pb.SnapshotResponse_VertexTombstone:
			vertexFrames++
			if e.VertexTombstone.GetKey() != victim ||
				!e.VertexTombstone.GetExpiration().AsTime().Equal(deadlines.OldestVertexRetentionDeadline) {
				t.Fatalf("vertex tombstone wire frame = %+v, deadline=%v", e.VertexTombstone, deadlines.OldestVertexRetentionDeadline)
			}
		case *pb.SnapshotResponse_EdgeTombstone:
			edgeFrames++
			if e.EdgeTombstone.GetTail() != tail || e.EdgeTombstone.GetHead() != head ||
				!e.EdgeTombstone.GetExpiration().AsTime().Equal(deadlines.OldestEdgeRetentionDeadline) {
				t.Fatalf("edge tombstone wire frame = %+v, deadline=%v", e.EdgeTombstone, deadlines.OldestEdgeRetentionDeadline)
			}
		case *pb.SnapshotResponse_Footer:
			footer = e.Footer
		}
	}
	if err := stream.Err(); err != nil {
		t.Fatal(err)
	}
	_ = stream.Close()
	if vertexFrames != 1 || edgeFrames != 1 || footer == nil ||
		footer.GetVertexTombstoneCount() != vertexFrames || footer.GetEdgeTombstoneCount() != edgeFrames {
		t.Fatalf("tombstone frames=%d/%d footer=%+v", vertexFrames, edgeFrames, footer)
	}
	boot := newPumpNode(t, hlc.NodeID{0xD3})
	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	boot.startPumpWithMetrics(ctx, t, []string{primary.url}, metrics)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && metrics.snapshots.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("gap repair snapshots=%d, want 1", got)
	}
	// A second real Connect pump now delivers B's stale origin stream.
	boot.startPump(ctx, t, []string{stale.url})
	deadline = time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && boot.svc.LocalSeq(stale.nodeID) < staleSeq {
		time.Sleep(10 * time.Millisecond)
	}
	if got := boot.svc.LocalSeq(stale.nodeID); got != staleSeq {
		t.Fatalf("stale peer applied seq=%d, want %d", got, staleSeq)
	}
	if _, ok := boot.cache.GetVertex(victim); ok {
		t.Fatal("pre-cutoff DeleteVertex lost: stale PutVertex resurrected on bootstrap peer")
	}
	if _, _, ok := boot.cache.GetEdgeDetail(tail, head); ok {
		t.Fatal("pre-cutoff DeleteEdge lost: stale PutEdge/AddEdge resurrected on bootstrap peer")
	}
}

type scriptedGraphSnapshotPeer struct {
	graphv1connect.UnimplementedLanternReplicationServiceHandler
	frames              []*pb.SnapshotResponse
	afterSend           func(context.Context, *pb.SnapshotResponse) error
	afterFirstSubscribe func(context.Context, *connect.Request[pb.SubscribeRequest], *connect.ServerStream[pb.SubscribeResponse]) error
	subscribeCalls      atomic.Int32
	snapshotCalls       atomic.Int32
}

func (*scriptedGraphSnapshotPeer) PeerStatus(context.Context, *connect.Request[pb.PeerStatusRequest]) (*connect.Response[pb.PeerStatusResponse], error) {
	return connect.NewResponse(&pb.PeerStatusResponse{
		RequiredSnapshotFormat: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
	}), nil
}

func (p *scriptedGraphSnapshotPeer) Subscribe(ctx context.Context, req *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
	if p.subscribeCalls.Add(1) == 1 {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("gapped"))
	}
	if p.afterFirstSubscribe != nil {
		return p.afterFirstSubscribe(ctx, req, stream)
	}
	<-ctx.Done()
	return ctx.Err()
}

func (p *scriptedGraphSnapshotPeer) Snapshot(ctx context.Context, _ *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	p.snapshotCalls.Add(1)
	for _, frame := range p.frames {
		if err := stream.Send(frame); err != nil {
			return err
		}
		if p.afterSend != nil {
			if err := p.afterSend(ctx, frame); err != nil {
				return err
			}
		}
	}
	return nil
}

type tombstoneSnapshotMetrics struct {
	failed    chan struct{}
	snapshots chan struct{}
}

func (*tombstoneSnapshotMetrics) OnPumpConnect(string)        {}
func (*tombstoneSnapshotMetrics) OnPumpApply(string)          {}
func (*tombstoneSnapshotMetrics) OnPumpDropSelfEcho(string)   {}
func (*tombstoneSnapshotMetrics) OnSearchConfig(string, bool) {}
func (m *tombstoneSnapshotMetrics) OnPumpDisconnect(_ string, reason string) {
	if reason == "snapshot_failed" {
		select {
		case m.failed <- struct{}{}:
		default:
		}
	}
}
func (m *tombstoneSnapshotMetrics) OnPumpSnapshotReplayed(string, uint64, uint64, time.Duration) {
	select {
	case m.snapshots <- struct{}{}:
	default:
	}
}

// TestPeerPump_SnapshotTombstoneFraming_E2E sends malformed and expired
// Snapshot frames through the actual Connect/h2c handler. Invalid framing
// fails before resume; an expired but otherwise valid marker is counted and
// cannot create a fresh D4 window on the receiver.
func TestPeerPump_SnapshotTombstoneFraming_E2E(t *testing.T) {
	stamp := &pb.HLCTimestamp{WallNs: 20, NodeId: append([]byte{0xF1}, make([]byte, 15)...)}
	cutoffOrigin := hlc.NodeID{0xF1}
	for _, tc := range []struct {
		name       string
		marker     *pb.SnapshotVertexTombstone
		footer     *pb.SnapshotFooter
		wantFailed bool
	}{
		{"valid expired", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp, Expiration: timestamppb.New(time.Now().Add(-time.Minute))}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, false},
		{"missing HLC", &pb.SnapshotVertexTombstone{Key: "expired-v", Expiration: timestamppb.New(time.Now().Add(time.Hour))}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, true},
		{"zero NodeID", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: &pb.HLCTimestamp{WallNs: 20, NodeId: make([]byte, 16)}, Expiration: timestamppb.New(time.Now().Add(time.Hour))}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, true},
		{"missing expiration", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp}, &pb.SnapshotFooter{VertexTombstoneCount: 1}, true},
		{"truncated marker count", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp, Expiration: timestamppb.New(time.Now().Add(time.Hour))}, &pb.SnapshotFooter{VertexTombstoneCount: 2}, true},
		{"missing footer", &pb.SnapshotVertexTombstone{Key: "expired-v", Hlc: stamp, Expiration: timestamppb.New(time.Now().Add(time.Hour))}, nil, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			frames := []*pb.SnapshotResponse{
				{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
					CutoffSeqPerOrigin: map[string]uint64{hex.EncodeToString(cutoffOrigin[:]): 7},
					CutoffHlc:          stamp,
				}}},
				{Entry: &pb.SnapshotResponse_VertexTombstone{VertexTombstone: tc.marker}},
			}
			if tc.footer != nil {
				frames = append(frames, &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Footer{Footer: tc.footer}})
			}
			peer := &scriptedGraphSnapshotPeer{frames: frames}
			mux := http.NewServeMux()
			mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
			srv := httptest.NewUnstartedServer(mux)
			protocols := new(http.Protocols)
			protocols.SetHTTP1(true)
			protocols.SetUnencryptedHTTP2(true)
			srv.Config.Protocols = protocols
			srv.Start()
			t.Cleanup(srv.Close)
			boot := newPumpNode(t, hlc.NodeID{0xF2})
			metrics := &tombstoneSnapshotMetrics{failed: make(chan struct{}, 1), snapshots: make(chan struct{}, 1)}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			t.Cleanup(cancel)
			pump := replication.NewPump(replication.Config{NodeID: boot.nodeID, Peers: []string{srv.URL}, HTTPClient: h2cClient(), BackoffMin: time.Second, BackoffMax: time.Second, Metrics: metrics}, boot.svc, boot.cache)
			go func() { _ = pump.Run(ctx) }()
			var event <-chan struct{} = metrics.snapshots
			if tc.wantFailed {
				event = metrics.failed
			}
			select {
			case <-event:
			case <-ctx.Done():
				t.Fatalf("Snapshot result did not arrive: %v", ctx.Err())
			}
			wantCutoff := uint64(7)
			if tc.wantFailed {
				wantCutoff = 0
			}
			if got := boot.svc.LocalSeq(cutoffOrigin); got != wantCutoff {
				t.Fatalf("Snapshot watermark after replay = %d, want %d", got, wantCutoff)
			}
			if !tc.wantFailed {
				older := hlc.Timestamp{WallNs: 10, NodeID: hlc.NodeID{0xF0}}
				if !boot.cache.PutVertexWithExpirationHLC("expired-v", &pb.Vertex{Key: "expired-v"}, time.Now().Add(time.Hour), older) {
					t.Fatal("expired tombstone frame extended the Delete floor")
				}
			}
		})
	}
}

func TestPeerPump_LateNonFiniteSnapshotEdgeRejectedBeforePublication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	boot := newPumpNode(t, hlc.NodeID{0xF3})
	origin := hlc.NodeID{0xF4}
	remote := newPumpNode(t, hlc.NodeID{0xF5})
	if result, err := remote.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{
			Tail: "peer-tail", Head: "peer-head", Weight: 4,
			Expiration: timestamppb.New(time.Now().Add(time.Hour)),
		},
	})); err != nil || result.Msg.GetEffectiveWeight() != 4 {
		t.Fatalf("source peer AddEdge = (%+v, %v), want effective 4", result, err)
	}
	remoteMutation := readFullMutationFrame(t, ctx, remote.url, 1).GetMutation()
	if remoteMutation == nil || remoteMutation.GetSeq() != 1 {
		t.Fatalf("source peer Subscribe mutation = %v, want seq 1", remoteMutation)
	}
	edgeFrame := func(tail, head string, weight float32) *pb.SnapshotResponse {
		return &pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{
			Tail: tail, Head: head,
			Contributions: []*pb.SnapshotEdgeContribution{{Weight: weight}},
		}}}
	}
	frames := []*pb.SnapshotResponse{
		{Entry: &pb.SnapshotResponse_Header{Header: &pb.SnapshotHeader{
			Format: pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1,
			CutoffSeqPerOrigin: map[string]uint64{
				hex.EncodeToString(origin[:]): 7,
			},
			CutoffHlc: &pb.HLCTimestamp{WallNs: time.Now().UnixNano(), NodeId: origin[:]},
		}}},
	}
	for _, key := range []string{"relay-warmup", "fault-tail", "fault-head", "bad-tail", "bad-head"} {
		vertex := &pb.Vertex{Key: key}
		if key == "relay-warmup" {
			vertex.Value = &pb.Vertex_String_{String_: "ready"}
		}
		frames = append(frames, &pb.SnapshotResponse{
			Entry: &pb.SnapshotResponse_Vertex{Vertex: &pb.SnapshotVertex{Vertex: vertex}},
		})
	}
	frames = append(frames,
		edgeFrame("fault-tail", "fault-head", 1),
		edgeFrame("bad-tail", "bad-head", float32(math.NaN())),
		&pb.SnapshotResponse{Entry: &pb.SnapshotResponse_Footer{Footer: &pb.SnapshotFooter{
			VertexCount: 5, EdgeCount: 2,
		}}},
	)
	runAttempt := func(frames []*pb.SnapshotResponse, afterSend func(context.Context, *pb.SnapshotResponse) error) (*tombstoneSnapshotMetrics, func()) {
		t.Helper()
		peer := &scriptedGraphSnapshotPeer{frames: frames, afterSend: afterSend}
		mux := http.NewServeMux()
		mux.Handle(graphv1connect.NewLanternReplicationServiceHandler(peer))
		srv := httptest.NewUnstartedServer(mux)
		protocols := new(http.Protocols)
		protocols.SetHTTP1(true)
		protocols.SetUnencryptedHTTP2(true)
		srv.Config.Protocols = protocols
		srv.Start()
		t.Cleanup(srv.Close)
		metrics := &tombstoneSnapshotMetrics{failed: make(chan struct{}, 1), snapshots: make(chan struct{}, 1)}
		pumpCtx, stopPump := context.WithCancel(ctx)
		pump := replication.NewPump(replication.Config{
			NodeID: boot.nodeID, Peers: []string{srv.URL}, HTTPClient: h2cClient(),
			BackoffMin: time.Second, BackoffMax: time.Second, Metrics: metrics,
		}, boot.svc, boot.cache)
		done := make(chan struct{})
		go func() {
			_ = pump.Run(pumpCtx)
			close(done)
		}()
		stop := func() {
			stopPump()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Error("snapshot pump did not stop")
			}
		}
		t.Cleanup(stop)
		return metrics, stop
	}

	firstEdgeSent := make(chan struct{})
	allowMalformed := make(chan struct{})
	defer func() {
		select {
		case <-allowMalformed:
		default:
			close(allowMalformed)
		}
	}()
	failed, stopFailed := runAttempt(frames, func(streamCtx context.Context, frame *pb.SnapshotResponse) error {
		if frame.GetEdge().GetTail() != "fault-tail" {
			return nil
		}
		close(firstEdgeSent)
		select {
		case <-allowMalformed:
			return nil
		case <-streamCtx.Done():
			return streamCtx.Err()
		}
	})
	select {
	case <-firstEdgeSent:
	case <-ctx.Done():
		t.Fatalf("first Snapshot edge was not sent: %v", ctx.Err())
	}
	if _, live := boot.cache.GetWeight("fault-tail", "fault-head"); live || boot.svc.SnapshotInstallActive() || boot.svc.SnapshotRepairRequired() {
		t.Fatal("unverified finite prefix changed live graph or healthy publication")
	}
	if _, err := boot.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
		Tail: "fault-tail", Head: "fault-head",
	})); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("in-flight Snapshot prefix GetEdge: %v, want healthy NotFound", err)
	}
	applyResult := make(chan error, 1)
	go func() { applyResult <- boot.svc.ApplyMutation(ctx, remoteMutation) }()
	select {
	case err := <-applyResult:
		if err != nil {
			t.Fatalf("concurrent peer apply during Snapshot collection: %v", err)
		}
	case <-ctx.Done():
		t.Fatalf("concurrent peer apply did not return while Snapshot was paused: %v", ctx.Err())
	}
	if weight, live := boot.cache.GetWeight("peer-tail", "peer-head"); !live || weight != 4 ||
		boot.svc.LocalSeq(remote.nodeID) != 1 || boot.log.Len() != 1 {
		t.Fatalf("collection did not preserve healthy concurrent peer mutation: seq/log=%d/%d",
			boot.svc.LocalSeq(remote.nodeID), boot.log.Len())
	}
	close(allowMalformed)
	select {
	case <-failed.failed:
	case <-ctx.Done():
		t.Fatalf("malformed second edge frame did not fail Snapshot: %v", ctx.Err())
	}
	if got, live := boot.cache.GetWeight("fault-tail", "fault-head"); live {
		t.Fatalf("earlier unverified finite frame leaked = (%v, %t)", got, live)
	}
	if _, live := boot.cache.GetWeight("bad-tail", "bad-head"); live {
		t.Fatal("malformed later edge frame changed the graph")
	}
	if seq := boot.svc.LocalSeq(origin); seq != 0 ||
		boot.svc.LocalSeq(remote.nodeID) != 1 || boot.log.Len() != 1 {
		t.Fatalf("rejected Snapshot changed snapshot/peer origin or log to %d/%d/%d",
			seq, boot.svc.LocalSeq(remote.nodeID), boot.log.Len())
	}
	stopFailed()
	if boot.svc.SnapshotRepairRequired() || boot.svc.SnapshotInstallActive() {
		t.Fatal("invalid late frame faulted healthy publication")
	}
	if err := boot.svc.ApplyMutation(ctx, remoteMutation); err != nil {
		t.Fatalf("duplicate peer after rejected Snapshot: %v", err)
	}
	if weight, live := boot.cache.GetWeight("peer-tail", "peer-head"); !live || weight != 4 ||
		boot.svc.LocalSeq(remote.nodeID) != 1 || boot.log.Len() != 1 {
		t.Fatalf("rejected Snapshot changed peer mutation: seq/log=%d/%d",
			boot.svc.LocalSeq(remote.nodeID), boot.log.Len())
	}

	forgedFrames := append([]*pb.SnapshotResponse(nil), frames...)
	forgedFrames[len(forgedFrames)-2] = &pb.SnapshotResponse{
		Entry: &pb.SnapshotResponse_Edge{Edge: &pb.SnapshotEdge{
			Tail: "bad-tail", Head: "bad-head",
			DerivedAggregate: &pb.SnapshotEdgeDerivedAggregate{Weight: 5},
		}},
	}
	forged, stopForged := runAttempt(forgedFrames, nil)
	select {
	case <-forged.failed:
	case <-ctx.Done():
		t.Fatalf("forged derived marker did not fail Snapshot retry: %v", ctx.Err())
	}
	stopForged()
	if boot.svc.SnapshotRepairRequired() || boot.svc.SnapshotInstallActive() {
		t.Fatal("forged marker faulted healthy publication")
	}
	if _, live := boot.cache.GetWeight("bad-tail", "bad-head"); live ||
		boot.svc.LocalSeq(origin) != 0 || boot.svc.LocalSeq(remote.nodeID) != 1 || boot.log.Len() != 1 {
		t.Fatalf("forged marker changed graph or watermark: seq=%d log=%d",
			boot.svc.LocalSeq(origin), boot.log.Len())
	}

	verifiedFrames := append([]*pb.SnapshotResponse(nil), frames...)
	verifiedFrames[len(verifiedFrames)-2] = edgeFrame("bad-tail", "bad-head", 2)
	succeeded, stopSucceeded := runAttempt(verifiedFrames, nil)
	select {
	case <-succeeded.snapshots:
	case <-ctx.Done():
		t.Fatalf("valid Snapshot retry did not finish: %v", ctx.Err())
	}
	stopSucceeded()
	if seq := boot.svc.LocalSeq(origin); seq != 7 {
		t.Fatalf("verified Snapshot watermark = %d, want 7", seq)
	}
	for _, edge := range []struct {
		tail, head string
		weight     float32
	}{
		{"fault-tail", "fault-head", 1},
		{"bad-tail", "bad-head", 2},
	} {
		got, err := boot.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
			Tail: edge.tail, Head: edge.head,
		}))
		if err != nil || got.Msg.GetEdge().GetWeight() != edge.weight {
			t.Fatalf("public GetEdge(%s,%s) after retry = (%+v, %v), want %v",
				edge.tail, edge.head, got, err, edge.weight)
		}
	}
	status, err := boot.raw.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil || status.Msg.GetVertexCount() != 7 || status.Msg.GetEdgeCount() != 3 {
		t.Fatalf("public status after verified retry = (%+v, %v), want five staged plus two pre-existing vertices and three edges", status, err)
	}
	put, err := boot.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{
		Tail: "post-retry", Head: "edge", Weight: 3,
	}}))
	if err != nil || put.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("public PutEdge after retry = (%+v, %v), want applied", put, err)
	}
	add, err := boot.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{Edge: &pb.Edge{
		Tail: "post-retry", Head: "edge", Weight: 2,
	}}))
	if err != nil || add.Msg.GetEffectiveWeight() != 5 {
		t.Fatalf("public AddEdge after retry = (%+v, %v), want effective 5", add, err)
	}
	read, err := boot.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "post-retry", Head: "edge"}))
	if err != nil || read.Msg.GetEdge().GetWeight() != 5 {
		t.Fatalf("public GetEdge after new writes = (%+v, %v), want weight 5", read, err)
	}
	deleted, err := boot.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "post-retry", Head: "edge"}))
	if err != nil || !deleted.Msg.GetExisted() || boot.svc.LocalSeq(boot.nodeID) != 3 || boot.log.Len() != 4 {
		t.Fatalf("public DeleteEdge after retry = (%+v, %v), local seq/log = %d/%d",
			deleted, err, boot.svc.LocalSeq(boot.nodeID), boot.log.Len())
	}
	if err := boot.svc.ApplyMutation(ctx, remoteMutation); err != nil {
		t.Fatalf("peer after verified Snapshot retry: %v", err)
	}
	if err := boot.svc.ApplyMutation(ctx, remoteMutation); err != nil {
		t.Fatalf("duplicate peer after verified Snapshot retry: %v", err)
	}
	read, err = boot.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
		Tail: "peer-tail", Head: "peer-head",
	}))
	if err != nil || read.Msg.GetEdge().GetWeight() != 4 ||
		boot.svc.LocalSeq(remote.nodeID) != 1 || boot.log.Len() != 4 {
		t.Fatalf("peer convergence after retry = (%+v,%v), origin/log=%d/%d, want one weight-4 effect",
			read, err, boot.svc.LocalSeq(remote.nodeID), boot.log.Len())
	}
}

// TestPeerPump_OutcomeAwarePutEdgesGapRecovery is the #1206 regression for
// the real broad_mutate failure: outcome-aware edge Puts filled a tiny log,
// a third node attached through a relay after the retained window had moved,
// and Snapshot had to restore endpoint vertices that edge writes had created
// implicitly. Those endpoints are represented internally by nil *pb.Vertex
// values, but the replication wire must carry canonical nil-valued Vertex
// messages rather than a nil SnapshotVertex payload.
func TestPeerPump_OutcomeAwarePutEdgesGapRecovery(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping three-node gap-recovery test in short mode")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	const logCapacity = 8
	a := newPumpNodeWithSearch(t, hlc.NodeID{0xC1}, logCapacity, true)
	b := newPumpNodeWithSearch(t, hlc.NodeID{0xC2}, logCapacity, true)
	c := newPumpNodeWithSearch(t, hlc.NodeID{0xC3}, logCapacity, true)

	// A and B replicate directly while C is partitioned. B therefore becomes
	// a relay whose tiny local log contains mutations from both origins.
	a.startPump(ctx, t, []string{b.url})
	b.startPump(ctx, t, []string{a.url})
	time.Sleep(150 * time.Millisecond)

	type expectedEdge struct {
		tail, head string
		weight     float32
	}
	var expected []expectedEdge
	writeSet := func(name string, node *pumpNode, baseWeight float32) {
		t.Helper()
		for i := 0; i < 4; i++ {
			tail := fmt.Sprintf("gap/%s/singular/%d", name, i)
			weight := baseWeight + float32(i)
			outcome, err := node.sdk.PutEdge(ctx, tail, "gap/head", weight, time.Hour)
			if err != nil {
				t.Fatalf("%s PutEdge[%d]: %v", name, i, err)
			}
			if outcome != client.PutOutcomeAppliedAndLive {
				t.Fatalf("%s PutEdge[%d] outcome = %s, want APPLIED_AND_LIVE", name, i, outcome)
			}
			expected = append(expected, expectedEdge{tail: tail, head: "gap/head", weight: weight})
		}
		for batch := 0; batch < 4; batch++ {
			inputs := make([]client.EdgeInput, 0, 2)
			for item := 0; item < 2; item++ {
				tail := fmt.Sprintf("gap/%s/plural/%d/%d", name, batch, item)
				weight := baseWeight + 10 + float32(2*batch+item)
				inputs = append(inputs, client.EdgeInput{
					Tail: tail, Head: "gap/head", Weight: weight,
					Expiration: time.Now().Add(time.Hour),
				})
				expected = append(expected, expectedEdge{tail: tail, head: "gap/head", weight: weight})
			}
			results, err := node.sdk.PutEdges(ctx, inputs)
			if err != nil {
				t.Fatalf("%s PutEdges[%d]: %v", name, batch, err)
			}
			for i, result := range results {
				if result.Outcome != client.PutOutcomeAppliedAndLive {
					t.Fatalf("%s PutEdges[%d][%d] outcome = %s, want APPLIED_AND_LIVE", name, batch, i, result.Outcome)
				}
			}
		}
	}
	writeSet("a", a, 100)
	writeSet("b", b, 200)

	// An accepted-expired Put is absent from the live graph but must survive
	// gap recovery as a causal floor. This stays Put-only: mixed Put/Add on one
	// identity belongs to the reset-aware contribution contract in #1203.
	expiredOutcome, err := a.sdk.PutEdgeAt(
		ctx, "gap/expired", "gap/head", 999, time.Now().Add(-time.Second),
	)
	if err != nil {
		t.Fatalf("a born-expired PutEdgeAt: %v", err)
	}
	if expiredOutcome != client.PutOutcomeExpired {
		t.Fatalf("a born-expired PutEdgeAt outcome = %s, want EXPIRED", expiredOutcome)
	}

	for _, edge := range expected {
		for _, node := range []*pumpNode{a, b} {
			if got, ok := waitForEdge(t, node.cache, edge.tail, edge.head, edge.weight, 5*time.Second); !ok || got != edge.weight {
				t.Fatalf("pre-gap %x edge %s->%s = (%v,%v), want (%v,true)", node.nodeID[0], edge.tail, edge.head, got, ok, edge.weight)
			}
		}
	}
	barrierDeadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(barrierDeadline) {
		_, edgeBarriers := b.cache.CausalBarrierCounts()
		if edgeBarriers == 1 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if _, edgeBarriers := b.cache.CausalBarrierCounts(); edgeBarriers != 1 {
		t.Fatalf("relay edge causal barriers = %d, want 1 before gap recovery", edgeBarriers)
	}

	// More cluster mutations than B's ring capacity make a local-seq=1
	// Subscribe deterministically gapped. This is the same real Connect/h2c
	// surface the pump uses, asserted explicitly before C starts recovery.
	relayReplication := newReplicationRawClient(t, b.url)
	gapStream, err := relayReplication.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err == nil {
		if gapStream.Receive() {
			t.Fatal("gapped direct Subscribe unexpectedly delivered a mutation")
		}
		err = gapStream.Err()
		_ = gapStream.Close()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition {
		t.Fatalf("direct Subscribe gap error = %v (code %v), want FailedPrecondition", err, connect.CodeOf(err))
	}

	metrics := &observedSearchConfigMetrics{
		gate: readiness.NewGate(100, true, nil), observations: make(chan bool, 1),
	}
	c.startPumpWithMetrics(ctx, t, []string{b.url}, metrics)
	for _, edge := range expected {
		if got, ok := waitForWireEdge(t, ctx, c.raw, edge.tail, edge.head, edge.weight, 8*time.Second); !ok || got != edge.weight {
			t.Fatalf("snapshot relay edge %s->%s = (%v,%v), want (%v,true)", edge.tail, edge.head, got, ok, edge.weight)
		}
	}
	snapshotDeadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(snapshotDeadline) && metrics.snapshots.Load() == 0 {
		time.Sleep(10 * time.Millisecond)
	}
	if got := metrics.snapshots.Load(); got != 1 {
		t.Fatalf("completed snapshot count = %d, want exactly 1", got)
	}
	if _, edgeBarriers := c.cache.CausalBarrierCounts(); edgeBarriers != 1 {
		t.Fatalf("snapshot edge causal barriers = %d, want 1", edgeBarriers)
	}
	if c.cache.PutEdgeWithExpirationHLC(
		"gap/expired", "gap/head", 1, time.Now().Add(time.Hour),
		hlc.Timestamp{WallNs: 1, NodeID: hlc.NodeID{0x01}},
	) {
		t.Fatal("snapshot lost born-expired edge causal floor")
	}

	// Prove the Snapshot cutoff stitched to the live Subscribe tail on the
	// same relay, for both singular and plural outcome-aware Put forms.
	postSingular := expectedEdge{tail: "gap/post/singular", head: "gap/head", weight: 301}
	if outcome, err := a.sdk.PutEdge(ctx, postSingular.tail, postSingular.head, postSingular.weight, time.Hour); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("post-snapshot PutEdge = (%s,%v), want (APPLIED_AND_LIVE,nil)", outcome, err)
	}
	postPlural := []expectedEdge{
		{tail: "gap/post/plural/0", head: "gap/head", weight: 302},
		{tail: "gap/post/plural/1", head: "gap/head", weight: 303},
	}
	results, err := b.sdk.PutEdges(ctx, []client.EdgeInput{
		{Tail: postPlural[0].tail, Head: postPlural[0].head, Weight: postPlural[0].weight, Expiration: time.Now().Add(time.Hour)},
		{Tail: postPlural[1].tail, Head: postPlural[1].head, Weight: postPlural[1].weight, Expiration: time.Now().Add(time.Hour)},
	})
	if err != nil || len(results) != 2 || results[0].Outcome != client.PutOutcomeAppliedAndLive || results[1].Outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("post-snapshot PutEdges = (%+v,%v), want two APPLIED_AND_LIVE outcomes", results, err)
	}
	expected = append(expected, postSingular)
	expected = append(expected, postPlural...)

	for _, edge := range expected {
		for _, node := range []*pumpNode{a, b, c} {
			if got, ok := waitForWireEdge(t, ctx, node.raw, edge.tail, edge.head, edge.weight, 5*time.Second); !ok || got != edge.weight {
				t.Errorf("final %x edge %s->%s = (%v,%v), want (%v,true)", node.nodeID[0], edge.tail, edge.head, got, ok, edge.weight)
			}
		}
	}
	waitForSearchConvergence(t, ctx, "gap", nil, a.raw, b.raw, c.raw)
}

// Use the actual default tail bound, not wall-clock throughput: each held
// Snapshot admits exactly 513 writes before finish. A core drop signal orders
// overflow before releasing the header; a third, quiet attempt is the control.
func TestPeerPump_SnapshotTailCapacityBeforeFinish(t *testing.T) {
	for _, ringCapacity := range []int{4, 10000} {
		t.Run(fmt.Sprintf("ring_%d", ringCapacity), func(t *testing.T) {

			ctx, cancel := context.WithTimeout(t.Context(), 12*time.Second)
			defer cancel()
			sourceID := hlc.NodeID{3}
			drops := make(chan struct{}, 4)
			var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
			var peer *observedSnapshotPeer
			fixture := newPeerSecurityFixture(t, peerSecurityOptions{
				LogOptions: func(index int) mutationlog.Options {
					opts := mutationlog.Options{Capacity: ringCapacity, SubscriberBuffer: 512}
					if index == 0 {
						opts.OnDrop = func(cause string) {
							if cause == mutationlog.DropCauseBufferFull {
								select {
								case drops <- struct{}{}:
								default:
								}
							}
						}
					}
					return opts
				},
				Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
					graphs[index] = runtime.GraphCache()
					if index != 0 {
						return
					}
					source, err := runtime.NewLanternReplicationService(f.primary[index])
					if err != nil {
						t.Fatal(err)
					}
					t.Cleanup(source.CloseSnapshotTails)
					peer = &observedSnapshotPeer{
						LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(source),
						source:                           source, headers: make(chan observedSnapshotHeader, 1), results: make(chan error, 4),
					}
					_, handler := graphv1connect.NewLanternReplicationServiceHandler(peer)
					mux.Handle("/graph.v1.LanternReplicationService/Snapshot", handler)
					mux.Handle("/graph.v1.LanternReplicationService/Subscribe", handler)
				},
			})
			put := func(key string) {
				t.Helper()
				if _, err := fixture.primary[0].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
					Key: key, Value: &pb.Vertex_String_{String_: "value"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
				}}); err != nil {
					t.Fatal(err)
				}
			}
			for i := range 8 {
				put(fmt.Sprintf("data:seed-%d", i))
			}
			finish, err := fixture.primary[1].BeginSnapshotInstall()
			if err != nil {
				t.Fatal(err)
			}
			finish(false) // Force the same repair path even when the source ring retains all history.
			transport, err := provider.NewWorkloadPeerTransport(fixture.runtimes[1])
			if err != nil {
				t.Fatal(err)
			}
			apply := &observedSnapshotApplier{LanternService: fixture.primary[1], ctx: ctx, began: make(chan *observedSnapshotInstall, 1)}
			replayApplied, successorApplied := make(chan error, 1), make(chan error, 1)
			replaySeq := fixture.primary[0].LocalSeq(sourceID) + 513
			successorSeq := replaySeq + 1
			if ringCapacity == 4 {
				successorSeq += 513 // Two rejected overflow cycles precede quiet recovery.
			}
			// Arm immutable targets before Pump starts. Notify only after the real
			// ApplyMutation returns, including its relay-log and cursor publication.
			apply.afterApply = func(mutation *pb.Mutation, err error) {
				if !bytes.Equal(mutation.GetOrigin(), sourceID[:]) {
					return
				}
				var applied chan error
				switch mutation.GetSeq() {
				case replaySeq:
					applied = replayApplied
				case successorSeq:
					applied = successorApplied
				default:
					return
				}
				select {
				case applied <- err:
				default: // Duplicate notification must never backpressure replication.
				}
			}
			waitApplied := func(label string, applied <-chan error) {
				t.Helper()
				select {
				case err := <-applied:
					if err != nil {
						t.Fatalf("%s ApplyMutation: %v", label, err)
					}
				case <-ctx.Done():
					t.Fatalf("%s did not apply within original test deadline: %v; receiver=%d source=%d faulted=%t Snapshots=%d", label, ctx.Err(), fixture.primary[1].LocalSeq(sourceID), fixture.primary[0].LocalSeq(sourceID), fixture.primary[1].SnapshotRepairRequired(), peer.snapshots.Load())
				}
			}
			pump := replication.NewPump(replication.Config{
				NodeID: hlc.NodeID{4}, NamespaceFormat: "namespaced-v1", Peers: []string{fixture.servers[0].URL},
				PeerTransport: transport, BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
			}, apply, graphs[1])
			done := make(chan error, 1)
			go func() { done <- pump.Run(ctx) }()
			defer func() {
				cancel()
				select {
				case err := <-done:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(time.Second):
					t.Error("capacity control Pump did not join")
				}
			}()
			cycles := 3
			if ringCapacity == 10000 {
				cycles = 1
			}
			for cycle := range cycles {
				var header observedSnapshotHeader
				var stage *observedSnapshotInstall
				select {
				case header = <-peer.headers:
				case <-ctx.Done():
					t.Fatal("Snapshot header", ctx.Err())
				}
				if fixture.primary[1].SnapshotInstallActive() || !fixture.primary[1].SnapshotRepairRequired() {
					t.Fatal("collection changed the pre-existing receiver repair state")
				}
				if cycle < 2 {
					for i := range 513 {
						put(fmt.Sprintf("data:overflow-%d-%d", cycle, i))
					}
					select {
					case <-drops:
					case <-ctx.Done():
						t.Fatal("513 writes did not trigger actual queue overflow", ctx.Err())
					}
				}
				first, _ := fixture.logs[0].FirstSeq()
				if cycle < 2 && (first <= header.header.GetCutoffLocalSeq()+1) != (ringCapacity == 10000) {
					t.Fatalf("ring control capacity=%d first=%d captured_next=%d", ringCapacity, first, header.header.GetCutoffLocalSeq()+1)
				}
				header.release <- nil
				var serverErr error
				select {
				case serverErr = <-peer.results:
				case <-ctx.Done():
					t.Fatal("Snapshot source completion", ctx.Err())
				}
				verified := false
				if cycle < 2 && ringCapacity == 4 {
					if connect.CodeOf(serverErr) != connect.CodeFailedPrecondition || !strings.Contains(serverErr.Error(), "Snapshot tail unavailable") {
						t.Fatalf("overflow cycle=%d source_error=%v", cycle, serverErr)
					}
					select {
					case stage := <-apply.began:
						close(stage.resume)
						t.Fatal("rejected overflow stream entered live replay")
					default:
					}
					if !fixture.primary[1].SnapshotRepairRequired() || fixture.primary[1].SnapshotInstallActive() || fixture.primary[1].LocalSeq(sourceID) != 0 {
						t.Fatal("pre-install rejection changed existing repair fault or origin")
					}
					if _, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:seed-0"}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
						t.Fatalf("faulted receiver read=%v", err)
					}
					if _, err := fixture.primary[1].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "data:refused"}}); connect.CodeOf(err) != connect.CodeFailedPrecondition {
						t.Fatalf("faulted receiver write=%v", err)
					}
				} else {
					if serverErr != nil {
						t.Fatal("complete Snapshot source", serverErr)
					}
					select {
					case stage = <-apply.began:
					case <-ctx.Done():
						t.Fatal("verified Snapshot install admission", ctx.Err())
					}
					select {
					case verified = <-stage.finished:
					case <-ctx.Done():
						t.Fatal("Snapshot receiver completion", ctx.Err())
					}
					if !verified || fixture.primary[1].SnapshotRepairRequired() {
						t.Fatalf("quiet recovery verified=%t faulted=%t", verified, fixture.primary[1].SnapshotRepairRequired())
					}
				}
				if _, err := fixture.primary[0].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:seed-0"}); err != nil {
					t.Fatal("healthy source read", err)
				}
				t.Logf("cycle=%d cutoff=%d retained_first=%d source=%d verified=%t receiver_faulted=%t source_error=%v", cycle+1, header.header.GetCutoffLocalSeq(), first, fixture.primary[0].LocalSeq(sourceID), verified, fixture.primary[1].SnapshotRepairRequired(), serverErr)
				if stage != nil {
					close(stage.resume)
				}
			}
			if ringCapacity == 10000 {
				waitApplied("retained replay", replayApplied)
				for i := range 513 {
					if _, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: fmt.Sprintf("data:overflow-0-%d", i)}); err != nil {
						t.Fatalf("retained replay lost update %d: %v", i, err)
					}
				}
			}
			put("data:after-quiet-repair")
			waitApplied("successor", successorApplied)
			if _, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:after-quiet-repair"}); err != nil {
				t.Fatal("recovered successor public read", err)
			}
			if _, err := fixture.primary[1].GetVertex(ctx, &pb.GetVertexRequest{Key: "data:seed-0"}); err != nil {
				t.Fatal("quiet recovery public read", err)
			}
			if got, want := fixture.primary[1].LocalSeq(sourceID), fixture.primary[0].LocalSeq(sourceID); got != want {
				t.Fatalf("recovered cursor=%d, source=%d", got, want)
			}
			if peer.snapshots.Load() != int32(cycles) {
				t.Fatalf("Snapshot count=%d, want%d", peer.snapshots.Load(), cycles)
			}

		})
	}
}

// mutualSnapshotPeer holds the source after its healthy cut/tail is owned,
// then holds the terminal response after the complete source-side check. This
// distinguishes receiving a Header or Footer from verifying the final status.
type mutualSnapshotPeer struct {
	graphv1connect.LanternReplicationServiceHandler
	source   *service.LanternReplicationService
	origin   string
	captured chan observedSnapshotHeader
	body     <-chan struct{}
	terminal <-chan struct{}
	results  chan error
	claims   chan error
	resumed  chan *pb.SubscribeRequest
	calls    atomic.Int32
}

func (p *mutualSnapshotPeer) Snapshot(ctx context.Context, request *connect.Request[pb.SnapshotRequest], stream *connect.ServerStream[pb.SnapshotResponse]) error {
	if p.calls.Add(1) != 1 {
		return p.LanternReplicationServiceHandler.Snapshot(ctx, request, stream)
	}
	ctx = service.WithReplicationTailMetadata(ctx, request.Header().Values(service.SnapshotTailHeader), stream.ResponseHeader())
	err := p.source.Snapshot(ctx, request.Msg, mutualSnapshotSender{ctx: ctx, stream: stream, captured: p.captured, body: p.body})
	select {
	case p.results <- err:
	default:
	}
	select {
	case <-p.terminal:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (p *mutualSnapshotPeer) Subscribe(ctx context.Context, request *connect.Request[pb.SubscribeRequest], stream *connect.ServerStream[pb.SubscribeResponse]) error {
	claim := len(request.Header().Values(service.SnapshotTailHeader)) != 0
	if !claim && request.Msg.GetFromSeqPerOrigin()[p.origin] == 9 {
		select {
		case p.resumed <- proto.Clone(request.Msg).(*pb.SubscribeRequest):
		default:
		}
	}
	err := p.LanternReplicationServiceHandler.Subscribe(ctx, request, stream)
	if claim {
		select {
		case p.claims <- err:
		default:
		}
	}
	return err
}

type mutualSnapshotSender struct {
	ctx      context.Context
	stream   *connect.ServerStream[pb.SnapshotResponse]
	captured chan<- observedSnapshotHeader
	body     <-chan struct{}
}

// Observe the next Receive after a Header or Footer has been returned to the
// selected production installer. The callbacks do not hold the stream or alter
// native admission. A rejected collector can be held before Pump retries.
type observedGraphSnapshotInstaller struct {
	replication.SnapshotInstaller
	headerConsumed chan<- struct{}
	footerConsumed chan<- struct{}
	failed         chan<- error
	retry          <-chan struct{}
}

func (i observedGraphSnapshotInstaller) Install(ctx context.Context, stream replication.SnapshotStream) (replication.SnapshotInstallResult, error) {
	result, err := i.SnapshotInstaller.Install(ctx, &observedGraphSnapshotStream{
		SnapshotStream: stream, headerConsumed: i.headerConsumed, footerConsumed: i.footerConsumed,
	})
	if err != nil && i.failed != nil {
		select {
		case i.failed <- err:
		case <-ctx.Done():
			return result, ctx.Err()
		}
		select {
		case <-i.retry:
		case <-ctx.Done():
			return result, ctx.Err()
		}
	}
	return result, err
}

func (i observedGraphSnapshotInstaller) SnapshotTransportLimits() replication.SnapshotTransportLimits {
	return i.SnapshotInstaller.(interface {
		SnapshotTransportLimits() replication.SnapshotTransportLimits
	}).SnapshotTransportLimits()
}

type observedGraphSnapshotStream struct {
	replication.SnapshotStream
	headerConsumed chan<- struct{}
	footerConsumed chan<- struct{}
	headerSeen     bool
	footerSeen     bool
	headerNotified bool
	footerNotified bool
}

func (s *observedGraphSnapshotStream) Receive() bool {
	if s.headerSeen && !s.headerNotified {
		s.headerNotified = true
		select {
		case s.headerConsumed <- struct{}{}:
		default:
		}
	}
	if s.footerSeen && !s.footerNotified {
		s.footerNotified = true
		select {
		case s.footerConsumed <- struct{}{}:
		default:
		}
	}
	if !s.SnapshotStream.Receive() {
		return false
	}
	if s.Msg().GetHeader() != nil {
		s.headerSeen = true
	}
	if s.Msg().GetFooter() != nil {
		s.footerSeen = true
	}
	return true
}

func (s mutualSnapshotSender) Send(frame *pb.SnapshotResponse) error {
	if frame.GetHeader() == nil {
		return s.stream.Send(frame)
	}
	cut := observedSnapshotHeader{header: proto.Clone(frame.GetHeader()).(*pb.SnapshotHeader), release: make(chan error, 1)}
	select {
	case s.captured <- cut:
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	select {
	case err := <-cut.release:
		if err != nil {
			return err
		}
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
	if err := s.stream.Send(frame); err != nil {
		return err
	}
	select {
	case <-s.body:
		return nil
	case <-s.ctx.Done():
		return s.ctx.Err()
	}
}

// Exercise both complete sources and an asymmetric first install against a
// still-transferring source. This controls the GRAPH_ONLY mutual-repair
// interleaving without attributing it to a saved load run. No post-cut eviction,
// queue overflow, failed WAL, native expiry or external time source is involved.
func TestPeerPump_MutualSnapshotInstallDefersPublicationUntilSourceCompletion(t *testing.T) {
	for _, asymmetric := range []bool{false, true} {
		name := "both sources complete before live install"
		if asymmetric {
			name = "first live install invalidates opposite pending source"
		}
		t.Run(name, func(t *testing.T) { testPeerPumpMutualSnapshotInstall(t, asymmetric) })
	}
}

func testPeerPumpMutualSnapshotInstall(t *testing.T, asymmetric bool) {
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()
	qualifiedUTC, began, clockOwner := time.Now().UTC(), time.Now(), new(byte)
	body := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	terminal := [2]chan struct{}{make(chan struct{}), make(chan struct{})}
	var peers [2]*mutualSnapshotPeer
	var graphs [2]*graphcache.GraphCache[string, *pb.Vertex]
	fixture := newPeerSecurityFixture(t, peerSecurityOptions{
		Now:        func() time.Time { return qualifiedUTC },
		TimeBounds: func() (time.Time, time.Time, error) { return qualifiedUTC, qualifiedUTC, nil },
		Time: func() (provider.PeerTimeObservation, error) {
			return peerWireObservation{clockOwner, qualifiedUTC, qualifiedUTC, time.Since(began)}, nil
		},
		IssuedBefore: time.Minute,
		LogOptions:   func(int) mutationlog.Options { return mutationlog.Options{Capacity: 4, SubscriberBuffer: 512} },
		Mount: func(f *peerSecurityFixture, index int, runtime *service.ServingRuntime, mux *http.ServeMux) {
			graphs[index] = runtime.GraphCache()
			source, err := runtime.NewLanternReplicationService(f.primary[index])
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(source.CloseSnapshotTails)
			origin := hlc.NodeID{byte(index + 3)}
			peers[index] = &mutualSnapshotPeer{
				LanternReplicationServiceHandler: service.NewLanternReplicationServiceConnectHandler(source),
				source:                           source, origin: hex.EncodeToString(origin[:]),
				captured: make(chan observedSnapshotHeader, 1), body: body[index], terminal: terminal[index],
				results: make(chan error, 4), claims: make(chan error, 4), resumed: make(chan *pb.SubscribeRequest, 1),
			}
			_, handler := graphv1connect.NewLanternReplicationServiceHandler(peers[index])
			for _, method := range []string{"PeerStatus", "Snapshot", "Subscribe"} {
				mux.Handle("/graph.v1.LanternReplicationService/"+method, handler)
			}
		},
	})
	put := func(node int, key string) {
		t.Helper()
		if _, err := fixture.primary[node].PutVertex(ctx, &pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: "data:" + key, Value: &pb.Vertex_String_{String_: "value"}, Expiration: timestamppb.New(qualifiedUTC.Add(time.Hour)),
		}}); err != nil {
			t.Fatalf("node%d seed/write: %v", node, err)
		}
	}
	var public [2]graphv1connect.LanternServiceClient
	var wire [2]graphv1connect.LanternReplicationServiceClient
	for node := range 2 {
		for sequence := range 8 {
			put(node, fmt.Sprintf("mutual-%d-seed-%d", node, sequence))
		}
		server := newConnectTestServer(t, fixture.primary[node], nil)
		public[node] = graphv1connect.NewLanternServiceClient(h2cClient(), server.url)
		wire[node] = graphv1connect.NewLanternReplicationServiceClient(fixture.runtimes[1-node].HTTPClient(), fixture.servers[node].URL)
		status, err := wire[node].PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"}))
		if err != nil || status.Msg.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 {
			t.Fatalf("node%d native GRAPH_ONLY precondition: %v, %v", node, status, err)
		}
	}
	var applies [2]*observedSnapshotApplier
	var headerConsumed [2]chan struct{}
	var footerConsumed [2]chan struct{}
	var failed [2]chan error
	var retry [2]chan struct{}
	var stops [2]context.CancelFunc
	var done [2]chan error
	var joined [2]bool
	defer func() {
		for _, stop := range stops {
			if stop != nil {
				stop()
			}
		}
		for node := range 2 {
			if done[node] != nil && !joined[node] {
				select {
				case err := <-done[node]:
					if err != nil {
						t.Error(err)
					}
				case <-time.After(time.Second):
					t.Errorf("node%d mutual Snapshot Pump did not join", node)
				}
			}
		}
	}()
	for receiver := range 2 {
		transport, err := provider.NewWorkloadPeerTransport(fixture.runtimes[receiver])
		if err != nil {
			t.Fatal(err)
		}
		runCtx, stop := context.WithCancel(ctx)
		stops[receiver], done[receiver] = stop, make(chan error, 1)
		applies[receiver] = &observedSnapshotApplier{LanternService: fixture.primary[receiver], ctx: runCtx, began: make(chan *observedSnapshotInstall, 1)}
		headerConsumed[receiver] = make(chan struct{}, 1)
		footerConsumed[receiver] = make(chan struct{}, 1)
		failed[receiver], retry[receiver] = make(chan error, 1), make(chan struct{})
		installer := observedGraphSnapshotInstaller{
			SnapshotInstaller: replication.NewGraphOnlySnapshotInstaller(applies[receiver], graphs[receiver]), headerConsumed: headerConsumed[receiver],
			footerConsumed: footerConsumed[receiver], failed: failed[receiver], retry: retry[receiver],
		}
		pump := replication.NewPump(replication.Config{
			NodeID: hlc.NodeID{byte(receiver + 3)}, NamespaceFormat: "namespaced-v1", Peers: []string{fixture.servers[1-receiver].URL},
			PeerTransport: transport, SnapshotInstaller: installer, BackoffMin: time.Millisecond, BackoffMax: time.Millisecond,
		}, applies[receiver], graphs[receiver])
		go func() { done[receiver] <- pump.Run(runCtx) }()
	}
	var cuts [2]observedSnapshotHeader
	for source := range 2 {
		select {
		case cuts[source] = <-peers[source].captured:
		case <-ctx.Done():
			t.Fatalf("source%d did not capture before either Header: %v", source, ctx.Err())
		}
		if cuts[source].header.GetCutoffLocalSeq() != 8 || cuts[source].header.GetFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 ||
			fixture.primary[source].SnapshotRepairRequired() {
			t.Fatalf("source%d did not own a healthy GRAPH_ONLY cut8", source)
		}
	}
	for source := range 2 {
		// The observer projects no retained payload. Seeing seq10 and then
		// joining its Close proves the single dispatcher completed fan-out of
		// seq9 and seq10 to the pre-existing native reservation as well. Thus
		// failure cleanup reclaims real charged frames, not only empty owners.
		projected := make(chan struct{}, 1)
		observer, err := fixture.logs[source].SubscribeTail(9, 2, func(entry mutationlog.Entry) (mutationlog.Entry, error) {
			if entry.Seq == 10 {
				select {
				case projected <- struct{}{}:
				default:
				}
			}
			return mutationlog.Entry{Seq: entry.Seq, HLC: entry.HLC}, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = observer.Close() })
		put(source, fmt.Sprintf("mutual-%d-tail-9", source))
		put(source, fmt.Sprintf("mutual-%d-tail-10", source))
		select {
		case <-projected:
		case <-ctx.Done():
			t.Fatal("post-cut charged-frame barrier", ctx.Err())
		}
		if err := observer.Close(); err != nil {
			t.Fatal(err)
		}
		first, hasFirst := fixture.logs[source].FirstSeq()
		last, hasLast := fixture.logs[source].LastSeq()
		origin := hlc.NodeID{byte(source + 3)}
		cut := cuts[source].header
		if !hasFirst || !hasLast || first != 7 || last != 10 ||
			cut.GetCutoffLocalSeq() != 8 || cut.GetCutoffSeqPerOrigin()[hex.EncodeToString(origin[:])] != 8 {
			t.Fatalf("source%d changed its original cut or evicted needed tail9: cut=%d retained=%d..%d", source, cut.GetCutoffLocalSeq(), first, last)
		}
		t.Logf("source%d original_cut=8 retained_first=%d retained_last=%d needed_next=9 queued_postcut=2", source, first, last)
	}
	for source := range 2 {
		cuts[source].release <- nil
	}
	for receiver := range 2 {
		select {
		case <-headerConsumed[receiver]:
		case <-ctx.Done():
			t.Fatalf("receiver%d did not consume its native Header: %v", receiver, ctx.Err())
		}
	}
	assertPrepublication := func(phase string) {
		t.Helper()
		for node := range 2 {
			select {
			case stage := <-applies[node].began:
				close(stage.resume)
				t.Fatalf("node%d began live install before verified terminal: %s", node, phase)
			default:
			}
			if fixture.primary[node].SnapshotInstallActive() || fixture.primary[node].SnapshotRepairRequired() ||
				fixture.primary[node].LocalSeq(hlc.NodeID{byte(node + 3)}) != 10 ||
				fixture.primary[node].LocalSeq(hlc.NodeID{byte(4 - node)}) != 0 {
				t.Fatalf("node%d changed live publication while %s", node, phase)
			}
			response, err := public[node].GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: fmt.Sprintf("mutual-%d-seed-0", node)}))
			if err != nil || response.Msg.GetVertex().GetString_() != "value" {
				t.Fatalf("node%d healthy public read while %s: %v, %v", node, phase, response, err)
			}
			status, err := wire[node].PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"}))
			if err != nil || status.Msg.GetRequiredSnapshotFormat() != pb.SnapshotFormat_SNAPSHOT_FORMAT_GRAPH_ONLY_V1 {
				t.Fatalf("node%d healthy native status while %s: %v, %v", node, phase, status, err)
			}
		}
	}
	assertPrepublication("bodies held")
	waitSource := func(source int, invalidated bool) {
		t.Helper()
		select {
		case err := <-peers[source].results:
			if invalidated {
				if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "stage=finish") || !strings.Contains(err.Error(), "terminal=publication_fault") {
					t.Fatalf("source%d did not identify its invalidated original reservation: %v", source, err)
				}
			} else if err != nil {
				t.Fatalf("source%d failed before either live install: %v", source, err)
			}
			t.Logf("source%d original_cut=8 queued_postcut=2 invalidated=%t terminal_held=true finish=%v", source, invalidated, err)
		case <-ctx.Done():
			t.Fatal("mutual source finish", ctx.Err())
		}
	}
	waitFooter := func(receiver int) {
		t.Helper()
		select {
		case <-footerConsumed[receiver]:
		case <-ctx.Done():
			t.Fatalf("receiver%d did not consume Footer before final status: %v", receiver, ctx.Err())
		}
	}
	var stages [2]*observedSnapshotInstall
	waitVerified := func(receiver int) {
		t.Helper()
		select {
		case stages[receiver] = <-applies[receiver].began:
		case <-ctx.Done():
			t.Fatalf("receiver%d did not admit verified image: %v", receiver, ctx.Err())
		}
		select {
		case verified := <-stages[receiver].finished:
			if !verified {
				t.Fatalf("receiver%d rejected its verified staged image", receiver)
			}
		case <-ctx.Done():
			t.Fatal("mutual receiver completion", ctx.Err())
		}
	}
	claimSources := []int{0, 1}
	wantSnapshots := [2]int32{1, 1}
	if !asymmetric {
		for source := range 2 {
			close(body[source])
			waitSource(source, false)
		}
		for receiver := range 2 {
			waitFooter(receiver)
		}
		// Both default collectors have consumed their Footer and requested
		// another Receive. A Footer-only installer cannot satisfy this barrier.
		assertPrepublication("both Footers consumed awaiting final status")
		for source := range 2 {
			close(terminal[source])
		}
		for receiver := range 2 {
			waitVerified(receiver)
		}
	} else {
		// Receiver0 can finish first. Its live Begin closes source0's old
		// publication generation while source0's outgoing body is still held.
		close(body[1])
		waitSource(1, false)
		waitFooter(0)
		assertPrepublication("first Footer consumed awaiting final status")
		close(terminal[1])
		waitVerified(0)
		assertPendingReceiverHealthy := func(phase string) {
			t.Helper()
			select {
			case stage := <-applies[1].began:
				close(stage.resume)
				t.Fatalf("unverified receiver1 began live replay while %s", phase)
			default:
			}
			if fixture.primary[1].SnapshotInstallActive() || fixture.primary[1].SnapshotRepairRequired() ||
				fixture.primary[1].LocalSeq(hlc.NodeID{3}) != 0 || fixture.primary[1].LocalSeq(hlc.NodeID{4}) != 10 || peers[0].calls.Load() != 1 {
				t.Fatalf("receiver1 changed healthy state before bounded retry while %s", phase)
			}
			response, err := public[1].GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "mutual-1-seed-0"}))
			if err != nil || response.Msg.GetVertex().GetString_() != "value" {
				t.Fatalf("pending receiver public read while %s: %v, %v", phase, response, err)
			}
			if _, err := wire[1].PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"})); err != nil {
				t.Fatalf("pending receiver native status while %s: %v", phase, err)
			}
		}
		assertPendingReceiverHealthy("opposite source is held after first verified install")
		close(body[0])
		waitSource(0, true)
		waitFooter(1)
		assertPendingReceiverHealthy("invalidated source Footer consumed awaiting non-OK terminal")
		close(terminal[0])
		select {
		case err := <-failed[1]:
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "terminal=publication_fault") {
				t.Fatalf("pending collector did not reject non-OK final status: %v", err)
			}
		case <-ctx.Done():
			t.Fatal("invalidated collector did not finish", ctx.Err())
		}
		assertPendingReceiverHealthy("non-OK collector rejected before retry")
		close(retry[1])
		waitVerified(1)
		claimSources = []int{1} // Source0's retry owns a new, valid reservation.
		wantSnapshots[0] = 2
	}
	for receiver := range 2 {
		close(stages[receiver].resume)
	}
	for _, source := range claimSources {
		select {
		case err := <-peers[source].claims:
			if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "Snapshot tail unavailable") {
				t.Fatalf("source%d did not reject reservation invalidated by its live install: %v", source, err)
			}
		case <-ctx.Done():
			t.Fatal("mutual old reservation claim", ctx.Err())
		}
		select {
		case request := <-peers[source].resumed:
			if request.GetFromLocalSeq() != 0 || request.GetNamespaceFormat() != "namespaced-v1" || !request.GetAcceptGraphMutations() {
				t.Fatalf("source%d invalid ordinary Subscribe resume: %v", source, request)
			}
		case <-ctx.Done():
			t.Fatal("mutual ordinary Subscribe resume", ctx.Err())
		}
	}
	for receiver := range 2 {
		for origin := range 2 {
			if !waitForVertex(t, graphs[receiver], fmt.Sprintf("data:mutual-%d-tail-10", origin), time.Second) {
				t.Fatalf("receiver%d lost origin%d post-cut effect", receiver, origin)
			}
			// Public reads fence the graph effect and its committed origin row.
			for sequence := range 8 {
				response, err := public[receiver].GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: fmt.Sprintf("mutual-%d-seed-%d", origin, sequence)}))
				if err != nil || response.Msg.GetVertex().GetString_() != "value" {
					t.Fatalf("receiver%d missing origin%d seed%d: %v, %v", receiver, origin, sequence, response, err)
				}
			}
			for _, sequence := range []int{9, 10} {
				response, err := public[receiver].GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: fmt.Sprintf("mutual-%d-tail-%d", origin, sequence)}))
				if err != nil || response.Msg.GetVertex().GetString_() != "value" {
					t.Fatalf("receiver%d missing origin%d tail%d: %v, %v", receiver, origin, sequence, response, err)
				}
			}
			if got := fixture.primary[receiver].LocalSeq(hlc.NodeID{byte(origin + 3)}); got != 10 {
				t.Fatalf("receiver%d origin%d committed=%d, want 10", receiver, origin, got)
			}
		}
		if fixture.primary[receiver].SnapshotInstallActive() || fixture.primary[receiver].SnapshotRepairRequired() || peers[receiver].calls.Load() != wantSnapshots[receiver] {
			t.Fatalf("receiver%d did not recover within bounded Snapshot count=%d, want %d", receiver, peers[receiver].calls.Load(), wantSnapshots[receiver])
		}
	}
	if _, err := public[0].PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
		Key: "mutual-public-after-verified", Value: &pb.Vertex_String_{String_: "public-value"}, Expiration: timestamppb.New(qualifiedUTC.Add(time.Hour)),
	}})); err != nil {
		t.Fatal("verified mutual install did not restore public write", err)
	}
	if !waitForVertex(t, graphs[1], "data:mutual-public-after-verified", time.Second) {
		t.Fatal("ordinary native Subscribe did not carry a fresh public write")
	}
	response, err := public[1].GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "mutual-public-after-verified"}))
	if err != nil || response.Msg.GetVertex().GetString_() != "public-value" || fixture.primary[1].LocalSeq(hlc.NodeID{3}) != 11 {
		t.Fatalf("fresh public effect/cursor did not converge: %v, %v", response, err)
	}
	for receiver := range 2 {
		stops[receiver]()
	}
	for receiver := range 2 {
		select {
		case err := <-done[receiver]:
			joined[receiver] = true
			if err != nil {
				t.Fatal(err)
			}
		case <-ctx.Done():
			t.Fatal("mutual Pump join", ctx.Err())
		}
	}
	for node := range 2 {
		if peers[node].calls.Load() != wantSnapshots[node] {
			t.Fatalf("node%d Snapshot count=%d, want %d", node, peers[node].calls.Load(), wantSnapshots[node])
		}
		if _, err := wire[node].PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{NamespaceFormat: "namespaced-v1"})); err != nil {
			t.Fatalf("node%d verified native PeerStatus=%v", node, err)
		}
		closed := make(chan struct{})
		go func() { peers[node].source.CloseSnapshotTails(); close(closed) }()
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatalf("node%d failed to join invalidated reservation owners", node)
		}
	}
	t.Logf("native GRAPH_ONLY final status preceded live publication; asymmetric=%t bounded_Snapshots=%v, invalid old reservations refused, ordinary Subscribe converged both origin10 cuts plus fresh public write, owners joined; no saved load-run attribution or external NTP", asymmetric, wantSnapshots)
}
