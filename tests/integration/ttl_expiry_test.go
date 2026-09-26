package integration_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// This file pins Lantern's defining product semantic — data decays — as an
// externally observable contract (#935): a client that writes with a TTL
// must see the entry vanish from every point read once the TTL elapses.
// Reads hide expired-but-not-yet-swept entries lazily (vertices.Has routes
// through Get; edges are hidden unless both endpoints are live, #750), so
// none of these assertions depend on GC timing. Expiry itself does depend on
// the wall clock, hence the poll helper: assert liveness strictly before the
// TTL, then poll with a generous deadline for the flip to ErrNotFound.

const (
	expiryTTL          = 500 * time.Millisecond
	expiryPollDeadline = 10 * time.Second
	expiryPollTick     = 20 * time.Millisecond
)

// eventuallyNotFound polls read until it reports client.ErrNotFound, failing
// the test if the entry is still readable after expiryPollDeadline.
func eventuallyNotFound(t *testing.T, what string, read func() error) {
	t.Helper()
	deadline := time.Now().Add(expiryPollDeadline)
	for {
		err := read()
		if errors.Is(err, client.ErrNotFound) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s still readable %v after its TTL (last err: %v)", what, expiryPollDeadline, err)
		}
		if err != nil {
			t.Fatalf("%s: unexpected error while waiting for expiry: %v", what, err)
		}
		time.Sleep(expiryPollTick)
	}
}

func TestTTL_ExternallyObservableDecay(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	t.Run("vertex expires after its TTL", func(t *testing.T) {
		l, cleanup := newInProcessClient(t)
		defer cleanup()

		if _, err := l.PutVertex(ctx, "mortal", "v", expiryTTL); err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
		if _, err := l.GetVertex(ctx, "mortal"); err != nil {
			t.Fatalf("vertex must be readable before its TTL: %v", err)
		}
		eventuallyNotFound(t, "vertex", func() error {
			_, err := l.GetVertex(ctx, "mortal")
			return err
		})
	})

	t.Run("edge expires after its TTL while endpoints stay live", func(t *testing.T) {
		l, cleanup := newInProcessClient(t)
		defer cleanup()

		if _, err := l.PutVertex(ctx, "tail", 1, time.Hour); err != nil {
			t.Fatalf("PutVertex tail: %v", err)
		}
		if _, err := l.PutVertex(ctx, "head", 2, time.Hour); err != nil {
			t.Fatalf("PutVertex head: %v", err)
		}
		if _, err := l.AddEdge(ctx, "tail", "head", 1, expiryTTL); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if _, err := l.GetEdge(ctx, "tail", "head"); err != nil {
			t.Fatalf("edge must be readable before its TTL: %v", err)
		}
		eventuallyNotFound(t, "edge", func() error {
			_, err := l.GetEdge(ctx, "tail", "head")
			return err
		})
		// The endpoints outlive their edge.
		if _, err := l.GetVertex(ctx, "tail"); err != nil {
			t.Fatalf("tail vertex must survive its edge: %v", err)
		}
	})

	t.Run("expired endpoint hides a still-live edge", func(t *testing.T) {
		// Referential closure (#750): an edge may physically outlive an
		// expired endpoint until GC, but no read may expose it.
		l, cleanup := newInProcessClient(t)
		defer cleanup()

		if _, err := l.PutVertex(ctx, "ephemeral", 1, expiryTTL); err != nil {
			t.Fatalf("PutVertex ephemeral: %v", err)
		}
		if _, err := l.PutVertex(ctx, "durable", 2, time.Hour); err != nil {
			t.Fatalf("PutVertex durable: %v", err)
		}
		if _, err := l.AddEdge(ctx, "ephemeral", "durable", 1, time.Hour); err != nil {
			t.Fatalf("AddEdge: %v", err)
		}
		if _, err := l.GetEdge(ctx, "ephemeral", "durable"); err != nil {
			t.Fatalf("edge must be readable while both endpoints live: %v", err)
		}
		eventuallyNotFound(t, "edge with expired tail", func() error {
			_, err := l.GetEdge(ctx, "ephemeral", "durable")
			return err
		})
	})
}

