package service

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"

	"connectrpc.com/connect"
)

// ApplyMutation is the internal entry point used by the peer-pump (#184)
// and the snapshot bootstrap (#183) to replay a Mutation produced on a
// remote node against the local cache. It is intentionally NOT exposed
// as an RPC: external clients use the regular write RPCs which append to
// the local log via the publication helpers.
//
// Under the Reading B contract (#415), a committed remote mutation is
// appended to the local mutation log for external Subscribe consumers.
// Mutations from one origin commit in seq order; future seqs remain outside
// the graph until the missing prefix arrives. The graph apply, log append,
// and contiguous watermark advance share the Snapshot cut gate.
//
// Idempotence rules per oneof case:
//
//   - Put* (vertex/edge): performed via the LWW-aware
//     PutVertexWithExpirationHLC / PutEdgeWithExpirationHLC. A strictly
//     older HLC is dropped silently; equal-ts writes apply, which is a
//     no-op for value-equal payloads (the convergence guarantee).
//
//   - Add* (edge): performed via the ContribID-aware
//     AddEdgeWithExpirationContrib. The mutation seq is folded together
//     with the per-edge index inside the batch so two edges submitted in
//     the same MutationOp_AddEdges receive distinct ContribIDs while
//     replays of the same (mutation seq, edge index) pair dedup.
//
//   - Delete* (vertex/edge): performed via exact victim identities and a
//     D4-bounded HLC tombstone when enabled. Predicate-shaped legacy relay
//     records are rejected before graph apply.
//
// Returns ctx.Err() when ctx is cancelled. Nil-or-empty mutations are
// dropped silently; malformed sequenced entries fail closed.
func (s *LanternService) ApplyMutation(ctx context.Context, m *pb.Mutation) error {
	if err := ctx.Err(); err != nil {
		return ctxToConnect(err)
	}
	if m == nil || (m.GetSeq() == 0 && len(m.GetOrigin()) == 0 && m.GetHlc() == nil && m.GetOp() == nil) {
		return nil
	}
	if m.GetOp() == nil || m.GetOp().GetOp() == nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: sequenced mutation has no op"))
	}
	if err := validateGenericGraphReceiptContext(m); err != nil {
		return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: %w", err))
	}
	var receiptEnvelope receiptMutationEnvelope
	var receiptErr error
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_ReplicatedReceiptEdgeAdd:
		receiptEnvelope, receiptErr = decodeReceiptEdgeAddMutation(m)
	case *pb.MutationOp_ReplicatedReceiptEdgeDelete:
		receiptEnvelope, receiptErr = decodeReceiptEdgeDeleteMutation(m)
	case *pb.MutationOp_ReplicatedReceiptVertexPut:
		receiptEnvelope, receiptErr = decodeReceiptVertexPutMutation(m)
	case *pb.MutationOp_ReplicatedReceiptVertexDelete:
		receiptEnvelope, receiptErr = decodeReceiptVertexDeleteMutation(m)
	}
	if receiptErr != nil {
		return connect.NewError(connect.CodeInvalidArgument,
			fmt.Errorf("replication receipt envelope: %w", receiptErr))
	}
	frameOp := mutationlog.MutationOp(m)
	if receiptEnvelope != nil {
		frameOp = receiptEnvelope
	}
	if err := s.validateReplicationFrame(frameOp); err != nil {
		return err
	}
	if receiptEnvelope == nil && s.receiptStore != nil {
		if err := s.validateDurableGraphMutationPreflight(m); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication durable preflight: %w", err))
		}
	} else if receiptEnvelope == nil {
		if err := validateSyntheticAddMutationBounds(m); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: %w", err))
		}
		if _, err := s.validateIncomingTombstoneExpiration(m); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: %w", err))
		}
		if err := validateGraphEffectPublicationShape(m); err != nil {
			return connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication publication shape: %w", err))
		}
	}
	if receiptEnvelope == nil {
		effect, err := maximalGraphEffectPublication(m)
		if err != nil {
			return connect.NewError(connect.CodeInvalidArgument,
				fmt.Errorf("replication publication shape: %w", err))
		}
		if err := s.validateReplicationFrame(effect); err != nil {
			return err
		}
	}
	s.replicationCutMu.Lock()
	defer s.replicationCutMu.Unlock()
	if s.receiptCommitFaulted {
		return publicationGapError()
	}
	return s.publishRemoteMutation(ctx, m, receiptEnvelope)
}

