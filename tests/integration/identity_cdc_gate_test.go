package integration_test

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/replication"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func receiptEdgeDeleteTailFixture(t *testing.T, origin hlc.NodeID, seq uint64, accepted []bool) (*pb.Mutation, hlc.Timestamp) {
	t.Helper()
	epoch := mutationreceipt.Epoch{0x73}
	group := mutationreceipt.GroupID{byte(seq)}
	issued := time.Now().Add(-time.Second)
	items := make([]*pb.ReplicatedReceiptEdgeDeleteItem, len(accepted))
	for i, isAccepted := range accepted {
		head := "edge/" + strconv.Itoa(i)
		id, err := mutationreceipt.NewID(epoch, issued, [24]byte{byte(seq), byte(i + 1)})
		if err != nil {
			t.Fatal(err)
		}
		canonical := []byte{byte(mutationreceipt.DeleteEdge)}
		var length [8]byte
		binary.BigEndian.PutUint64(length[:], uint64(len("edge/tail")))
		canonical = append(canonical, length[:]...)
		canonical = append(canonical, "edge/tail"...)
		binary.BigEndian.PutUint64(length[:], uint64(len(head)))
		canonical = append(canonical, length[:]...)
		canonical = append(canonical, head...)
		digest := mutationreceipt.IntentDigest(canonical)
		items[i] = &pb.ReplicatedReceiptEdgeDeleteItem{
			Key: &pb.EdgeKey{Tail: "edge/tail", Head: head},
			Receipt: &pb.MutationReceipt{
				OperationId: id.Bytes(), LogicalCallId: group[:], ItemIndex: uint32(i),
				ItemCount: uint32(len(accepted)), IntentSha256: digest[:],
				DeadlineUnixMs: uint64(issued.Add(time.Hour).UnixMilli()),
				OriginalResult: &pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: isAccepted}},
			},
			CausallyAccepted: isAccepted,
		}
	}
	stamp := hlc.Timestamp{WallNs: time.Now().UnixNano(), NodeID: origin}
	return &pb.Mutation{
		Seq: seq, Origin: origin[:], Hlc: &pb.HLCTimestamp{WallNs: stamp.WallNs, NodeId: origin[:]},
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
			ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{
				DeploymentEpoch: epoch[:], PolicyFingerprint: append([]byte{0x51}, make([]byte, 31)...),
				TombstoneExpiration: timestamppb.New(time.Now().Add(time.Hour)), Items: items,
			},
		}},
	}, stamp
}

// Synthetic log entries exercise the production-disabled wire projection on
// real Connect/h2c. No public receipt write or remote apply path is enabled.
func TestIdentityCDC_ReceiptEdgeDeleteTailFailsClosedAndPreservesCursor(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x15}
	node := newPumpNode(t, origin)
	rep := newReplicationRawClient(t, node.url)
	for seq, accepted := range [][]bool{{true, false}, {false}} {
		mutation, stamp := receiptEdgeDeleteTailFixture(t, origin, uint64(seq+1), accepted)
		if _, err := node.log.Append(mutation, stamp); err != nil {
			t.Fatal(err)
		}
	}

	legacy, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err == nil {
		if legacy.Receive() {
			t.Fatalf("legacy full Subscribe received downgrade frame: %+v", legacy.Msg())
		}
		err = legacy.Err()
		_ = legacy.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("legacy full Subscribe error = %v, want InvalidArgument", err)
	}
	full, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1, AcceptReceiptEnvelopes: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = full.Close() }()
	for seq, count := range []int{2, 1} {
		if !full.Receive() {
			t.Fatalf("receipt full frame %d: %v", seq+1, full.Err())
		}
		mutation := full.Msg().GetMutation()
		call := mutation.GetOp().GetReplicatedReceiptEdgeDelete()
		if mutation.GetSeq() != uint64(seq+1) || call == nil || len(call.GetItems()) != count || mutation.GetOp().GetDeleteEdges() != nil {
			t.Fatalf("receipt full frame %d lost envelope: %+v", seq+1, full.Msg())
		}
	}
	identity, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection:       pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 1},
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = identity.Close() }()
	if !identity.Receive() {
		t.Fatalf("accepted identity: %v", identity.Err())
	}
	accepted := identity.Msg().GetIdentityChunk()
	if accepted == nil || accepted.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE ||
		accepted.GetSeq() != 1 || len(accepted.GetEdgeKeys()) != 1 || accepted.GetEdgeKeys()[0].GetHead() != "edge/0" {
		t.Fatalf("accepted identity = %+v", identity.Msg())
	}
	if !identity.Receive() {
		t.Fatalf("receipt-only identity: %v", identity.Err())
	}
	marker := identity.Msg().GetIdentityChunk()
	if marker == nil || marker.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY ||
		marker.GetSeq() != 2 || marker.GetChunkIndex() != 0 || marker.GetFirstItemIndex() != 0 ||
		!marker.GetIsLast() || len(marker.GetVertexKeys())+len(marker.GetEdgeKeys()) != 0 {
		t.Fatalf("receipt-only marker = %+v", identity.Msg())
	}

	bad, stamp := receiptEdgeDeleteTailFixture(t, origin, 3, []bool{false})
	bad.GetOp().GetReplicatedReceiptEdgeDelete().Items[0].Receipt.IntentSha256[0] ^= 1
	if _, err := node.log.Append(bad, stamp); err != nil {
		t.Fatal(err)
	}
	malformed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 3, AcceptReceiptEnvelopes: true}))
	if err == nil {
		if malformed.Receive() {
			t.Fatalf("malformed receipt full frame escaped: %+v", malformed.Msg())
		}
		err = malformed.Err()
		_ = malformed.Close()
	}
	if connect.CodeOf(err) != connect.CodeInternal {
		t.Fatalf("malformed receipt full error = %v, want Internal", err)
	}
}

