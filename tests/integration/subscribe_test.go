package integration_test

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
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
	"github.com/anaregdesign/lantern/server/service"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type subscribeCutMetrics struct{ started chan struct{} }

func (m *subscribeCutMetrics) OnSubscribeStarted()       { close(m.started) }
func (m *subscribeCutMetrics) OnSubscribeEnded()         {}
func (m *subscribeCutMetrics) OnSubscribeDropped(string) {}

// The local append callback runs after the log dispatch and origin advance,
// but before the enclosing graph publication cut ends. Neither the full CDC
// stream nor PeerStatus may expose that staged frontier over h2c.
func TestSubscribeAndPeerStatus_RealWireWaitForPublicationCut(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0xA5}, hlc.Options{})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	appendEntered := make(chan struct{})
	appendRelease := make(chan struct{})
	t.Cleanup(func() {
		select {
		case <-appendRelease:
		default:
			close(appendRelease)
		}
	})
	svc := service.NewLanternService(cache).WithReplication(log, clock, func() {
		close(appendEntered)
		<-appendRelease
	})
	metrics := &subscribeCutMetrics{started: make(chan struct{})}
	rep := service.NewLanternReplicationService(log, cache, clock).WithOriginStates(svc).WithMetrics(metrics)
	srv := newConnectTestServer(t, svc, rep)
	subCli := newReplicationRawClient(t, srv.url)
	streamDone := make(chan struct {
		mutation *pb.Mutation
		err      error
	}, 1)
	go func() {
		stream, err := subCli.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
		if err == nil {
			defer stream.Close()
			if stream.Receive() {
				streamDone <- struct {
					mutation *pb.Mutation
					err      error
				}{stream.Msg().GetMutation(), nil}
				return
			}
			err = stream.Err()
		}
		streamDone <- struct {
			mutation *pb.Mutation
			err      error
		}{nil, err}
	}()
	select {
	case <-metrics.started:
	case <-ctx.Done():
		t.Fatal("Subscribe did not register")
	}

	raw := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	writeDone := make(chan error, 1)
	go func() {
		_, err := raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: "staged", Value: &pb.Vertex_String_{String_: "committed"}, Expiration: timestamppb.New(time.Now().Add(time.Minute)),
		}}))
		writeDone <- err
	}()
	select {
	case <-appendEntered:
	case <-ctx.Done():
		t.Fatal("PutVertex did not reach post-append stage")
	}
	statusDone := make(chan struct {
		response *pb.PeerStatusResponse
		err      error
	}, 1)
	go func() {
		response, err := subCli.PeerStatus(ctx, connect.NewRequest(&pb.PeerStatusRequest{}))
		if err != nil {
			statusDone <- struct {
				response *pb.PeerStatusResponse
				err      error
			}{nil, err}
			return
		}
		statusDone <- struct {
			response *pb.PeerStatusResponse
			err      error
		}{response.Msg, nil}
	}()
	select {
	case result := <-streamDone:
		t.Fatalf("Subscribe crossed held publication: mutation=%v err=%v", result.mutation, result.err)
	case result := <-statusDone:
		t.Fatalf("PeerStatus crossed held publication: response=%v err=%v", result.response, result.err)
	case <-time.After(200 * time.Millisecond):
	}
	close(appendRelease)
	select {
	case err := <-writeDone:
		if err != nil {
			t.Fatalf("PutVertex: %v", err)
		}
	case <-ctx.Done():
		t.Fatal("PutVertex did not finish")
	}
	select {
	case result := <-streamDone:
		if result.err != nil || result.mutation.GetSeq() != 1 {
			t.Fatalf("Subscribe after publication: mutation=%v err=%v", result.mutation, result.err)
		}
	case <-ctx.Done():
		t.Fatal("Subscribe did not deliver after publication")
	}
	select {
	case result := <-statusDone:
		if result.err != nil || len(result.response.GetOrigins()) != 1 || result.response.GetOrigins()[0].GetLastSeq() != 1 {
			t.Fatalf("PeerStatus after publication: response=%v err=%v", result.response, result.err)
		}
	case <-ctx.Done():
		t.Fatal("PeerStatus did not finish after publication")
	}
}