// applyMutationGraph runs exactly one sequenced mutation against the graph.
// Caller holds replicationCutMu and publishes only after this succeeds.
type graphApplyResult struct {
	opName string
	walOp  mutationlog.MutationOp
}

func (s *LanternService) applyMutationGraph(m *pb.Mutation) (graphApplyResult, error) {
	if err := validateGenericGraphReceiptContext(m); err != nil {
		return graphApplyResult{}, connect.NewError(
			connect.CodeInvalidArgument,
			fmt.Errorf("replication: %w", err),
		)
	}
	ts := hlcFromProto(m.GetHlc())
	origin := m.GetOrigin()
	seq := m.GetSeq()

	// Reuse the origin's absolute deadline. A delayed relay or FileWAL replay
	// must not renew a Delete floor from its own wall clock.
	useTomb := s.tombstoneTTL > 0
	tombExp, err := s.validateIncomingTombstoneExpiration(m)
	if err != nil {
		return graphApplyResult{}, connect.NewError(connect.CodeInvalidArgument, fmt.Errorf("replication: %w", err))
	}

	// opName is set by each case after it commits to a backend call so
	// the replication-apply hook only fires for genuinely-applied
	// mutations (nil-payload early returns above the switch and inside
	// each arm leave opName empty and skip the counter bump). Per-case
	// strings mirror the proto MutationOp oneof variant names so the
	// metric label matches the wire schema.
	var opName string

	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		v := op.PutVertex.GetVertex()
		if v == nil {
			return graphApplyResult{}, nil
		}
		outcomes := s.cache.PutVerticesWithExpirationHLCOutcomes([]graphcache.VertexItem[string, *pb.Vertex]{{
			Key: v.GetKey(), Value: v, Expiration: prototime.Expiration(v.GetExpiration()),
		}}, ts)
		if outcomes[0] == graphcache.PutOutcomeSuperseded && useTomb && s.onTombstoneClampReject != nil {
			s.onTombstoneClampReject()
		}
		opName = "PutVertex"
		effect, err := newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Put evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_PutVertices:
		// Route the whole batch through the single-lock batch method (#840):
		// one GraphCache.mu cycle instead of N, and search documents are
		// analyzed OUTSIDE the lock (#739). Born-expired items follow the
		// batch dead-on-arrival semantics (#698): nothing is stored, but the
		// HLC watermark is still recorded so a later strictly-older write
		// cannot resurrect the key — the singular path physically stored such
		// entries until GC, which only inflated replica high-water.
		vs := op.PutVertices.GetVertices()
		items := make([]graphcache.VertexItem[string, *pb.Vertex], 0, len(vs))
		for _, v := range vs {
			if v == nil {
				continue
			}
			items = append(items, graphcache.VertexItem[string, *pb.Vertex]{
				Key:        v.GetKey(),
				Value:      v,
				Expiration: prototime.Expiration(v.GetExpiration()),
			})
		}
		outcomes := s.cache.PutVerticesWithExpirationHLCOutcomes(items, ts)
		if useTomb && s.onTombstoneClampReject != nil {
			for _, outcome := range outcomes {
				if outcome == graphcache.PutOutcomeSuperseded {
					s.onTombstoneClampReject()
				}
			}
		}
		opName = "PutVertices"
		effect, err := newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Put evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_ReplicatedPutVertices:
		// Each entry is an origin-authoritative outcome. Replay the full
		// interleaved sequence through one GraphCache lock so duplicate keys
		// retain request order and a clock-behind receiver cannot turn a causal
		// barrier back into a live value.
		entries := op.ReplicatedPutVertices.GetEntries()
		items := make([]graphcache.VertexItem[string, *pb.Vertex], 0, len(entries))
		for _, entry := range entries {
			if entry == nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: nil ReplicatedPutVertex entry"))
			}
			switch outcome := entry.GetOutcome().(type) {
			case *pb.ReplicatedPutVertex_Live:
				v := outcome.Live
				if v == nil {
					return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: nil ReplicatedPutVertex live payload"))
				}
				items = append(items, graphcache.VertexItem[string, *pb.Vertex]{Key: v.GetKey(), Value: v, Expiration: prototime.Expiration(v.GetExpiration())})
			case *pb.ReplicatedPutVertex_CausalBarrier:
				barrier := outcome.CausalBarrier
				if barrier == nil {
					return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: nil ReplicatedPutVertex causal barrier"))
				}
				items = append(items, graphcache.VertexItem[string, *pb.Vertex]{Key: barrier.GetKey(), CausalBarrier: true})
			default:
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: ReplicatedPutVertex entry has no outcome"))
			}
		}
		outcomes := s.cache.PutVerticesWithExpirationHLCOutcomes(items, ts)
		if useTomb && s.onTombstoneClampReject != nil {
			for _, outcome := range outcomes {
				if outcome == graphcache.PutOutcomeSuperseded {
					s.onTombstoneClampReject()
				}
			}
		}
		opName = "ReplicatedPutVertices"
		effect, err := newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Put evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_DeleteVertex:
		if useTomb {
			_, accepted := s.cache.DeleteVerticesHLCDecisions([]string{op.DeleteVertex.GetKey()}, ts, tombExp)
			effect, err := newGraphDeleteEffectEnvelope(m, accepted)
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteVertex", walOp: effect}, nil
		} else {
			s.cache.DeleteVertices([]string{op.DeleteVertex.GetKey()})
			effect, err := newGraphDeleteEffectEnvelope(m, []int{0})
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteVertex", walOp: effect}, nil
		}

	case *pb.MutationOp_DeleteVertices:
		if useTomb {
			_, accepted := s.cache.DeleteVerticesHLCDecisions(op.DeleteVertices.GetKeys(), ts, tombExp)
			effect, err := newGraphDeleteEffectEnvelope(m, accepted)
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteVertices", walOp: effect}, nil
		} else {
			s.cache.DeleteVertices(op.DeleteVertices.GetKeys())
			effect, err := newGraphDeleteEffectEnvelope(m, allAcceptedIndexes(len(op.DeleteVertices.GetKeys())))
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteVertices", walOp: effect}, nil
		}

	case *pb.MutationOp_AddEdge:
		e := op.AddEdge.GetEdge()
		if e == nil {
			return graphApplyResult{}, nil
		}
		// Prefer a client-supplied ContribID carried on the wire (#588) so a
		// retried idempotent Add dedups identically on every replica; fall
		// back to the synthesized per-entry id for legacy writes.
		cID := contribIDFromBytes(op.AddEdge.GetContribId())
		if cID.IsZero() {
			cID = contribIDFor(origin, seq, 0)
		}
		_, accepted, noWeight := s.cache.AddEdgesWithExpirationContribHLCResults([]graphcache.EdgeItem[string]{{
			Tail: e.GetTail(), Head: e.GetHead(), Weight: e.GetWeight(),
			Expiration: prototime.Expiration(e.GetExpiration()), ContribID: cID,
		}}, ts)
		if noWeight != 0 && useTomb && s.onTombstoneClampReject != nil {
			s.onTombstoneClampReject()
		}
		opName = "AddEdge"
		effect, err := newGraphAddEffectEnvelope(m, accepted)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Add evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_AddEdges:
		// Batch-routed (#840): one lock cycle for the whole mutation. The
		// ContribID synthesis fallback keys on the WIRE index (including nil
		// slots) so a mutation with a nil edge in the middle produces the
		// same per-edge dedup ids the singular loop did.
		edges := op.AddEdges.GetEdges()
		contribIDs := op.AddEdges.GetContribIds()
		items := make([]graphcache.EdgeItem[string], 0, len(edges))
		for i, e := range edges {
			if e == nil {
				continue
			}
			// Prefer the index-aligned client ContribID (#588); fall back to
			// the synthesized (origin, seq, idx) id when the wire slot is
			// absent or empty so legacy mutations keep their replay-dedup id.
			var cID graphcache.ContribID
			if i < len(contribIDs) {
				cID = contribIDFromBytes(contribIDs[i])
			}
			if cID.IsZero() {
				cID = contribIDFor(origin, seq, uint16(i))
			}
			items = append(items, graphcache.EdgeItem[string]{
				Tail:       e.GetTail(),
				Head:       e.GetHead(),
				Weight:     e.GetWeight(),
				Expiration: prototime.Expiration(e.GetExpiration()),
				ContribID:  cID,
			})
		}
		// The HLC path is required even when tombstone retention is disabled:
		// permanent Put causal barriers independently fence older additive
		// contributions. The batch return counts every item that added no
		// weight; only expose that through the tombstone-specific hook when D4
		// is enabled.
		_, accepted, noWeight := s.cache.AddEdgesWithExpirationContribHLCResults(items, ts)
		if useTomb && s.onTombstoneClampReject != nil {
			for i := 0; i < noWeight; i++ {
				s.onTombstoneClampReject()
			}
		}
		opName = "AddEdges"
		effect, err := newGraphAddEffectEnvelope(m, accepted)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Add evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_PutEdge:
		e := op.PutEdge.GetEdge()
		if e == nil {
			return graphApplyResult{}, nil
		}
		outcomes := s.cache.PutEdgesWithExpirationHLCOutcomes([]graphcache.EdgeItem[string]{{
			Tail: e.GetTail(), Head: e.GetHead(), Weight: e.GetWeight(),
			Expiration: prototime.Expiration(e.GetExpiration()),
		}}, ts)
		if outcomes[0] == graphcache.PutOutcomeSuperseded && useTomb && s.onTombstoneClampReject != nil {
			s.onTombstoneClampReject()
		}
		opName = "PutEdge"
		effect, err := newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Put evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_PutEdges:
		// Batch-routed (#840): one lock cycle; rejected counts tombstone-
		// fenced and LWW-lost items, matching the singular applied=false set.
		in := op.PutEdges.GetEdges()
		items := make([]graphcache.EdgeItem[string], 0, len(in))
		for _, e := range in {
			if e == nil {
				continue
			}
			items = append(items, graphcache.EdgeItem[string]{
				Tail:       e.GetTail(),
				Head:       e.GetHead(),
				Weight:     e.GetWeight(),
				Expiration: prototime.Expiration(e.GetExpiration()),
			})
		}
		outcomes := s.cache.PutEdgesWithExpirationHLCOutcomes(items, ts)
		if useTomb && s.onTombstoneClampReject != nil {
			for _, outcome := range outcomes {
				if outcome == graphcache.PutOutcomeSuperseded {
					s.onTombstoneClampReject()
				}
			}
		}
		opName = "PutEdges"
		effect, err := newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Put evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_ReplicatedPutEdges:
		entries := op.ReplicatedPutEdges.GetEntries()
		items := make([]graphcache.EdgeItem[string], 0, len(entries))
		for _, entry := range entries {
			if entry == nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: nil ReplicatedPutEdge entry"))
			}
			switch outcome := entry.GetOutcome().(type) {
			case *pb.ReplicatedPutEdge_Live:
				e := outcome.Live
				if e == nil {
					return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: nil ReplicatedPutEdge live payload"))
				}
				items = append(items, graphcache.EdgeItem[string]{Tail: e.GetTail(), Head: e.GetHead(), Weight: e.GetWeight(), Expiration: prototime.Expiration(e.GetExpiration())})
			case *pb.ReplicatedPutEdge_CausalBarrier:
				barrier := outcome.CausalBarrier
				if barrier == nil {
					return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: nil ReplicatedPutEdge causal barrier"))
				}
				items = append(items, graphcache.EdgeItem[string]{Tail: barrier.GetTail(), Head: barrier.GetHead(), CausalBarrier: true})
			default:
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication: ReplicatedPutEdge entry has no outcome"))
			}
		}
		outcomes := s.cache.PutEdgesWithExpirationHLCOutcomes(items, ts)
		if useTomb && s.onTombstoneClampReject != nil {
			for _, outcome := range outcomes {
				if outcome == graphcache.PutOutcomeSuperseded {
					s.onTombstoneClampReject()
				}
			}
		}
		opName = "ReplicatedPutEdges"
		effect, err := newGraphPutEffectEnvelope(m, outcomes)
		if err != nil {
			return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Put evidence: %w", err))
		}
		return graphApplyResult{opName: opName, walOp: effect}, nil

	case *pb.MutationOp_DeleteEdge:
		k := op.DeleteEdge
		if useTomb {
			_, accepted := s.cache.DeleteEdgesHLCDecisions([]graphcache.EdgeKey[string]{{Tail: k.GetTail(), Head: k.GetHead()}}, ts, tombExp)
			effect, err := newGraphDeleteEffectEnvelope(m, accepted)
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteEdge", walOp: effect}, nil
		} else {
			s.cache.DeleteEdges([]graphcache.EdgeKey[string]{{Tail: k.GetTail(), Head: k.GetHead()}})
			effect, err := newGraphDeleteEffectEnvelope(m, []int{0})
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteEdge", walOp: effect}, nil
		}

	case *pb.MutationOp_DeleteEdges:
		in := op.DeleteEdges.GetEdges()
		keys := make([]graphcache.EdgeKey[string], 0, len(in))
		for _, e := range in {
			keys = append(keys, graphcache.EdgeKey[string]{Tail: e.GetTail(), Head: e.GetHead()})
		}
		if useTomb {
			_, accepted := s.cache.DeleteEdgesHLCDecisions(keys, ts, tombExp)
			effect, err := newGraphDeleteEffectEnvelope(m, accepted)
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteEdges", walOp: effect}, nil
		} else {
			s.cache.DeleteEdges(keys)
			effect, err := newGraphDeleteEffectEnvelope(m, allAcceptedIndexes(len(keys)))
			if err != nil {
				return graphApplyResult{}, connect.NewError(connect.CodeInternal, fmt.Errorf("replication Delete evidence: %w", err))
			}
			return graphApplyResult{opName: "DeleteEdges", walOp: effect}, nil
		}

	}

	return graphApplyResult{opName: opName}, nil
}

