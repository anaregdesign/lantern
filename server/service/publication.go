package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// A future origin seq must not become read-visible before the missing prefix.
// These global bounds limit memory while a peer is missing; a rejected entry
// remains absent from graph, log, and cutoff so anti-entropy can recover it.
const (
	maxPendingMutations = 4096
	maxPendingBytes     = 32 << 20
	maxPendingSeqGap    = 4096
)

type pendingMutation struct {
	mutation   *pb.Mutation
	receipt    receiptMutationEnvelope
	receiptWAL receiptMutationEnvelope
	walOp      mutationlog.MutationOp
	size       int
	applied    bool // graph committed; retry the log append without applying twice
	faulted    bool // relay WAL failed after graph apply; all CDC streams are gapped
	opName     string
}

type receiptMutationEnvelope interface {
	GraphMutation() *pb.Mutation
}

func (s *LanternService) validateReplicatedReceiptEnvelope(envelope receiptMutationEnvelope) error {
	switch value := envelope.(type) {
	case *graphAddEffectEnvelope:
		if !value.receiptBearing() {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("graph-only Edge Add is not a receipt envelope"))
		}
		if s.receiptEdgeAddCoordinator == nil {
			return connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("receipt-bearing Edge Add replication apply is not enabled"))
		}
		return s.receiptEdgeAddCoordinator.validateReplicatedEnvelope(value)
	case *edgeDeleteReceiptEnvelope:
		if s.receiptEdgeDeleteCoordinator == nil {
			return connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("receipt-bearing Edge Delete replication apply is not enabled"))
		}
		return s.receiptEdgeDeleteCoordinator.validateReplicatedEnvelope(value)
	case *vertexPutReceiptEnvelope:
		if s.receiptVertexPutCoordinator == nil {
			return connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("receipt-bearing Vertex Put replication apply is not enabled"))
		}
		return s.receiptVertexPutCoordinator.validateReplicatedEnvelope(value)
	case *vertexDeleteReceiptEnvelope:
		if s.receiptVertexDeleteCoordinator == nil {
			return connect.NewError(connect.CodeUnimplemented,
				fmt.Errorf("receipt-bearing Vertex Delete replication apply is not enabled"))
		}
		return s.receiptVertexDeleteCoordinator.validateReplicatedEnvelope(value)
	default:
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("unknown receipt-bearing replication envelope %T", envelope))
	}
}

func sameReceiptMutationIntent(left, right receiptMutationEnvelope) bool {
	switch value := left.(type) {
	case *graphAddEffectEnvelope:
		other, ok := right.(*graphAddEffectEnvelope)
		return ok && value.receiptBearing() && other.receiptBearing() &&
			sameReceiptEdgeAddIntent(value, other)
	case *edgeDeleteReceiptEnvelope:
		other, ok := right.(*edgeDeleteReceiptEnvelope)
		return ok && sameReceiptEdgeDeleteIntent(value, other)
	case *vertexPutReceiptEnvelope:
		other, ok := right.(*vertexPutReceiptEnvelope)
		return ok && sameReceiptVertexPutIntent(value, other)
	case *vertexDeleteReceiptEnvelope:
		other, ok := right.(*vertexDeleteReceiptEnvelope)
		return ok && sameReceiptVertexDeleteIntent(value, other)
	default:
		return left == nil && right == nil
	}
}

func (s *LanternService) commitReplicatedReceipt(
	ctx context.Context,
	origin hlc.NodeID,
	seq uint64,
	ts hlc.Timestamp,
	pending *pendingMutation,
) (string, error) {
	switch pending.receipt.(type) {
	case *graphAddEffectEnvelope:
		if s.receiptEdgeAddCoordinator == nil {
			return "", connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt-bearing Edge Add coordinator became unavailable"))
		}
		return "replicated_receipt_edge_add",
			s.receiptEdgeAddCoordinator.commitReplicated(ctx, origin, seq, ts, pending)
	case *edgeDeleteReceiptEnvelope:
		if s.receiptEdgeDeleteCoordinator == nil {
			return "", connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt-bearing Edge Delete coordinator became unavailable"))
		}
		return "replicated_receipt_edge_delete",
			s.receiptEdgeDeleteCoordinator.commitReplicated(ctx, origin, seq, ts, pending)
	case *vertexPutReceiptEnvelope:
		if s.receiptVertexPutCoordinator == nil {
			return "", connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt-bearing Vertex Put coordinator became unavailable"))
		}
		return "replicated_receipt_vertex_put",
			s.receiptVertexPutCoordinator.commitReplicated(ctx, origin, seq, ts, pending)
	case *vertexDeleteReceiptEnvelope:
		if s.receiptVertexDeleteCoordinator == nil {
			return "", connect.NewError(connect.CodeInternal,
				fmt.Errorf("receipt-bearing Vertex Delete coordinator became unavailable"))
		}
		return "replicated_receipt_vertex_delete",
			s.receiptVertexDeleteCoordinator.commitReplicated(ctx, origin, seq, ts, pending)
	default:
		return "", connect.NewError(connect.CodeInternal,
			fmt.Errorf("receipt-bearing replication coordinator became unavailable"))
	}
}