// TestSubscribe_E2E_100Writes wires a real LanternService +
// LanternReplicationService through the Connect-on-h2c httptest
// harness, drives 100 PutVertex writes through the SDK, and asserts
// the subscriber sees all 100 mutations in strict Seq order with
// PutVertices payloads intact. Acceptance criterion for issue #180.
func TestSubscribe_E2E_100Writes(t *testing.T) {
	const N = 100

	vi := provider.NewValidationInterceptor(provider.ValidationLimits{
		MaxKeyLen:         256,
		MaxBatchSize:      1024,
		IlluminateMaxStep: 32,
		IlluminateMaxK:    256,
	})

	log := mutationlog.New(mutationlog.Options{Capacity: 4 * N, SubscriberBuffer: 4 * N})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(hlc.NodeID{0xAA, 0xBB}, hlc.Options{})

	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)

	svc := service.NewLanternService(cache).
		WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock)
	srv := newConnectTestServer(t, svc, rep, vi.ConnectInterceptor())

	// Subscriber: raw Connect-Go replication client.
	subCli := newReplicationRawClient(t, srv.url)

	// Writer (SDK Connect transport).
	l := newConnectClientFor(t, srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// Issue writes first so Subscribe can replay them from the
	// in-memory ring. SubscriberBuffer is sized to fit all entries.
	for i := 0; i < N; i++ {
		if _, err := l.PutVertex(ctx, "k-"+itoa(i), "v", time.Minute); err != nil {
			t.Fatalf("PutVertex[%d]: %v", i, err)
		}
	}

	stream, err := subCli.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	var prev uint64
	for i := 0; i < N; i++ {
		if !stream.Receive() {
			if streamErr := stream.Err(); streamErr != nil && !errors.Is(streamErr, io.EOF) {
				t.Fatalf("Recv[%d]: %v", i, streamErr)
			}
			t.Fatalf("stream ended at %d/%d", i, N)
		}
		resp := stream.Msg()
		got := resp.GetMutation()
		if got.GetSeq() != prev+1 {
			t.Fatalf("entry[%d] seq=%d want %d", i, got.GetSeq(), prev+1)
		}
		prev = got.GetSeq()
		pv := got.GetOp().GetReplicatedPutVertices()
		if pv == nil || len(pv.GetEntries()) != 1 || pv.GetEntries()[0].GetLive() == nil {
			t.Fatalf("entry[%d] missing ReplicatedPutVertices live payload", i)
		}
		if want := "k-" + itoa(i); pv.GetEntries()[0].GetLive().GetKey() != want {
			t.Errorf("entry[%d] key=%q want %q", i, pv.GetEntries()[0].GetLive().GetKey(), want)
		}
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b [20]byte
	pos := len(b)
	for i > 0 {
		pos--
		b[pos] = byte('0' + i%10)
		i /= 10
	}
	return string(b[pos:])
}

// TestSubscribe_PerOriginCursor_Skips covers the leaderless Subscribe
// contract's resume semantics (#415, B-2). The wire-level cursor is
// `map<string, uint64> from_seq_per_origin` keyed by hex-encoded HLC
// NodeID; an entry whose origin appears in the cursor is delivered
// only when its seq is >= the cursor value, and an entry whose origin
// is absent from the cursor is delivered from the oldest retained
// entry.
//
// The test fabricates entries from two origins on a single replica's
// log via the LanternReplicationService directly (cheaper and more
// targeted than spinning a multi-node cluster — the per-origin filter
// lives entirely in the Subscribe handler and is independent of the
// peer pump).
func TestSubscribe_PerOriginCursor_Skips(t *testing.T) {
	originA := hlc.NodeID{0x11}
	originB := hlc.NodeID{0x22}

	log := mutationlog.New(mutationlog.Options{Capacity: 64, SubscriberBuffer: 64})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(originA, hlc.Options{})

	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).
		WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock)
	srv := newConnectTestServer(t, svc, rep, nil)
	subCli := newReplicationRawClient(t, srv.url)

	// Append four entries with per-origin seqs: A=1, B=1, A=2, B=2.
	// Reading B (#415) anchors mu.Seq to the originating writer's
	// seq, not to the local log seq, so the cursor {hex(A): 2}
	// should skip A's entry at seq 1 while keeping every other one:
	// B's seq 1 (B absent from cursor → delivered from oldest),
	// A's seq 2 (exactly at cursor), B's seq 2 (still no cursor).
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mkMu := func(originID hlc.NodeID, originSeq uint64, key string) *pb.Mutation {
		ts := hlc.New(originID, hlc.Options{}).Now()
		return &pb.Mutation{
			Seq: originSeq,
			Hlc: &pb.HLCTimestamp{
				WallNs: ts.WallNs, Logical: ts.Logical,
				NodeId: append([]byte(nil), originID[:]...),
			},
			Origin: append([]byte(nil), originID[:]...),
			Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{
				PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: key}},
			}},
		}
	}
	for _, m := range []*pb.Mutation{
		mkMu(originA, 1, "a1"), mkMu(originB, 1, "b1"),
		mkMu(originA, 2, "a2"), mkMu(originB, 2, "b2"),
	} {
		// Append directly to the log so we control origin assignment
		// and per-origin seq; going through PutVertex would stamp
		// originA + auto-assigned seqs.
		if _, err := log.Append(m, hlc.Timestamp{NodeID: hlc.NodeID(m.GetHlc().GetNodeId()[0:16])}); err != nil {
			t.Fatalf("log.Append: %v", err)
		}
	}

	cursor := map[string]uint64{hex.EncodeToString(originA[:]): 2}
	stream, err := subCli.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromSeqPerOrigin: cursor}))
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	t.Cleanup(func() { _ = stream.Close() })

	type got struct {
		seq    uint64
		origin string
	}
	var seen []got
	for i := 0; i < 3; i++ {
		if !stream.Receive() {
			if streamErr := stream.Err(); streamErr != nil && !errors.Is(streamErr, io.EOF) {
				t.Fatalf("Recv[%d]: %v", i, streamErr)
			}
			t.Fatalf("stream ended after %d entries; want 3", len(seen))
		}
		m := stream.Msg().GetMutation()
		seen = append(seen, got{seq: m.GetSeq(), origin: hex.EncodeToString(m.GetOrigin())})
	}

	want := []got{
		{seq: 1, origin: hex.EncodeToString(originB[:])}, // B's first; not in cursor → oldest
		{seq: 2, origin: hex.EncodeToString(originA[:])}, // A's second; exactly at cursor
		{seq: 2, origin: hex.EncodeToString(originB[:])}, // B's second
	}
	for i, g := range seen {
		if g != want[i] {
			t.Errorf("entry[%d] got %+v want %+v", i, g, want[i])
		}
	}
}