// validateIncomingTombstoneExpiration bounds a peer's Delete deadline by both
// the origin HLC and the receiver's clock. The origin samples the deadline
// before its HLC stamp, so the first bound pins the original D4 duration;
// the second stops a forged future HLC from extending retention indefinitely.
// D3 permits up to DefaultMaxSkew between the two nodes' wall clocks.
// A delayed deadline may already be past.
func (s *LanternService) validateIncomingTombstoneExpiration(m *pb.Mutation) (time.Time, error) {
	return s.validateIncomingTombstoneExpirationAt(m, s.remoteValidationWall())
}

func (s *LanternService) validateIncomingTombstoneExpirationAt(m *pb.Mutation, localWall time.Time) (time.Time, error) {
	expiration, err := mutationTombstoneExpiration(m, s.tombstoneTTL > 0)
	if err != nil || expiration.IsZero() {
		return expiration, err
	}
	if m.GetHlc() == nil {
		return time.Time{}, fmt.Errorf("Delete mutation lacks its origin HLC")
	}
	if err := s.tombstoneDeadlineBoundsAt(m.GetHlc().GetWallNs(), expiration, localWall); err != nil {
		return time.Time{}, err
	}
	return expiration, nil
}

// mutationTombstoneExpiration enforces the D4 wire shape. The serving apply
// path also checks the configured retention bound before queueing or replay.
func mutationTombstoneExpiration(m *pb.Mutation, retentionEnabled bool) (time.Time, error) {
	if m == nil || m.GetOp() == nil {
		return time.Time{}, fmt.Errorf("mutation has no operation")
	}
	var deleting bool
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_DeleteVertex, *pb.MutationOp_DeleteVertices,
		*pb.MutationOp_DeleteEdge, *pb.MutationOp_DeleteEdges:
		deleting = true
	}
	stamp := m.GetTombstoneExpiration()
	if !deleting {
		if stamp != nil {
			return time.Time{}, fmt.Errorf("non-Delete mutation has a tombstone expiration")
		}
		return time.Time{}, nil
	}
	if !retentionEnabled {
		if stamp != nil {
			return time.Time{}, fmt.Errorf("Delete tombstone retention is disabled on this node")
		}
		return time.Time{}, nil
	}
	if stamp == nil {
		return time.Time{}, fmt.Errorf("Delete mutation lacks its absolute tombstone expiration")
	}
	if err := stamp.CheckValid(); err != nil {
		return time.Time{}, fmt.Errorf("invalid Delete tombstone expiration: %w", err)
	}
	expiration := stamp.AsTime()
	if expiration.Unix() <= 0 {
		return time.Time{}, fmt.Errorf("Delete tombstone expiration must be after Unix epoch")
	}
	return expiration, nil
}