func publicationGapError() error {
	return connect.NewError(connect.CodeFailedPrecondition,
		errors.New("gapped: mutation publication or Snapshot install requires repair before reading, subscribing, or taking a snapshot"))
}

// BeginSnapshotInstall invalidates the current CDC generation before a peer
// Snapshot can change graph state without individual log entries. An
// interrupted install leaves this node gapped until a later verified install
// advances its watermarks. Only one replay may install at a time.
func (s *LanternService) BeginSnapshotInstall() (func(verified bool), error) {
	if !s.snapshotInstallMu.TryLock() {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("Snapshot install already in progress"))
	}
	s.snapshotReadCutMu.Lock()
	s.replicationCutMu.Lock()
	if !s.snapshotInstallFaulted {
		if s.publicationFaultCount == 0 {
			close(s.publicationFaultCh)
		}
		s.publicationFaultCount++
		s.snapshotInstallFaulted = true
	}
	s.replicationCutMu.Unlock()
	s.snapshotReadCutMu.Unlock()
	return func(verified bool) {
		if verified {
			s.snapshotReadCutMu.Lock()
			s.replicationCutMu.Lock()
			s.snapshotInstallFaulted = false
			s.publicationFaultCount--
			if s.publicationFaultCount == 0 {
				s.publicationFaultCh = make(chan struct{})
			}
			s.replicationCutMu.Unlock()
			s.snapshotReadCutMu.Unlock()
		}
		s.snapshotInstallMu.Unlock()
	}, nil
}

func publicationChangedDuringReadError() error {
	return connect.NewError(connect.CodeUnavailable,
		errors.New("graph publication changed during read; retry"))
}

// Caller holds replicationCutMu. One fault poisons every currently open
// Subscribe generation and prevents a Snapshot from advertising an image
// whose graph effects have no matching local log entry/cutoff.
func (s *LanternService) markPublicationFault(pending *pendingMutation) {
	if pending.faulted {
		return
	}
	pending.faulted = true
	if s.publicationFaultCount == 0 {
		close(s.publicationFaultCh)
	}
	s.publicationFaultCount++
}

// clearPublicationFault must run under replicationCutMu after the original
// mutation has been durably appended (or a verified remote snapshot has
// superseded it). The old generation stays closed after a repair.
func (s *LanternService) clearPublicationFault(pending *pendingMutation) {
	if !pending.faulted {
		return
	}
	s.publicationFaultCount--
	if s.publicationFaultCount == 0 {
		s.publicationFaultCh = make(chan struct{})
	}
}

func (s *LanternService) checkPublicGraphWriteFaultLocked() error {
	if s.snapshotInstallFaulted || s.receiptCommitFaulted {
		return publicationGapError()
	}
	return nil
}

// prepareLocalMutationLocked repairs an earlier graph-applied write before a
// new local write can touch the graph or claim its origin seq. The retained
// mutation is appended as-is; in particular, a conditional Put or a bounded
// prefix Delete is never re-evaluated while repairing its publication.
func (s *LanternService) prepareLocalMutationLocked() error {
	if err := s.checkPublicGraphWriteFaultLocked(); err != nil {
		return err
	}
	if pending := s.pendingLocalMutation; pending != nil {
		if err := s.appendPreparedLocalMutationLocked(pending.walOp); err != nil {
			return connect.NewError(connect.CodeUnavailable,
				fmt.Errorf("local mutation publication repair: %w", err))
		}
		s.pendingLocalMutation = nil
		s.clearPublicationFault(pending)
	}
	if s.log == nil || s.clock == nil {
		return nil
	}
	origin := s.clock.NodeID()
	if origin == (hlc.NodeID{}) {
		return connect.NewError(connect.CodeFailedPrecondition, errors.New("mutation log origin NodeID is zero"))
	}
	if s.origins == nil {
		return connect.NewError(connect.CodeInternal, errors.New("mutation origin state is unavailable"))
	}
	if s.origins.LocalSeq(origin) == ^uint64(0) {
		return connect.NewError(connect.CodeResourceExhausted,
			fmt.Errorf("mutation origin %x sequence exhausted", origin))
	}
	return nil
}

