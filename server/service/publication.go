package service

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
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
	mutation *pb.Mutation
	size     int
	applied  bool // graph committed; retry the log append without applying twice
	faulted  bool // relay WAL failed after graph apply; all CDC streams are gapped
	opName   string
}

func publicationGapError() error {
	return connect.NewError(connect.CodeFailedPrecondition,
		errors.New("gapped: mutation publication or Snapshot install requires repair"))
}

// BeginSnapshotInstall invalidates the current CDC generation before a peer
// Snapshot can change graph state without individual log entries. An
// interrupted install leaves this node gapped until a later verified install
// advances its watermarks. Only one replay may install at a time.
func (s *LanternService) BeginSnapshotInstall() (func(verified bool), error) {
	if !s.snapshotInstallMu.TryLock() {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("Snapshot install already in progress"))
	}
	s.replicationCutMu.Lock()
	if !s.snapshotInstallFaulted {
		if s.publicationFaultCount == 0 {
			close(s.publicationFaultCh)
		}
		s.publicationFaultCount++
		s.snapshotInstallFaulted = true
	}
	s.replicationCutMu.Unlock()
	return func(verified bool) {
		if verified {
			s.replicationCutMu.Lock()
			s.snapshotInstallFaulted = false
			s.publicationFaultCount--
			if s.publicationFaultCount == 0 {
				s.publicationFaultCh = make(chan struct{})
			}
			s.replicationCutMu.Unlock()
		}
		s.snapshotInstallMu.Unlock()
	}, nil
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

// prepareLocalMutationLocked repairs an earlier graph-applied write before a
// new local write can touch the graph or claim its origin seq. The retained
// mutation is appended as-is; in particular, a conditional Put or a bounded
// prefix Delete is never re-evaluated while repairing its publication.
func (s *LanternService) prepareLocalMutationLocked() error {
	if pending := s.pendingLocalMutation; pending != nil {
		if err := s.appendPreparedLocalMutationLocked(pending.mutation); err != nil {
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
func (s *LanternService) publishLocalGraphMutationLocked(op *pb.MutationOp, ts hlc.Timestamp) error {
	if op == nil || s.log == nil || s.clock == nil {
		return nil
	}
	mutation := s.newLocalMutationLocked(op, ts)
	if err := s.appendPreparedLocalMutationLocked(mutation); err != nil {
		pending := &pendingMutation{mutation: cloneQueuedMutation(mutation), size: proto.Size(mutation), applied: true}
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

// publishRemoteMutation runs under replicationCutMu. The watermark is a
// contiguous prefix for each origin, never a maximum observed seq. A future
// mutation owns a copied buffer and has no visible effect until every earlier
// seq commits. Snapshot refuses to serve while a remote graph effect has not
// reached the relay log. Local graph-first writes use the same gate and fault
// generation through publishLocalGraphMutationLocked.
func (s *LanternService) publishRemoteMutation(ctx context.Context, m *pb.Mutation) error {
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
		if !proto.Equal(prev.mutation, m) {
			return connect.NewError(connect.CodeFailedPrecondition, fmt.Errorf("replication: conflicting mutation for origin %x seq %d", origin, m.GetSeq()))
		}
	} else {
		size := proto.Size(m)
		if s.pendingCount >= maxPendingMutations || size > maxPendingBytes-s.pendingBytes {
			return connect.NewError(connect.CodeResourceExhausted, fmt.Errorf("replication: pending mutation buffer full"))
		}
		if queue == nil {
			queue = make(map[uint64]*pendingMutation)
			if s.pendingMutations == nil {
				s.pendingMutations = make(map[hlc.NodeID]map[uint64]*pendingMutation)
			}
			s.pendingMutations[origin] = queue
		}
		queue[m.GetSeq()] = &pendingMutation{mutation: cloneQueuedMutation(m), size: size}
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
		if !pending.applied {
			opName, err := s.applyMutationGraph(m)
			if err != nil {
				return err
			}
			if opName == "" {
				s.dropPending(origin, seq)
				return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: mutation origin %x seq %d has no applicable op", origin, seq))
			}
			pending.applied = true
			pending.opName = opName
		}
		if s.log != nil {
			if _, err := s.log.Append(m, hlcFromProto(m.GetHlc())); err != nil {
				s.markPublicationFault(pending)
				return connect.NewError(connect.CodeUnavailable, fmt.Errorf("replication: relay log append for origin %x seq %d: %w", origin, seq, err))
			}
		}
		if !s.origins.Record(origin, seq, hlcFromProto(m.GetHlc())) {
			return connect.NewError(connect.CodeInternal, fmt.Errorf("replication: noncontiguous publication for origin %x seq %d", origin, seq))
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