func TestTTL_ExplicitEpochDeadlines_RealConnectWire(t *testing.T) {
	raw, _ := newRawConnectClient(t, false)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	valueTime := time.Unix(-1, 500_000_000).UTC()
	seed, err := raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "tail"}, {Key: "head"}, {Key: "pre-head"}, {Key: "fraction-head"},
		{Key: "victim"},
		{Key: "dated", Value: &pb.Vertex_Timestamp{Timestamp: timestamppb.New(valueTime)}},
	}}))
	if err != nil || len(seed.Msg.GetOutcomes()) != 6 {
		t.Fatalf("seed permanent vertices = (%+v, %v)", seed, err)
	}
	for _, outcome := range seed.Msg.GetOutcomes() {
		if outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
			t.Fatalf("seed outcome = %v, want APPLIED_AND_LIVE", outcome)
		}
	}
	dated, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "dated"}))
	if err != nil || dated.Msg.GetVertex().GetExpiration() != nil ||
		!dated.Msg.GetVertex().GetTimestamp().AsTime().Equal(valueTime) {
		t.Fatalf("permanent pre-epoch value = (%+v, %v), want %v", dated, err, valueTime)
	}
	if seededEdge, err := raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{
		Edge: &pb.Edge{Tail: "tail", Head: "head", Weight: 2},
	})); err != nil || seededEdge.Msg.GetOutcome() != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
		t.Fatalf("seed permanent edge = (%+v, %v)", seededEdge, err)
	}

	expirations := []*timestamppb.Timestamp{
		timestamppb.New(time.Unix(0, 0).UTC()),
		timestamppb.New(time.Unix(-1, 0).UTC()),
		timestamppb.New(time.Unix(0, 500_000_000).UTC()),
	}
	expiredVertices := []*pb.Vertex{
		{Key: "victim", Expiration: expirations[0]},
		{Key: "pre-expired", Expiration: expirations[1]},
		{Key: "fraction-expired", Expiration: expirations[2]},
	}
	put, err := raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: expiredVertices}))
	expired := []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_EXPIRED, pb.PutOutcome_PUT_OUTCOME_EXPIRED, pb.PutOutcome_PUT_OUTCOME_EXPIRED}
	if err != nil || !slices.Equal(put.Msg.GetOutcomes(), expired) {
		t.Fatalf("epoch vertex Put outcomes = (%+v, %v), want %v", put, err, expired)
	}
	for _, v := range expiredVertices {
		if _, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: v.Key})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("expired vertex %s remained readable: %v", v.Key, err)
		}
	}

	expiredEdges := []*pb.Edge{
		{Tail: "tail", Head: "head", Weight: 3, Expiration: expirations[0]},
		{Tail: "tail", Head: "pre-head", Weight: 3, Expiration: expirations[1]},
		{Tail: "tail", Head: "fraction-head", Weight: 3, Expiration: expirations[2]},
	}
	edges, err := raw.PutEdges(ctx, connect.NewRequest(&pb.PutEdgesRequest{Edges: expiredEdges}))
	if err != nil || !slices.Equal(edges.Msg.GetOutcomes(), expired) {
		t.Fatalf("epoch edge Put outcomes = (%+v, %v), want %v", edges, err, expired)
	}
	add, err := raw.AddEdges(ctx, connect.NewRequest(&pb.AddEdgesRequest{Edges: expiredEdges}))
	if err != nil || add.Msg.GetWritten() != 3 || !slices.Equal(add.Msg.GetEffectiveWeights(), []float32{0, 0, 0}) {
		t.Fatalf("epoch edge Add = (%+v, %v), want three zero effective weights", add, err)
	}
	for _, edge := range expiredEdges {
		if _, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{
			Tail: edge.Tail, Head: edge.Head,
		})); connect.CodeOf(err) != connect.CodeNotFound {
			t.Fatalf("expired edge %s->%s remained readable: %v", edge.Tail, edge.Head, err)
		}
	}
	permanent, err := raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{
		Edge: &pb.Edge{Tail: "tail", Head: "head", Weight: 4},
	}))
	if err != nil || permanent.Msg.GetEffectiveWeight() != 4 {
		t.Fatalf("omitted-expiration AddEdge = (%+v, %v)", permanent, err)
	}
	live, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "tail", Head: "head"}))
	if err != nil || live.Msg.GetEdge().GetWeight() != 4 || live.Msg.GetEdge().GetExpiration() != nil {
		t.Fatalf("permanent edge = (%+v, %v)", live, err)
	}

	for _, tc := range []struct {
		name string
		exp  *timestamppb.Timestamp
	}{
		{"explicit Go zero", timestamppb.New(time.Time{})},
		{"invalid nanos", &timestamppb.Timestamp{Nanos: -1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
				{Key: "partial"}, {Key: "invalid", Expiration: tc.exp},
			}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid vertex deadline = %v, want InvalidArgument", err)
			}
			if _, err := raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "partial"})); connect.CodeOf(err) != connect.CodeNotFound {
				t.Fatalf("invalid vertex batch partially applied: %v", err)
			}
			if _, err := raw.AddEdges(ctx, connect.NewRequest(&pb.AddEdgesRequest{Edges: []*pb.Edge{
				{Tail: "tail", Head: "head", Weight: 1},
				{Tail: "tail", Head: "head", Weight: 1, Expiration: tc.exp},
			}})); connect.CodeOf(err) != connect.CodeInvalidArgument {
				t.Fatalf("invalid Add deadline = %v, want InvalidArgument", err)
			}
			unchanged, err := raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "tail", Head: "head"}))
			if err != nil || unchanged.Msg.GetEdge().GetWeight() != 4 {
				t.Fatalf("invalid Add batch changed permanent edge: (%+v, %v)", unchanged, err)
			}
		})
	}
}