// publishLocalGraphMutationLocked runs after a successful local graph apply
// while the caller still holds replicationCutMu. A failed WAL append leaves
// one owned, exact mutation for append-only repair and poisons CDC until then.
func (s *LanternService) publishLocalGraphPutLocked(op *pb.MutationOp, ts hlc.Timestamp, outcomes []graphcache.PutOutcome) error {
	mutation := s.newLocalMutationLocked(op, ts)
	effect, err := newGraphPutEffectEnvelope(mutation, outcomes)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("local Put publication evidence: %w", err))
	}
	return s.publishLocalGraphEffectLocked(effect)
}

// publishLocalGraphDeleteLocked preserves the exact accepted indexes and
// deadline returned by the same GraphCache application lock.
func (s *LanternService) publishLocalGraphDeleteLocked(op *pb.MutationOp, ts hlc.Timestamp, expiration time.Time, acceptedIndexes []int) error {
	mutation := s.newLocalMutationLocked(op, ts)
	if !expiration.IsZero() {
		mutation.TombstoneExpiration = timestamppb.New(expiration)
	}
	effect, err := newGraphDeleteEffectEnvelope(mutation, acceptedIndexes)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("local Delete publication evidence: %w", err))
	}
	return s.publishLocalGraphEffectLocked(effect)
}

func (s *LanternService) publishLocalGraphAddLocked(mutation *pb.Mutation, accepted []bool) error {
	effect, err := newGraphAddEffectEnvelope(mutation, accepted)
	if err != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("local Add publication evidence: %w", err))
	}
	return s.publishLocalGraphEffectLocked(effect)
}

func (s *LanternService) publishLocalGraphEffectLocked(effect mutationlog.MutationOp) error {
	if effect == nil || s.log == nil || s.clock == nil {
		return nil
	}
	mutation, err := graphEffectMutation(effect)
	if err != nil {
		return connect.NewError(connect.CodeInternal, err)
	}
	if err := s.appendPreparedLocalMutationLocked(effect); err != nil {
		pending := &pendingMutation{mutation: mutation, walOp: effect, size: proto.Size(mutation), applied: true}
		s.pendingLocalMutation = pending
		s.markPublicationFault(pending)
		logger := s.logger
		if logger == nil {
			logger = slog.Default()
		}
		logger.Warn("local mutation log append failed", slog.Any("err", err))
		return connect.NewError(connect.CodeUnavailable,
			fmt.Errorf("local mutation log append (graph effect is ambiguous): %w", err))
	}
	return nil
}

func graphEffectMutation(op mutationlog.MutationOp) (*pb.Mutation, error) {
	switch effect := op.(type) {
	case *graphPutEffectEnvelope:
		if effect != nil && effect.GraphMutation() != nil {
			return effect.GraphMutation(), nil
		}
	case *graphAddEffectEnvelope:
		if effect != nil && effect.GraphMutation() != nil {
			return effect.GraphMutation(), nil
		}
	case *graphDeleteEffectEnvelope:
		if effect != nil && effect.GraphMutation() != nil {
			return effect.GraphMutation(), nil
		}
	}
	return nil, fmt.Errorf("graph publication requires an effect-complete envelope, got %T", op)
}

// validateGraphEffectPublicationShape proves before graph apply that the
// mutation and the largest possible accepted-effect sidecar fit the private
// durable union. Actual accepted effects are still captured from GraphCache's
// final application lock; this preflight never predicts which effects win.
func validateGraphEffectPublicationShape(m *pb.Mutation) error {
	effect, err := maximalGraphEffectPublication(m)
	if err != nil {
		return err
	}
	_, err = encodeReceiptWALUnion(effect)
	return err
}