// hlcFromProto converts the wire HLCTimestamp into the in-process value
// type. A nil input yields the zero Timestamp, which Less() treats as the
// minimum — i.e. any stored HLC will win the LWW compare. The NodeId byte
// slice is right-padded/truncated into the fixed-size NodeID array so
// peers using shorter test IDs still produce a stable total order.
func hlcFromProto(p *pb.HLCTimestamp) hlc.Timestamp {
	if p == nil {
		return hlc.Timestamp{}
	}
	var nid hlc.NodeID
	copy(nid[:], p.GetNodeId())
	return hlc.Timestamp{
		WallNs:  p.GetWallNs(),
		Logical: p.GetLogical(),
		NodeID:  nid,
	}
}

// hlcToProto is the inverse of hlcFromProto and is used by the snapshot
// path (#184) to stamp the cutoff HLC and per-entry HLCs onto the wire.
// A zero in-process Timestamp returns nil so wire payloads stay compact
// for entries with no recorded HLC (local-only writes).
func hlcToProto(ts hlc.Timestamp) *pb.HLCTimestamp {
	var zero hlc.Timestamp
	if ts == zero {
		return nil
	}
	return &pb.HLCTimestamp{
		WallNs:  ts.WallNs,
		Logical: ts.Logical,
		NodeId:  append([]byte(nil), ts.NodeID[:]...),
	}
}