func TestIdentityCDC_BootstrapLiveExactVictimsAndFullCompatibility(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x01}
	node := newPumpNode(t, origin)
	rep := newReplicationRawClient(t, node.url)
	if _, err := node.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "cap/one", Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}},
		{Key: "cap/two", Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}},
	}})); err != nil {
		t.Fatal(err)
	}
	full, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{FromLocalSeq: 1}))
	if err != nil {
		t.Fatal(err)
	}
	if !full.Receive() || full.Msg().GetMutation() == nil || full.Msg().GetIdentityChunk() != nil {
		t.Fatalf("default Subscribe no longer returns full Mutation: (%v,%v)", full.Msg(), full.Err())
	}
	_ = full.Close()

	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() {
		t.Fatalf("bootstrap checkpoint: %v", feed.Err())
	}
	checkpoint := feed.Msg().GetCheckpoint()
	if checkpoint == nil || checkpoint.GetLastSeqPerOrigin()[hex.EncodeToString(origin[:])] != 1 || feed.Msg().GetMutation() != nil {
		t.Fatalf("bad identity checkpoint: %+v", feed.Msg())
	}
	if _, err := node.raw.DeleteVerticesByPrefix(ctx, connect.NewRequest(&pb.DeleteVerticesByPrefixRequest{Prefix: "cap/", Limit: 1})); err != nil {
		t.Fatal(err)
	}
	if _, err := node.raw.AddEdge(ctx, connect.NewRequest(&pb.AddEdgeRequest{Edge: &pb.Edge{Tail: "edge/a", Head: "edge/b", Weight: 72.5}, ContribId: []byte("PRIVATE-CONTRIBUTION-ID!")})); err != nil {
		t.Fatal(err)
	}
	if _, err := node.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "edge/a", Head: "edge/c", Weight: 91.25}})); err != nil {
		t.Fatal(err)
	}
	if _, err := node.raw.DeleteEdge(ctx, connect.NewRequest(&pb.DeleteEdgeRequest{Tail: "edge/a", Head: "edge/b"})); err != nil {
		t.Fatal(err)
	}
	for _, expected := range []struct {
		seq  uint64
		op   pb.IdentityOperation
		tail string
		head string
	}{
		{seq: 2, op: pb.IdentityOperation_IDENTITY_OPERATION_DELETE_VERTEX},
		{seq: 3, op: pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE, tail: "edge/a", head: "edge/b"},
		{seq: 4, op: pb.IdentityOperation_IDENTITY_OPERATION_PUT_EDGE, tail: "edge/a", head: "edge/c"},
		{seq: 5, op: pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE, tail: "edge/a", head: "edge/b"},
	} {
		if !feed.Receive() {
			t.Fatalf("identity event seq %d: %v", expected.seq, feed.Err())
		}
		chunk := feed.Msg().GetIdentityChunk()
		if chunk == nil || chunk.GetSeq() != expected.seq || chunk.GetOperation() != expected.op || !chunk.GetIsLast() || chunk.GetChunkIndex() != 0 || len(chunk.GetOrigin()) != 16 || feed.Msg().GetMutation() != nil {
			t.Fatalf("bad identity event seq %d: %+v", expected.seq, feed.Msg())
		}
		if expected.seq == 2 {
			if len(chunk.GetVertexKeys()) != 1 || !strings.HasPrefix(chunk.GetVertexKeys()[0], "cap/") {
				t.Fatalf("prefix did not project exact victim: %+v", chunk)
			}
			survivor := "cap/one"
			if chunk.GetVertexKeys()[0] == survivor {
				survivor = "cap/two"
			}
			if _, err := node.raw.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: survivor})); err != nil {
				t.Fatalf("capped prefix removed survivor: %v", err)
			}
		} else if len(chunk.GetEdgeKeys()) != 1 || chunk.GetEdgeKeys()[0].GetTail() != expected.tail || chunk.GetEdgeKeys()[0].GetHead() != expected.head {
			t.Fatalf("edge operation projected wrong pair: %+v", chunk)
		}
		encoded, err := protojson.Marshal(feed.Msg())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(encoded), "PRIVATE-VALUE") || strings.Contains(string(encoded), "PRIVATE-CONTRIBUTION-ID!") || strings.Contains(string(encoded), "72.5") || strings.Contains(string(encoded), "91.25") {
			t.Fatalf("identity frame leaked payload: %s", encoded)
		}
	}
}