func maximalGraphEffectPublication(m *pb.Mutation) (mutationlog.MutationOp, error) {
	var effect mutationlog.MutationOp
	switch {
	case isAnyGraphPut(m):
		slots, err := graphPutSlots(m)
		if err != nil {
			return nil, err
		}
		outcomes := make([]graphcache.PutOutcome, 0, len(slots))
		for _, slot := range slots {
			switch slot {
			case graphPutSlotNil:
				continue
			case graphPutSlotBarrier:
				outcomes = append(outcomes, graphcache.PutOutcomeExpired)
			default:
				outcomes = append(outcomes, graphcache.PutOutcomeAppliedAndLive)
			}
		}
		effect, err = newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return nil, err
		}
	case isAnyGraphAdd(m):
		slots, err := graphAddSlots(m)
		if err != nil {
			return nil, err
		}
		accepted := make([]bool, 0, len(slots))
		for _, present := range slots {
			if present {
				accepted = append(accepted, true)
			}
		}
		effect, err = newGraphAddEffectEnvelope(m, accepted)
		if err != nil {
			return nil, err
		}
	case isAnyGraphDelete(m):
		count, ok := graphDeleteRequestCount(m)
		if !ok {
			return nil, receiptWALUnionError("graph Delete publication requires an exact Delete arm")
		}
		var err error
		effect, err = newGraphDeleteEffectEnvelope(m, allAcceptedIndexes(count))
		if err != nil {
			return nil, err
		}
	default:
		return nil, receiptWALUnionError("unsupported graph publication arm")
	}
	return effect, nil
}

func (s *LanternService) validateGraphPublicationShape(m *pb.Mutation) error {
	var err error
	if s.receiptStore != nil {
		err = s.validateDurableGraphMutationPreflight(m)
	} else {
		err = validateGraphEffectPublicationShape(m)
	}
	if err != nil {
		return err
	}
	effect, err := maximalGraphEffectPublication(m)
	if err != nil {
		return err
	}
	return s.validateReplicationFrame(effect)
}