// contribIDBytes returns the wire encoding of a ContribID. A zero ContribID
// (the local-only / non-replicated sentinel) is encoded as a nil slice so
// receivers can recognise it explicitly and skip dedup.
func contribIDBytes(c graphcache.ContribID) []byte {
	var zero graphcache.ContribID
	if c == zero {
		return nil
	}
	return append([]byte(nil), c[:]...)
}

// contribIDFromBytes decodes a wire-encoded ContribID. Only the canonical
// 24-byte length is accepted; a shorter, longer, or empty slice yields the
// zero ContribID (the "no identity" sentinel), letting callers fall back to
// a synthesized id and skip dedup. The inverse of contribIDBytes.
func contribIDFromBytes(b []byte) graphcache.ContribID {
	var c graphcache.ContribID
	if len(b) != len(c) {
		return c
	}
	copy(c[:], b)
	return c
}

// contribIDFor builds the dedup identifier for an additive contribution.
// The 24-byte ContribID layout is documented on graphcache.ContribID:
//
//	bytes [0:16] = origin NodeID (replicating node)
//	bytes [16:24] = uint64 BE = (mutation seq << 16) | edge index
//
// Folding the per-edge index into the low bits lets a single
// MutationOp_AddEdges batch carry up to 65 536 distinct edges while still
// guaranteeing a globally unique ContribID per (origin, seq, idx) triple.
// Validating the 16-bit wire index and 48-bit sequence before calling this
// helper is mandatory: configurable batch caps can exceed 65,536 slots.
func contribIDFor(origin []byte, seq uint64, idx uint16) graphcache.ContribID {
	var c graphcache.ContribID
	copy(c[:16], origin)
	combined := (seq << 16) | uint64(idx)
	binary.BigEndian.PutUint64(c[16:], combined)
	return c
}