func TestIdentityCDC_ChunkedPluralAndVectorGap(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	origin := hlc.NodeID{0xD1, 0x02}
	node := newPumpNodeWithSearch(t, origin, 3, true)
	rep := newReplicationRawClient(t, node.url)
	vertices := make([]*pb.Vertex, 1025)
	for i := range vertices {
		vertices[i] = &pb.Vertex{Key: "bulk/" + strconv.Itoa(i), Value: &pb.Vertex_String_{String_: "PRIVATE-VALUE"}}
	}
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing bootstrap checkpoint: %v", feed.Err())
	}
	if _, err := node.raw.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
		t.Fatal(err)
	}
	for i, count := range []int{1024, 1} {
		if !feed.Receive() {
			t.Fatalf("plural chunk %d: %v", i, feed.Err())
		}
		chunk := feed.Msg().GetIdentityChunk()
		if chunk == nil || chunk.GetSeq() != 1 || len(chunk.GetVertexKeys()) != count || chunk.GetChunkIndex() != uint32(i) || chunk.GetIsLast() != (i == 1) || proto.Size(feed.Msg()) > 1<<20 {
			t.Fatalf("plural chunk %d invalid: %+v", i, feed.Msg())
		}
	}
	for i := 2; i <= 4; i++ {
		if _, err := node.raw.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "gap/key"})); err != nil {
			t.Fatal(err)
		}
	}
	resume, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 3}}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resume.Close() }()
	for _, seq := range []uint64{3, 4} {
		if !resume.Receive() || resume.Msg().GetIdentityChunk().GetSeq() != seq {
			t.Fatalf("resume seq %d: (%v,%v)", seq, resume.Msg(), resume.Err())
		}
	}
	gap, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, FromSeqPerOrigin: map[string]uint64{hex.EncodeToString(origin[:]): 1}}))
	if err == nil {
		if gap.Receive() {
			t.Fatalf("evicted seq was silently skipped: %+v", gap.Msg())
		}
		err = gap.Err()
		_ = gap.Close()
	}
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || !strings.Contains(err.Error(), "gapped") {
		t.Fatalf("evicted vector error = %v", err)
	}
	bad, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, FromSeqPerOrigin: map[string]uint64{"BAD": 1}}))
	if err == nil {
		if bad.Receive() {
			t.Fatalf("invalid cursor produced frame: %+v", bad.Msg())
		}
		err = bad.Err()
		_ = bad.Close()
	}
	if connect.CodeOf(err) != connect.CodeInvalidArgument {
		t.Fatalf("invalid cursor error = %v", err)
	}
}