func TestTTL_GoSDKZeroTimeRemainsPermanent_RealConnectWire(t *testing.T) {
	l, cleanup := newInProcessClient(t)
	defer cleanup()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()

	if outcome, err := l.PutVertex(ctx, "sdk-tail", "tail", 0); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("zero-TTL PutVertex = (%v, %v)", outcome, err)
	}
	if outcome, err := l.PutVertexIfAbsentAt(ctx, "sdk-head", "head", time.Time{}); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("zero-time conditional PutVertex = (%v, %v)", outcome, err)
	}
	if outcomes, err := l.PutVertices(ctx, []client.VertexInput{{Key: "sdk-batch", Value: "live"}}); err != nil ||
		len(outcomes) != 1 || outcomes[0].Outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("omitted batch PutVertex = (%v, %v)", outcomes, err)
	}
	for _, key := range []string{"sdk-tail", "sdk-head", "sdk-batch"} {
		v, err := l.GetVertex(ctx, key)
		if err != nil || v.GetExpiration() != nil {
			t.Fatalf("SDK permanent vertex %q = (%v, %v)", key, v, err)
		}
	}
	if weight, err := l.AddEdge(ctx, "sdk-tail", "sdk-head", 2, 0); err != nil || weight != 2 {
		t.Fatalf("zero-TTL AddEdge = (%v, %v)", weight, err)
	}
	if outcome, err := l.PutEdgeAt(ctx, "sdk-tail", "sdk-batch", 3, time.Time{}); err != nil || outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("zero-time PutEdge = (%v, %v)", outcome, err)
	}
	if outcomes, err := l.PutEdges(ctx, []client.EdgeInput{{Tail: "sdk-head", Head: "sdk-batch", Weight: 4}}); err != nil ||
		len(outcomes) != 1 || outcomes[0].Outcome != client.PutOutcomeAppliedAndLive {
		t.Fatalf("omitted batch PutEdge = (%v, %v)", outcomes, err)
	}
	for _, edge := range []client.EdgeRef{
		{Tail: "sdk-tail", Head: "sdk-head"},
		{Tail: "sdk-tail", Head: "sdk-batch"},
		{Tail: "sdk-head", Head: "sdk-batch"},
	} {
		got, err := l.GetEdge(ctx, edge.Tail, edge.Head)
		if err != nil || got.GetExpiration() != nil {
			t.Fatalf("SDK permanent edge %v = (%v, %v)", edge, got, err)
		}
	}
	if outcome, err := l.PutVertexAt(ctx, "sdk-fractional", "expired", time.Unix(0, 500_000_000).UTC()); err != nil ||
		outcome != client.PutOutcomeExpired {
		t.Fatalf("SDK fractional-epoch deadline = (%v, %v), want EXPIRED", outcome, err)
	}
	if _, err := l.GetVertex(ctx, "sdk-fractional"); !errors.Is(err, client.ErrNotFound) {
		t.Fatalf("SDK fractional-epoch vertex = %v, want NotFound", err)
	}
}