// validateDurableGraphMutationPreflight proves that an owned generic graph
// mutation can complete apply before advancing the receipt Store's persistent
// clock high-water. Graph-only runtimes retain their historical permissive
// nil/empty batch behavior.
func (s *LanternService) validateDurableGraphMutationPreflight(m *pb.Mutation) error {
	if err := validateSyntheticAddMutationBounds(m); err != nil {
		return err
	}
	if _, err := s.validateIncomingTombstoneExpiration(m); err != nil {
		return err
	}
	if err := validateGraphEffectPublicationShape(m); err != nil {
		return err
	}

	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		if op == nil || op.PutVertex == nil || op.PutVertex.GetVertex() == nil {
			return errors.New("PutVertex has no applicable vertex")
		}
		return validateDurableGraphExpiration("PutVertex", op.PutVertex.GetVertex().GetExpiration())
	case *pb.MutationOp_PutVertices:
		if op == nil || op.PutVertices == nil {
			return errors.New("PutVertices request is nil")
		}
		return validateDurableVertexBatch("PutVertices", op.PutVertices.GetVertices())
	case *pb.MutationOp_ReplicatedPutVertices:
		if op == nil || op.ReplicatedPutVertices == nil {
			return errors.New("ReplicatedPutVertices request is nil")
		}
		entries := op.ReplicatedPutVertices.GetEntries()
		if len(entries) == 0 {
			return errors.New("ReplicatedPutVertices has no applicable entry")
		}
		for i, entry := range entries {
			if entry == nil {
				return fmt.Errorf("ReplicatedPutVertices entry %d is nil", i)
			}
			switch outcome := entry.GetOutcome().(type) {
			case *pb.ReplicatedPutVertex_Live:
				if outcome.Live == nil {
					return fmt.Errorf("ReplicatedPutVertices entry %d has a nil live payload", i)
				}
				if err := validateDurableGraphExpiration(
					fmt.Sprintf("ReplicatedPutVertices entry %d", i),
					outcome.Live.GetExpiration(),
				); err != nil {
					return err
				}
			case *pb.ReplicatedPutVertex_CausalBarrier:
				if outcome.CausalBarrier == nil {
					return fmt.Errorf("ReplicatedPutVertices entry %d has a nil causal barrier", i)
				}
			default:
				return fmt.Errorf("ReplicatedPutVertices entry %d has no outcome", i)
			}
		}
		return nil
	case *pb.MutationOp_PutEdge:
		if op == nil || op.PutEdge == nil || op.PutEdge.GetEdge() == nil {
			return errors.New("PutEdge has no applicable edge")
		}
		return validateDurableGraphExpiration("PutEdge", op.PutEdge.GetEdge().GetExpiration())
	case *pb.MutationOp_PutEdges:
		if op == nil || op.PutEdges == nil {
			return errors.New("PutEdges request is nil")
		}
		return validateDurableEdgeBatch("PutEdges", op.PutEdges.GetEdges())
	case *pb.MutationOp_ReplicatedPutEdges:
		if op == nil || op.ReplicatedPutEdges == nil {
			return errors.New("ReplicatedPutEdges request is nil")
		}
		entries := op.ReplicatedPutEdges.GetEntries()
		if len(entries) == 0 {
			return errors.New("ReplicatedPutEdges has no applicable entry")
		}
		for i, entry := range entries {
			if entry == nil {
				return fmt.Errorf("ReplicatedPutEdges entry %d is nil", i)
			}
			switch outcome := entry.GetOutcome().(type) {
			case *pb.ReplicatedPutEdge_Live:
				if outcome.Live == nil {
					return fmt.Errorf("ReplicatedPutEdges entry %d has a nil live payload", i)
				}
				if err := validateDurableGraphExpiration(
					fmt.Sprintf("ReplicatedPutEdges entry %d", i),
					outcome.Live.GetExpiration(),
				); err != nil {
					return err
				}
			case *pb.ReplicatedPutEdge_CausalBarrier:
				if outcome.CausalBarrier == nil {
					return fmt.Errorf("ReplicatedPutEdges entry %d has a nil causal barrier", i)
				}
			default:
				return fmt.Errorf("ReplicatedPutEdges entry %d has no outcome", i)
			}
		}
		return nil
	case *pb.MutationOp_AddEdge:
		if op == nil || op.AddEdge == nil || op.AddEdge.GetEdge() == nil {
			return errors.New("AddEdge has no applicable edge")
		}
		if err := validateDurableContribID("AddEdge", op.AddEdge.GetContribId()); err != nil {
			return err
		}
		return validateDurableGraphExpiration("AddEdge", op.AddEdge.GetEdge().GetExpiration())
	case *pb.MutationOp_AddEdges:
		if op == nil || op.AddEdges == nil {
			return errors.New("AddEdges request is nil")
		}
		edges := op.AddEdges.GetEdges()
		contribIDs := op.AddEdges.GetContribIds()
		if len(contribIDs) > len(edges) {
			return fmt.Errorf("AddEdges has %d contribution IDs for %d edge slots", len(contribIDs), len(edges))
		}
		applicable := 0
		for i, edge := range edges {
			var contribID []byte
			if i < len(contribIDs) {
				contribID = contribIDs[i]
			}
			if edge == nil {
				if len(contribID) != 0 {
					return fmt.Errorf("AddEdges contribution ID %d targets a nil edge slot", i)
				}
				continue
			}
			applicable++
			if err := validateDurableContribID(fmt.Sprintf("AddEdges contribution ID %d", i), contribID); err != nil {
				return err
			}
			if err := validateDurableGraphExpiration(fmt.Sprintf("AddEdges edge %d", i), edge.GetExpiration()); err != nil {
				return err
			}
		}
		if applicable == 0 {
			return errors.New("AddEdges has no applicable edge")
		}
		return nil
	case *pb.MutationOp_DeleteVertex:
		if op == nil || op.DeleteVertex == nil {
			return errors.New("DeleteVertex request is nil")
		}
		return nil
	case *pb.MutationOp_DeleteVertices:
		if op == nil || op.DeleteVertices == nil || len(op.DeleteVertices.GetKeys()) == 0 {
			return errors.New("DeleteVertices has no applicable key")
		}
		return nil
	case *pb.MutationOp_DeleteEdge:
		if op == nil || op.DeleteEdge == nil {
			return errors.New("DeleteEdge request is nil")
		}
		return nil
	case *pb.MutationOp_DeleteEdges:
		if op == nil || op.DeleteEdges == nil || len(op.DeleteEdges.GetEdges()) == 0 {
			return errors.New("DeleteEdges has no applicable edge key")
		}
		for i, edge := range op.DeleteEdges.GetEdges() {
			if edge == nil {
				return fmt.Errorf("DeleteEdges edge key %d is nil", i)
			}
		}
		return nil
	default:
		return errors.New("unsupported durable graph mutation")
	}
}