func TestIdentityCDC_CappedEdgePrefixProjectsOnlyCommittedVictim(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	node := newPumpNode(t, hlc.NodeID{0xD1, 0x06})
	rep := newReplicationRawClient(t, node.url)
	for _, head := range []string{"cap/one", "cap/two"} {
		if _, err := node.raw.PutEdge(ctx, connect.NewRequest(&pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "cap/tail", Head: head, Weight: 9}})); err != nil {
			t.Fatal(err)
		}
	}
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing checkpoint: %v", feed.Err())
	}
	deleted, err := node.raw.DeleteEdgesByPrefix(ctx, connect.NewRequest(&pb.DeleteEdgesByPrefixRequest{TailPrefix: "cap/", HeadPrefix: "cap/", Limit: 1}))
	if err != nil || deleted.Msg.GetDeleted() != 1 {
		t.Fatalf("capped edge delete = (%v,%v)", deleted, err)
	}
	if !feed.Receive() {
		t.Fatalf("missing capped edge identity: %v", feed.Err())
	}
	chunk := feed.Msg().GetIdentityChunk()
	if chunk == nil || chunk.GetSeq() != 3 || chunk.GetOperation() != pb.IdentityOperation_IDENTITY_OPERATION_DELETE_EDGE || len(chunk.GetEdgeKeys()) != 1 || !chunk.GetIsLast() {
		t.Fatalf("prefix projected wrong victim set: %+v", feed.Msg())
	}
	victim := chunk.GetEdgeKeys()[0]
	if victim.GetTail() != "cap/tail" || (victim.GetHead() != "cap/one" && victim.GetHead() != "cap/two") {
		t.Fatalf("wrong edge victim: %+v", victim)
	}
	survivor := "cap/one"
	if victim.GetHead() == survivor {
		survivor = "cap/two"
	}
	if _, err := node.raw.GetEdge(ctx, connect.NewRequest(&pb.GetEdgeRequest{Tail: "cap/tail", Head: survivor})); err != nil {
		t.Fatalf("capped edge prefix deleted survivor: %v", err)
	}
}

type identityStreamMetrics struct {
	ended chan struct{}
}

func (*identityStreamMetrics) OnSubscribeStarted()       {}
func (m *identityStreamMetrics) OnSubscribeEnded()       { close(m.ended) }
func (*identityStreamMetrics) OnSubscribeDropped(string) {}

func TestIdentityCDC_CancelReleasesSubscriber(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	metrics := &identityStreamMetrics{ended: make(chan struct{})}
	node := newPumpNodeWithWALAndMetrics(t, hlc.NodeID{0xD1, 0x05}, 16, true, nil, metrics)
	rep := newReplicationRawClient(t, node.url)
	streamCtx, stop := context.WithCancel(ctx)
	feed, err := rep.Subscribe(streamCtx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
	if err != nil {
		stop()
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		stop()
		t.Fatalf("missing checkpoint before cancel: (%v,%v)", feed.Msg(), feed.Err())
	}
	stop()
	if feed.Receive() {
		t.Fatalf("identity feed continued after cancellation: %+v", feed.Msg())
	}
	select {
	case <-metrics.ended:
	case <-ctx.Done():
		t.Fatal("canceled identity feed retained its subscriber")
	}
}

func TestIdentityCDC_SnapshotInstallGapsExistingStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	source := newPumpNodeWithSearch(t, hlc.NodeID{0xD1, 0x07}, 2, true)
	follower := newPumpNode(t, hlc.NodeID{0xD1, 0x08})
	for i := 0; i < 6; i++ {
		if _, err := source.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "snapshot/" + strconv.Itoa(i)}})); err != nil {
			t.Fatal(err)
		}
	}
	rep := newReplicationRawClient(t, follower.url)
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing pre-Snapshot checkpoint: %v", feed.Err())
	}
	metrics := &tombstoneSnapshotMetrics{failed: make(chan struct{}, 1), snapshots: make(chan struct{}, 1)}
	follower.startPumpWithMetrics(ctx, t, []string{source.url}, metrics)
	select {
	case <-metrics.snapshots:
	case <-metrics.failed:
		t.Fatal("peer Snapshot recovery failed")
	case <-ctx.Done():
		t.Fatal("peer Snapshot recovery did not complete")
	}
	if _, ok := follower.cache.GetVertex("snapshot/5"); !ok {
		t.Fatal("peer Snapshot did not change the graph")
	}
	if feed.Receive() || connect.CodeOf(feed.Err()) != connect.CodeFailedPrecondition {
		t.Fatalf("identity stream survived an unlogged Snapshot graph change: (%v,%v)", feed.Msg(), feed.Err())
	}
	fresh, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = fresh.Close() }()
	if !fresh.Receive() || fresh.Msg().GetCheckpoint().GetLastSeqPerOrigin()[hex.EncodeToString(source.nodeID[:])] != 6 {
		t.Fatalf("fresh checkpoint missed verified Snapshot cutoff: (%v,%v)", fresh.Msg(), fresh.Err())
	}
}