func TestTTL_ExplicitEpochReceipt_RealConnectWire(t *testing.T) {
	const token = "ttl-receipt"
	wire := newPublicReceiptWireServer(t, hlc.NodeID{0xa9}, 8, token)
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	capability := publicReceiptCapability(t, wire, token)
	issued := time.UnixMilli(int64(capability.GetServerNowUnixMs())).Add(-time.Second)
	receiptContext := publicReceiptWireContext(t, capability, 0xa9, 2, issued)
	request := &pb.PutVerticesRequest{
		Vertices: []*pb.Vertex{
			{Key: "fractional", Value: &pb.Vertex_String_{String_: "gone"},
				Expiration: timestamppb.New(time.Unix(0, 500_000_000).UTC())},
			{Key: "permanent", Value: &pb.Vertex_String_{String_: "live"}},
		},
		ReceiptContext: receiptContext,
	}
	response, err := wire.raw.PutVertices(ctx, receiptRequestWithToken(request, token))
	want := []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_EXPIRED, pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}
	if err != nil || !slices.Equal(response.Msg.GetOutcomes(), want) {
		t.Fatalf("receipt Put outcomes = (%+v, %v), want %v", response, err, want)
	}
	statuses, err := wire.raw.GetReceiptStatuses(ctx, receiptRequestWithToken(&pb.GetReceiptStatusesRequest{
		OperationIds: receiptContext.GetOperationIds(),
	}, token))
	if err != nil || len(statuses.Msg.GetStatuses()) != len(want) {
		t.Fatalf("receipt statuses = (%+v, %v)", statuses, err)
	}
	for i, status := range statuses.Msg.GetStatuses() {
		if status.GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED ||
			status.GetReceipt().GetOriginalResult().GetPutVertexOutcome() != want[i] {
			t.Fatalf("receipt status[%d] = %+v, want %v", i, status, want[i])
		}
	}
	if _, err := wire.raw.GetVertex(ctx, receiptRequestWithToken(&pb.GetVertexRequest{Key: "fractional"}, token)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("receipt expired Put remained readable: %v", err)
	}
	if live, err := wire.raw.GetVertex(ctx, receiptRequestWithToken(&pb.GetVertexRequest{Key: "permanent"}, token)); err != nil ||
		live.Msg.GetVertex().GetString_() != "live" || live.Msg.GetVertex().GetExpiration() != nil {
		t.Fatalf("receipt permanent Put = (%+v, %v)", live, err)
	}

	contribID := make([]byte, len(graphcache.ContribID{}))
	contribID[0] = 1
	addContext := publicReceiptWireContext(t, capability, 0xaa, 1, issued)
	add, err := wire.raw.AddEdges(ctx, receiptRequestWithToken(&pb.AddEdgesRequest{
		Edges: []*pb.Edge{{Tail: "tail", Head: "head", Weight: 2,
			Expiration: timestamppb.New(time.Unix(-1, 0).UTC())}},
		ContribIds:     [][]byte{contribID},
		ReceiptContext: addContext,
	}, token))
	if err != nil || add.Msg.GetWritten() != 1 || !slices.Equal(add.Msg.GetEffectiveWeights(), []float32{0}) {
		t.Fatalf("receipt pre-epoch Add = (%+v, %v), want zero effective weight", add, err)
	}
	status, err := wire.raw.GetReceiptStatus(ctx, receiptRequestWithToken(&pb.GetReceiptStatusRequest{
		OperationId: addContext.GetOperationIds()[0],
	}, token))
	if err != nil || status.Msg.GetStatus().GetState() != pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED {
		t.Fatalf("receipt Add status = (%+v, %v)", status, err)
	}
	result, ok := status.Msg.GetStatus().GetReceipt().GetOriginalResult().GetResult().(*pb.ReceiptResult_AddEdgeEffectiveWeight)
	if !ok || result.AddEdgeEffectiveWeight != 0 {
		t.Fatalf("receipt Add original result = %+v, want zero effective weight", result)
	}
	if _, err := wire.raw.GetEdge(ctx, receiptRequestWithToken(&pb.GetEdgeRequest{
		Tail: "tail", Head: "head",
	}, token)); connect.CodeOf(err) != connect.CodeNotFound {
		t.Fatalf("receipt expired Add remained readable: %v", err)
	}
}