func validateDurableVertexBatch(name string, vertices []*pb.Vertex) error {
	applicable := 0
	for i, vertex := range vertices {
		if vertex == nil {
			continue
		}
		applicable++
		if err := validateDurableGraphExpiration(fmt.Sprintf("%s vertex %d", name, i), vertex.GetExpiration()); err != nil {
			return err
		}
	}
	if applicable == 0 {
		return fmt.Errorf("%s has no applicable vertex", name)
	}
	return nil
}

func validateDurableEdgeBatch(name string, edges []*pb.Edge) error {
	applicable := 0
	for i, edge := range edges {
		if edge == nil {
			continue
		}
		applicable++
		if err := validateDurableGraphExpiration(fmt.Sprintf("%s edge %d", name, i), edge.GetExpiration()); err != nil {
			return err
		}
	}
	if applicable == 0 {
		return fmt.Errorf("%s has no applicable edge", name)
	}
	return nil
}

func validateDurableGraphExpiration(name string, expiration *timestamppb.Timestamp) error {
	if expiration == nil {
		return nil
	}
	if err := expiration.CheckValid(); err != nil {
		return fmt.Errorf("%s has invalid expiration: %w", name, err)
	}
	return nil
}

func validateDurableContribID(name string, id []byte) error {
	if len(id) != 0 && len(id) != len(graphcache.ContribID{}) {
		return fmt.Errorf("%s must be empty or %d bytes", name, len(graphcache.ContribID{}))
	}
	return nil
}

func allAcceptedIndexes(count int) []int {
	indexes := make([]int, count)
	for i := range indexes {
		indexes[i] = i
	}
	return indexes
}

// publishRemoteMutation runs under replicationCutMu. The watermark is a
// contiguous prefix for each origin, never a maximum observed seq. A future
// mutation owns a copied buffer and has no visible effect until every earlier
// seq commits. Snapshot refuses to serve while a remote graph effect has not
// reached the relay log. Local graph-first writes use the same gate and fault
// generation through publishLocalGraphMutationLocked.
func (s *LanternService) publishRemoteMutation(
	ctx context.Context,
	m *pb.Mutation,
	receipt receiptMutationEnvelope,
) error {
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_DeleteVerticesByPrefix, *pb.MutationOp_DeleteEdgesByPrefix:
		// A predicate may mutate only part of a graph before an interrupted
		// scan returns an error. Current origins publish exact victim IDs;
		// reject obsolete predicate-shaped relay records before queuing or
		// changing any graph state.
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: predicate-shaped delete replay is unsupported; publish exact victims"))
	}
	if len(m.GetOrigin()) != len(hlc.NodeID{}) || m.GetSeq() == 0 || m.GetHlc() == nil || len(m.GetHlc().GetNodeId()) != len(hlc.NodeID{}) {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: mutation requires a 16-byte origin/HLC NodeID and nonzero seq"))
	}
	var origin hlc.NodeID
	copy(origin[:], m.GetOrigin())
	var zero hlc.NodeID
	if origin == zero || zeroNodeID(m.GetHlc().GetNodeId()) {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: zero NodeID is forbidden"))
	}
	if !bytes.Equal(m.GetHlc().GetNodeId(), m.GetOrigin()) {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: HLC NodeID differs from origin"))
	}
	if s.origins == nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("replication: origin state is unavailable"))
	}
	if receipt != nil {
		if err := s.validateReplicatedReceiptEnvelope(receipt); err != nil {
			return err
		}
	} else if err := s.validateDurableRemoteHLC(hlcFromProto(m.GetHlc())); err != nil {
		return err
	}
	committed := s.origins.LocalSeq(origin)
	if m.GetSeq() <= committed {
		return nil
	}
	if s.pendingLocalMutation != nil && origin == s.clock.NodeID() {
		return connect.NewError(connect.CodeFailedPrecondition,
			fmt.Errorf("replication: local origin %x has an unpublished mutation at seq %d", origin, s.pendingLocalMutation.mutation.GetSeq()))
	}
	if m.GetSeq()-committed > maxPendingSeqGap {
		return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("replication: origin %x seq gap exceeds %d", origin, maxPendingSeqGap))
	}
	queue := s.pendingMutations[origin]
	if prev, exists := queue[m.GetSeq()]; exists {
		same := proto.Equal(prev.mutation, m)
		if prev.receipt != nil || receipt != nil {
			same = sameReceiptMutationIntent(prev.receipt, receipt)
		}
		if !same {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("replication: conflicting mutation for origin %x seq %d", origin, m.GetSeq()))
		}
	} else {
		size := proto.Size(m)
		if s.pendingCount >= maxPendingMutations || size > maxPendingBytes-s.pendingBytes {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("replication: pending mutation buffer full"))
		}
		queuedMutation := cloneQueuedMutation(m)
		if receipt != nil {
			// Receipt decoding already made an owned graph projection.
			// Retaining a second full receipt frame would nearly
			// double the bounded pending queue's actual memory.
			queuedMutation = receipt.GraphMutation()
		}
		if queue == nil {
			queue = make(map[uint64]*pendingMutation)
			if s.pendingMutations == nil {
				s.pendingMutations = make(map[hlc.NodeID]map[uint64]*pendingMutation)
			}
			s.pendingMutations[origin] = queue
		}
		queue[m.GetSeq()] = &pendingMutation{
			mutation: queuedMutation,
			receipt:  receipt,
			size:     size,
		}
		s.pendingCount++
		s.pendingBytes += size
	}
	return s.drainRemoteOrigin(ctx, origin)
}