func TestIdentityCDC_AntiEntropySnapshotAlsoGapsExistingStream(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	source := newPumpNodeWithSearch(t, hlc.NodeID{0xD1, 0x09}, 2, true)
	follower := newPumpNode(t, hlc.NodeID{0xD1, 0x0A})
	for i := 0; i < 6; i++ {
		if _, err := source.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "anti-snapshot/" + strconv.Itoa(i)}})); err != nil {
			t.Fatal(err)
		}
	}
	rep := newReplicationRawClient(t, follower.url)
	feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{
		Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY,
		Bootstrap:  true,
	}))
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = feed.Close() }()
	if !feed.Receive() || feed.Msg().GetCheckpoint() == nil {
		t.Fatalf("missing pre-Snapshot checkpoint: %v", feed.Err())
	}
	anti := replication.NewAntiEntropy(replication.AntiEntropyConfig{
		NodeID: follower.nodeID, Peers: []string{source.url}, Interval: 20 * time.Millisecond,
		SubscribeTimeout: time.Second, HTTPClient: h2cClient(),
	}, follower.svc, follower.svc, follower.cache)
	antiCtx, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- anti.Run(antiCtx) }()
	t.Cleanup(func() {
		stop()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Log("anti-entropy did not stop within 2s")
		}
	})
	for follower.svc.LocalSeq(source.nodeID) < 6 {
		select {
		case <-ctx.Done():
			t.Fatal("anti-entropy Snapshot did not advance the source cutoff")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if _, ok := follower.cache.GetVertex("anti-snapshot/5"); !ok {
		t.Fatal("anti-entropy Snapshot did not change the graph")
	}
	if feed.Receive() || connect.CodeOf(feed.Err()) != connect.CodeFailedPrecondition {
		t.Fatalf("identity stream survived anti-entropy Snapshot: (%v,%v)", feed.Msg(), feed.Err())
	}
}

type checkpointHoldWAL struct {
	calls   atomic.Int32
	entered chan struct{}
	release chan struct{}
}

func (w *checkpointHoldWAL) Write(mutationlog.Entry) error {
	if w.calls.Add(1) == 1 {
		close(w.entered)
		<-w.release
	}
	return nil
}

func TestIdentityCDC_CheckpointAndTailSharePublicationCut(t *testing.T) {
	for _, mode := range []string{"local", "relay"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
			defer cancel()
			wal := &checkpointHoldWAL{entered: make(chan struct{}), release: make(chan struct{})}
			node := newPumpNodeWithWAL(t, hlc.NodeID{0xD1, 0x03}, 16, true, wal)
			rep := newReplicationRawClient(t, node.url)
			writeDone := make(chan error, 1)
			origin := node.nodeID
			if mode == "local" {
				go func() {
					_, err := node.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "cut/first"}}))
					writeDone <- err
				}()
			} else {
				origin = hlc.NodeID{0xD1, 0x04}
				go func() {
					writeDone <- node.svc.ApplyMutation(ctx, &pb.Mutation{Seq: 1, Origin: origin[:], Hlc: &pb.HLCTimestamp{NodeId: origin[:], WallNs: time.Now().UnixNano()}, Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertices{PutVertices: &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "cut/first"}}}}}})
				}()
			}
			select {
			case <-wal.entered:
			case <-ctx.Done():
				t.Fatal("write never entered WAL")
			}
			type opened struct {
				feed *connect.ServerStreamForClient[pb.SubscribeResponse]
				err  error
			}
			first := make(chan opened, 1)
			go func() {
				feed, err := rep.Subscribe(ctx, connect.NewRequest(&pb.SubscribeRequest{Projection: pb.SubscribeProjection_SUBSCRIBE_PROJECTION_IDENTITY_ONLY, Bootstrap: true}))
				if err == nil {
					if !feed.Receive() {
						err = feed.Err()
					}
				}
				first <- opened{feed: feed, err: err}
			}()
			select {
			case got := <-first:
				t.Fatalf("checkpoint crossed graph-before-WAL cut: %+v", got)
			case <-time.After(150 * time.Millisecond):
			}
			close(wal.release)
			if err := <-writeDone; err != nil {
				t.Fatalf("committing first write: %v", err)
			}
			got := <-first
			if got.err != nil {
				t.Fatal(got.err)
			}
			defer func() { _ = got.feed.Close() }()
			if got.feed.Msg().GetCheckpoint().GetLastSeqPerOrigin()[hex.EncodeToString(origin[:])] != 1 {
				t.Fatalf("checkpoint skipped committed first write: %+v", got.feed.Msg())
			}
			if _, err := node.raw.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "cut/second"}})); err != nil {
				t.Fatal(err)
			}
			if !got.feed.Receive() || got.feed.Msg().GetIdentityChunk().GetSeq() == 0 {
				t.Fatalf("live tail missed post-checkpoint write: (%v,%v)", got.feed.Msg(), got.feed.Err())
			}
		})
	}
}