const (
	maxSyntheticContribIndex    = (1 << 16) - 1
	maxSyntheticContribSequence = (uint64(1) << 48) - 1
)

var (
	errSyntheticContribIndex    = errors.New("synthesized Add ContribID wire index exceeds 16 bits")
	errSyntheticContribSequence = errors.New("synthesized Add ContribID origin sequence exceeds 48 bits")
)

// The 24-byte legacy ID packs an origin into 16 bytes and seq/index into the
// remaining 8. Reject out-of-range fallback inputs before any graph or log
// effect rather than silently aliasing another contribution. Explicit client
// IDs do not consume these packed fields.
func validateSyntheticAddIDs(seq uint64, count int, ids [][]byte) error {
	for i := 0; i < count; i++ {
		if i < len(ids) && !contribIDFromBytes(ids[i]).IsZero() {
			continue
		}
		if i > maxSyntheticContribIndex {
			return fmt.Errorf("%w: %d", errSyntheticContribIndex, i)
		}
		if seq == 0 || seq > maxSyntheticContribSequence {
			return fmt.Errorf("%w: %d", errSyntheticContribSequence, seq)
		}
	}
	return nil
}

func validateSyntheticAddMutationBounds(m *pb.Mutation) error {
	if m == nil || m.GetOp() == nil {
		return nil
	}
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_AddEdge:
		return validateSyntheticAddIDs(m.GetSeq(), 1, [][]byte{op.AddEdge.GetContribId()})
	case *pb.MutationOp_AddEdges:
		return validateSyntheticAddIDs(m.GetSeq(), len(op.AddEdges.GetEdges()), op.AddEdges.GetContribIds())
	default:
		return nil
	}
}