// proto.Clone normalizes nil elements in repeated message fields into empty
// messages. A nil slot is significant to the batch replay code (it must be
// skipped, including for ContribID's wire index), so restore those slots on
// the private queued copy before it reaches the graph or relay log.
func cloneQueuedMutation(m *pb.Mutation) *pb.Mutation {
	copy := proto.Clone(m).(*pb.Mutation)
	switch source := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertices:
		for i, item := range source.PutVertices.GetVertices() {
			if item == nil {
				copy.GetOp().GetPutVertices().Vertices[i] = nil
			}
		}
	case *pb.MutationOp_AddEdges:
		for i, item := range source.AddEdges.GetEdges() {
			if item == nil {
				copy.GetOp().GetAddEdges().Edges[i] = nil
			}
		}
	case *pb.MutationOp_PutEdges:
		for i, item := range source.PutEdges.GetEdges() {
			if item == nil {
				copy.GetOp().GetPutEdges().Edges[i] = nil
			}
		}
	case *pb.MutationOp_DeleteEdges:
		for i, item := range source.DeleteEdges.GetEdges() {
			if item == nil {
				copy.GetOp().GetDeleteEdges().Edges[i] = nil
			}
		}
	case *pb.MutationOp_ReplicatedPutVertices:
		for i, item := range source.ReplicatedPutVertices.GetEntries() {
			if item == nil {
				copy.GetOp().GetReplicatedPutVertices().Entries[i] = nil
			}
		}
	case *pb.MutationOp_ReplicatedPutEdges:
		for i, item := range source.ReplicatedPutEdges.GetEntries() {
			if item == nil {
				copy.GetOp().GetReplicatedPutEdges().Entries[i] = nil
			}
		}
	}
	return copy
}

func zeroNodeID(b []byte) bool {
	for _, v := range b {
		if v != 0 {
			return false
		}
	}
	return true
}

