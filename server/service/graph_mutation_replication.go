package service

import (
	"bytes"
	"context"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func (s *LanternService) validateReplicatedGraphMutation(e *graphMutationEnvelope) error {
	if s.receiptVertexPutCoordinator == nil || s.receiptEdgeAddCoordinator == nil || s.receiptStore == nil {
		return connect.NewError(connect.CodeUnimplemented, errGraphMutation)
	}
	if _, err := decodeGraphMutation(e.Mutation); err != nil {
		return invalidReceiptRequest(err)
	}
	metadata, _ := receiptWALEnvelopeInfo(e)
	// The existing validator checks the common Store epoch, policy, clock skew,
	// and complete committed receipt group; it does not plan Vertex effects.
	return s.receiptVertexPutCoordinator.validateReplicatedEnvelope(&vertexPutReceiptEnvelope{Epoch: metadata.epoch, PolicyFingerprint: metadata.policy, HLC: hlcFromProto(e.Mutation.GetHlc()), Receipts: e.Receipts})
}

func sameGraphMutationIntent(a, b *graphMutationEnvelope) bool {
	if a == nil || b == nil {
		return false
	}
	left, right := proto.Clone(a.Mutation).(*pb.Mutation), proto.Clone(b.Mutation).(*pb.Mutation)
	left.GetOp().GetGraphMutation().Projection = nil
	right.GetOp().GetGraphMutation().Projection = nil
	return proto.Equal(left, right)
}

func (s *LanternService) commitReplicatedGraphMutation(ctx context.Context, origin hlc.NodeID, seq uint64, ts hlc.Timestamp, pending *pendingMutation) error {
	e, ok := pending.receipt.(*graphMutationEnvelope)
	if !ok || e == nil || !bytes.Equal(e.Mutation.GetOrigin(), origin[:]) || e.Mutation.GetSeq() != seq || !hlcFromProto(e.Mutation.GetHlc()).Equal(ts) {
		return connect.NewError(connect.CodeInternal, errGraphMutation)
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	s.receiptOriginCutMu.Lock()
	defer s.receiptOriginCutMu.Unlock()
	publication := stagedPublication{service: s}
	defer publication.failClosedOnPanic()
	if s.publicationFaultCount != 0 || s.receiptCommitFaulted {
		return publicationGapError()
	}
	if err := s.validateReplicationRelayFrame(e); err != nil {
		return err
	}
	storeTx, err := s.receiptStore.Begin(time.Now())
	if err != nil {
		return receiptStoreError(err)
	}
	defer storeTx.Abort()
	if err := storeTx.PrepareCommitted(e.Receipts, ts.WallNs/int64(time.Millisecond)); err != nil {
		return receiptStoreError(err)
	}
	floor, err := receiptClockFloor(storeTx)
	if err != nil {
		return receiptStoreError(err)
	}
	if err := s.clock.RestoreFloor(floor); err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	// A healthy retry has no durable local decision. Other origins may have
	// advanced since a rejected preflight or definite WAL abort, so reconsider
	// the immutable origin effects rather than the last provisional projection.
	vertices, vi, edges, ei := graphMutationEffects(e, false)
	graphTx, err := s.receiptVertexPutCoordinator.cache.BeginReplicatedGraphMutation(ctx, vertices, edges, ts, uint64(4*proto.Size(e.Mutation)))
	if err != nil {
		return graphMutationError(err)
	}
	defer graphTx.Abort()
	projection, err := graphMutationProjection(graphTx.Result(), vi, ei)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	m := proto.Clone(e.Mutation).(*pb.Mutation)
	m.GetOp().GetGraphMutation().Projection = projection
	local, err := decodeGraphMutation(m)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if prior, ok := pending.receiptWAL.(*graphMutationEnvelope); ok && proto.Equal(prior.Mutation, local.Mutation) {
		local = prior
	}
	if err := s.validateReplicationFrame(local); err != nil {
		return err
	}
	originTx, ok := s.origins.stageNext(origin, seq, ts)
	if !ok {
		return connect.NewError(connect.CodeInternal, errGraphMutation)
	}
	defer originTx.Abort()
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	if err := storeTx.Stage(); err != nil {
		return receiptStoreError(err)
	}
	pending.receiptWAL = local
	if err := graphTx.CheckBeforeWAL(); err != nil {
		return graphMutationError(err)
	}
	if err := publication.commit(local, ts, storeTx, graphTx.Commit, originTx); err != nil {
		return graphMutationPublicationError(err)
	}
	if err := s.clock.RestoreFloor(ts); err != nil {
		s.markReceiptCommitFaultLocked()
		return connect.NewError(connect.CodeInternal, err)
	}
	return nil
}

func maximalGraphMutationEnvelope(e *graphMutationEnvelope) (*graphMutationEnvelope, error) {
	if _, err := decodeGraphMutation(e.Mutation); err != nil {
		return nil, err
	}
	m := proto.Clone(e.Mutation).(*pb.Mutation)
	vertices, vi, edges, ei := graphMutationEffects(e, false)
	result := graphcache.GraphMutationResult[string, *pb.Vertex]{}
	for i, v := range vertices {
		outcome := graphcache.PutOutcomeAppliedAndLive
		if v.CausalBarrier {
			outcome = graphcache.PutOutcomeExpired
		}
		result.Vertices.Accepted = append(result.Vertices.Accepted, graphcache.IndexedVertexPut[string, *pb.Vertex]{Index: i, Item: v, Outcome: outcome})
	}
	for i, e := range edges {
		result.Edges.Accepted = append(result.Edges.Accepted, graphcache.IndexedEdgeAdd[string]{Index: i, Item: e})
	}
	var err error
	m.GetOp().GetGraphMutation().Projection, err = graphMutationProjection(result, vi, ei)
	if err != nil {
		return nil, err
	}
	return decodeGraphMutation(m)
}

// Committed WAL replay may expire a previously live Vertex, but may neither
// re-admit effects nor change immutable origin decisions.
func graphMutationReplayProjectionMatches(recorded, replayed []*pb.GraphMutationProjection) bool {
	if len(recorded) != len(replayed) {
		return false
	}
	for i, left := range recorded {
		right := replayed[i]
		if proto.Equal(left, right) {
			continue
		}
		if left.GetItemIndex() != right.GetItemIndex() {
			return false
		}
		live, barrier := left.GetPutVertex().GetLive(), right.GetPutVertex().GetCausalBarrier()
		if live == nil || live.GetExpiration() == nil || live.GetExpiration().CheckValid() != nil || barrier == nil || barrier.GetKey() != live.GetKey() {
			return false
		}
	}
	return true
}

func replayGraphMutation(graph *graphcache.GraphCache[string, *pb.Vertex], e *graphMutationEnvelope) error {
	vertices, vi, edges, ei := graphMutationEffects(e, true)
	tx, err := graph.BeginReplicatedGraphMutation(context.Background(), vertices, edges, hlcFromProto(e.Mutation.GetHlc()), uint64(4*proto.Size(e.Mutation)))
	if err != nil {
		return err
	}
	defer tx.Abort()
	replayed, err := graphMutationProjection(tx.Result(), vi, ei)
	if err != nil {
		return err
	}
	if !graphMutationReplayProjectionMatches(e.Mutation.GetOp().GetGraphMutation().GetProjection(), replayed) {
		return receiptWALUnionError("mixed graph projection drift")
	}
	tx.Commit()
	return nil
}