func TestTTL_BudgetedGCDoesNotDelayWireHidingAndEventuallyReclaimsDangling(t *testing.T) {
	cache := provider.NewGraphCache(provider.CacheConfig{TTL: time.Hour, GCEdgeBudget: 1}, provider.SearchConfig{})
	svc := service.NewLanternService(cache)
	validation := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, validation.ConnectInterceptor())
	l := newConnectClientFor(t, srv.url)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, key := range []string{"a", "b", "c"} {
		if _, err := l.PutVertex(ctx, key, key, time.Hour); err != nil {
			t.Fatalf("PutVertex(%s): %v", key, err)
		}
	}
	if _, err := l.PutVertex(ctx, "short_head", "head", expiryTTL); err != nil {
		t.Fatalf("PutVertex(short_head): %v", err)
	}
	for _, tail := range []string{"a", "b", "c"} {
		if _, err := l.AddEdge(ctx, tail, "short_head", 1, time.Hour); err != nil {
			t.Fatalf("AddEdge(%s): %v", tail, err)
		}
		if _, err := l.GetEdge(ctx, tail, "short_head"); err != nil {
			t.Fatalf("live GetEdge(%s): %v", tail, err)
		}
	}
	eventuallyNotFound(t, "short_head", func() error {
		_, err := l.GetVertex(ctx, "short_head")
		return err
	})
	// No GC has run yet, so all three buckets remain physically present, but
	// the wire contract already hides every edge with the expired endpoint.
	if got := cache.EdgeCount(); got != 3 {
		t.Fatalf("physical edges before GC = %d, want 3", got)
	}
	for _, tail := range []string{"a", "b", "c"} {
		if _, err := l.GetEdge(ctx, tail, "short_head"); !errors.Is(err, client.ErrNotFound) {
			t.Fatalf("GetEdge(%s) after endpoint expiry = %v, want NotFound", tail, err)
		}
	}

	statsCh := make(chan graphcache.GCSweepStats, 8)
	cache.SetGCHooks(nil, func(time.Duration) {
		select {
		case statsCh <- cache.LastGCSweepStats():
		default:
		}
	})
	watchCtx, stop := context.WithCancel(ctx)
	watchDone := make(chan struct{})
	go func() { cache.Watch(watchCtx, 10*time.Millisecond); close(watchDone) }()
	defer func() { stop(); <-watchDone }()
	var removed int
	for removed < 3 {
		select {
		case stats := <-statsCh:
			if stats.ScannedTails > 1 {
				t.Fatalf("one tick scanned %d tails under budget 1", stats.ScannedTails)
			}
			removed += stats.DanglingRemoved
		case <-ctx.Done():
			t.Fatal("budgeted GC did not reclaim dangling edges before deadline")
		}
	}
	if got := cache.EdgeCount(); got != 0 {
		t.Fatalf("physical edges after bounded GC = %d, want 0", got)
	}
}