// drainRemoteOrigin commits one contiguous prefix. A failed WAL append leaves
// the already-applied frontier queued and retryable, with no cursor advance.
// The next delivery (including a duplicate) retries it before later seqs.
func (s *LanternService) drainRemoteOrigin(ctx context.Context, origin hlc.NodeID) error {
	queue := s.pendingMutations[origin]
	for len(queue) > 0 {
		if err := ctx.Err(); err != nil {
			return ctxToConnect(err)
		}
		seq := s.origins.LocalSeq(origin) + 1
		pending := queue[seq]
		if pending == nil {
			return nil
		}
		m := pending.mutation
		if pending.receipt != nil {
			opName, err := s.commitReplicatedReceipt(ctx, origin, seq, hlcFromProto(m.GetHlc()), pending)
			if err != nil {
				return err
			}
			if s.onReplicationApply != nil {
				s.onReplicationApply(opName)
			}
			if s.onApplied != nil {
				s.onApplied(hex.EncodeToString(origin[:]))
			}
			s.dropPending(origin, seq)
			queue = s.pendingMutations[origin]
			continue
		}
		if !pending.applied {
			if s.receiptStore != nil {
				ts := hlcFromProto(m.GetHlc())
				if err := s.validateDurableGraphMutationPreflight(m); err != nil {
					return connect.NewError(connect.CodeInvalidArgument,
						fmt.Errorf("replication durable queued preflight for origin %x seq %d: %w", origin, seq, err))
				}
				if err := s.validateDurableRemoteHLC(ts); err != nil {
					return err
				}
				if err := s.persistDurableRemoteClockHighWater(ts); err != nil {
					return err
				}
			}
			result, err := s.applyMutationGraph(m)
			if err != nil {
				if s.receiptStore != nil {
					s.receiptOriginCutMu.Lock()
					s.markReceiptCommitFaultLocked()
					s.receiptOriginCutMu.Unlock()
					return connect.NewError(connect.CodeInternal,
						fmt.Errorf("replication: durable graph apply failed after preflight and clock persistence: %w", err))
				}
				return err
			}
			if result.opName == "" {
				if s.receiptStore != nil {
					s.receiptOriginCutMu.Lock()
					s.markReceiptCommitFaultLocked()
					s.receiptOriginCutMu.Unlock()
					return connect.NewError(connect.CodeInternal,
						fmt.Errorf("replication: durable graph apply produced no effect after preflight and clock persistence for origin %x seq %d", origin, seq))
				}
				s.dropPending(origin, seq)
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: mutation origin %x seq %d has no applicable op", origin, seq))
			}
			pending.applied = true
			pending.opName = result.opName
			pending.walOp = result.walOp
		}
		if s.log != nil {
			if _, err := s.log.Append(pending.walOp, hlcFromProto(m.GetHlc())); err != nil {
				s.markPublicationFault(pending)
				return connect.NewError(connect.CodeUnavailable, fmt.Errorf("replication: relay log append for origin %x seq %d: %w", origin, seq, err))
			}
		}
		if !s.origins.Record(origin, seq, hlcFromProto(m.GetHlc())) {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("replication: noncontiguous publication for origin %x seq %d", origin, seq))
		}
		if s.receiptStore != nil {
			if err := s.clock.RestoreFloor(hlcFromProto(m.GetHlc())); err != nil {
				s.receiptOriginCutMu.Lock()
				s.markReceiptCommitFaultLocked()
				s.receiptOriginCutMu.Unlock()
				return connect.NewError(connect.CodeInternal, fmt.Errorf("replication: durable origin clock floor: %w", err))
			}
		} else if s.clock != nil {
			s.clock.Update(hlcFromProto(m.GetHlc()))
		}
		if s.onReplicationApply != nil {
			s.onReplicationApply(pending.opName)
		}
		if s.onApplied != nil {
			s.onApplied(hex.EncodeToString(origin[:]))
		}
		s.dropPending(origin, seq)
		queue = s.pendingMutations[origin]
	}
	return nil
}

func (s *LanternService) persistDurableRemoteClockHighWater(ts hlc.Timestamp) error {
	tx, err := s.receiptStore.Begin(time.Unix(0, ts.WallNs))
	if err != nil {
		return receiptStoreError(err)
	}
	tx.Abort()
	return nil
}

func (s *LanternService) validateDurableRemoteHLC(ts hlc.Timestamp) error {
	if s.receiptStore == nil {
		return nil
	}
	if s.clock == nil {
		return connect.NewError(connect.CodeInternal, errors.New("replication: durable runtime clock is unavailable"))
	}
	if ts.WallNs <= 0 {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("replication: durable remote HLC wall time must be positive"))
	}
	if time.Unix(0, ts.WallNs).After(s.remoteValidationWall().Add(hlc.DefaultMaxSkew)) {
		return connect.NewError(connect.CodeInvalidArgument, errors.New("replication: durable remote HLC exceeds maximum clock skew"))
	}
	return nil
}

func (s *LanternService) dropPending(origin hlc.NodeID, seq uint64) {
	queue := s.pendingMutations[origin]
	if pending := queue[seq]; pending != nil {
		delete(queue, seq)
		s.pendingCount--
		s.pendingBytes -= pending.size
		s.clearPublicationFault(pending)
	}
	if len(queue) == 0 {
		delete(s.pendingMutations, origin)
	}
}