// TestSubscribeSDK_TypedCursorAndGap exercises the public Go SDK facade over
// the real Connect/h2c handler. It pins both the typed per-origin cursor happy
// path and the retained-log gap failure contract introduced by #1182.
func TestSubscribeSDK_TypedCursorAndGap(t *testing.T) {
	origin := hlc.NodeID{0x31, 0x32, 0x33, 0x34}
	log := mutationlog.New(mutationlog.Options{Capacity: 8, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = log.Close() })
	clock := hlc.New(origin, hlc.Options{})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithReplication(log, clock, nil)
	rep := service.NewLanternReplicationService(log, cache, clock)
	srv := newConnectTestServer(t, svc, rep, nil)
	sdk := newConnectClientFor(t, srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for i := 1; i <= 4; i++ {
		if _, err := sdk.PutVertex(ctx, "sdk-cursor-"+itoa(i), "v", time.Minute); err != nil {
			t.Fatalf("PutVertex[%d]: %v", i, err)
		}
	}

	sdkOrigin, err := client.ChangeOriginFromBytes(origin[:])
	if err != nil {
		t.Fatalf("ChangeOriginFromBytes: %v", err)
	}
	var events []*client.ChangeEvent
	for event, streamErr := range sdk.Subscribe(ctx, client.ChangeCursor{sdkOrigin: 3}) {
		if streamErr != nil {
			t.Fatalf("Subscribe(cursor=3): %v", streamErr)
		}
		events = append(events, event)
		if len(events) == 2 {
			break
		}
	}
	if len(events) != 2 || events[0].GetSeq() != 3 || events[1].GetSeq() != 4 {
		t.Fatalf("events = %v, want seqs [3 4]", events)
	}
	gotOrigin, err := client.ChangeOriginFromBytes(events[0].GetOrigin())
	if err != nil {
		t.Fatalf("event origin: %v", err)
	}
	if gotOrigin != sdkOrigin {
		t.Fatalf("event origin = %s, want %s", gotOrigin, sdkOrigin)
	}

	gapLog := mutationlog.New(mutationlog.Options{Capacity: 2, SubscriberBuffer: 8})
	t.Cleanup(func() { _ = gapLog.Close() })
	gapClock := hlc.New(origin, hlc.Options{})
	gapCache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	gapService := service.NewLanternService(gapCache).WithReplication(gapLog, gapClock, nil)
	gapReplication := service.NewLanternReplicationService(gapLog, gapCache, gapClock)
	gapServer := newConnectTestServer(t, gapService, gapReplication, nil)
	gapSDK := newConnectClientFor(t, gapServer.url)
	for i := 1; i <= 4; i++ {
		if _, err := gapSDK.PutVertex(ctx, "sdk-gap-"+itoa(i), "v", time.Minute); err != nil {
			t.Fatalf("gap PutVertex[%d]: %v", i, err)
		}
	}

	var gapErr error
	for _, streamErr := range gapSDK.Subscribe(ctx, client.ChangeCursor{sdkOrigin: 1}) {
		gapErr = streamErr
		break
	}
	if !errors.Is(gapErr, client.ErrFailedPrecondition) {
		t.Fatalf("Subscribe(cursor=1) error = %v, want ErrFailedPrecondition", gapErr)
	}
}

// TestSubscribeIdentitySDK exercises the public payload-free facade over the
// production Connect/h2c service: checkpoint, bounded chunks, cursor resume,
// and a retained-log gap are all observed at the SDK boundary.
func TestSubscribeIdentitySDK(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xA1, 0x02}
	node := newPumpNode(t, origin)
	sdkOrigin, err := client.ChangeOriginFromBytes(origin[:])
	if err != nil {
		t.Fatal(err)
	}
	if _, err := node.sdk.PutVertex(ctx, "resident/a", "PRIVATE-VALUE", time.Hour); err != nil {
		t.Fatal(err)
	}
	var cursor client.ChangeCursor
	var checkpointSeen bool
	var chunkCount int
bootstrapLoop:
	for event, streamErr := range node.sdk.BootstrapIdentity(ctx) {
		if streamErr != nil {
			t.Fatalf("bootstrap identity: %v", streamErr)
		}
		switch change := event.(type) {
		case *client.IdentityCheckpoint:
			if checkpointSeen || change.LastSeqPerOrigin[sdkOrigin] != 1 {
				t.Fatalf("checkpoint = %+v", change)
			}
			checkpointSeen = true
			cursor, err = change.NextCursor()
			if err != nil || cursor[sdkOrigin] != 2 {
				t.Fatalf("checkpoint next cursor = (%v,%v)", cursor, err)
			}
			vertices := make([]*pb.Vertex, 1025)
			for i := range vertices {
				vertices[i] = &pb.Vertex{Key: "bulk/" + itoa(i), Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}}
			}
			if _, err := node.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
				t.Fatalf("plural write: %v", err)
			}
		case *client.IdentityChunk:
			if !checkpointSeen || change.Origin != sdkOrigin || change.Seq != 2 || change.Operation != client.IdentityPutVertex || change.ChunkIndex != uint32(chunkCount) || strings.Contains(fmt.Sprintf("%+v", change), "PRIVATE-VALUE") {
				t.Fatalf("identity chunk = %+v", change)
			}
			if chunkCount == 0 {
				if change.IsLast || len(change.VertexKeys) != 1024 || change.FirstItemIndex != 0 {
					t.Fatalf("first chunk = %+v", change)
				}
				if _, err := change.NextCursor(cursor); !errors.Is(err, client.ErrIncompleteIdentityMutation) {
					t.Fatalf("partial chunk advanced cursor: %v", err)
				}
			} else if !change.IsLast || len(change.VertexKeys) != 1 || change.FirstItemIndex != 1024 {
				t.Fatalf("final chunk = %+v", change)
			}
			chunkCount++
			if change.IsLast {
				cursor, err = change.NextCursor(cursor)
				if err != nil || cursor[sdkOrigin] != 3 {
					t.Fatalf("final cursor = (%v,%v)", cursor, err)
				}
				break bootstrapLoop
			}
		default:
			t.Fatalf("unexpected identity event %T", event)
		}
	}
	if !checkpointSeen || chunkCount != 2 {
		t.Fatalf("bootstrap stream saw checkpoint=%t chunks=%d", checkpointSeen, chunkCount)
	}
	if _, err := node.sdk.DeleteVertex(ctx, "resident/a"); err != nil {
		t.Fatal(err)
	}
	var resumed *client.IdentityChunk
	for event, streamErr := range node.sdk.SubscribeIdentity(ctx, cursor) {
		if streamErr != nil {
			t.Fatalf("resume identity: %v", streamErr)
		}
		var ok bool
		resumed, ok = event.(*client.IdentityChunk)
		if !ok {
			t.Fatalf("resume event %T, want chunk", event)
		}
		break
	}
	if resumed == nil || resumed.Seq != 3 || resumed.Operation != client.IdentityDeleteVertex || len(resumed.VertexKeys) != 1 || resumed.VertexKeys[0] != "resident/a" {
		t.Fatalf("resumed identity = %+v", resumed)
	}
	if _, err := resumed.NextCursor(cursor); err != nil {
		t.Fatalf("resumed cursor: %v", err)
	}
	streamCtx, stop := context.WithCancel(ctx)
	defer stop()
	var cancelErr error
	for event, streamErr := range node.sdk.BootstrapIdentity(streamCtx) {
		if streamErr != nil {
			cancelErr = streamErr
			break
		}
		if _, ok := event.(*client.IdentityCheckpoint); !ok {
			t.Fatalf("cancellation stream first event = %T", event)
		}
		stop()
	}
	if !errors.Is(cancelErr, context.Canceled) {
		t.Fatalf("canceled identity stream = %v", cancelErr)
	}

	gapNode := newPumpNodeWithSearch(t, hlc.NodeID{0xA1, 0x03}, 2, true)
	gapOrigin, err := client.ChangeOriginFromBytes(gapNode.nodeID[:])
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 4; i++ {
		if _, err := gapNode.sdk.PutVertex(ctx, "gap/"+itoa(i), "v", time.Hour); err != nil {
			t.Fatal(err)
		}
	}
	var gapErr error
	for _, streamErr := range gapNode.sdk.SubscribeIdentity(ctx, client.ChangeCursor{gapOrigin: 1}) {
		gapErr = streamErr
		break
	}
	if !errors.Is(gapErr, client.ErrIdentityGap) || !errors.Is(gapErr, client.ErrFailedPrecondition) || connect.CodeOf(gapErr) != connect.CodeFailedPrecondition {
		t.Fatalf("gap error = %v", gapErr)
	}
}
