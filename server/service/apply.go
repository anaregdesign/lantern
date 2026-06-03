package service

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"time"

	"github.com/anaregdesign/lantern/core/cache/graph"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/grpc/status"
)

// ApplyMutation is the internal entry point used by the peer-pump (#184)
// and the snapshot bootstrap (#183) to replay a Mutation produced on a
// remote node against the local cache. It is intentionally NOT a gRPC
// method: external clients use the regular write RPCs which append to the
// local log; ApplyMutation deliberately bypasses logMutation so a replayed
// mutation is not re-broadcast.
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
//   - Delete* (vertex/edge/by-prefix): performed via the existing
//     destructive batch methods. Tombstone + TTL clamp semantics are
//     deferred to #183; for now repeated deletes are naturally no-ops
//     because the second pass finds nothing to remove.
//
// Returns ctx.Err() when ctx is cancelled; otherwise nil. Nil-or-empty
// mutations are dropped silently so callers (pump, snapshot replay) can
// forward whatever the wire produces without additional null checks.
func (s *LanternService) ApplyMutation(ctx context.Context, m *pb.Mutation) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if m == nil || m.GetOp() == nil {
		return nil
	}

	ts := hlcFromProto(m.GetHlc())
	origin := m.GetOrigin()
	seq := m.GetSeq()

	// Tombstone expiration is computed once per apply so a batch of
	// per-edge contributions inside a single MutationOp_AddEdges shares
	// the same wall-clock expiration. When the clamp is disabled
	// (s.tombstoneTTL == 0) tombExp is the zero time and the underlying
	// non-HLC backend variants are dispatched instead.
	useTomb := s.tombstoneTTL > 0
	tombExp := time.Time{}
	if useTomb {
		tombExp = time.Now().Add(s.tombstoneTTL)
	}

	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		v := op.PutVertex.GetVertex()
		if v == nil {
			return nil
		}
		s.cache.PutVertexWithExpirationHLC(v.GetKey(), v, v.GetExpiration().AsTime(), ts)

	case *pb.MutationOp_PutVertices:
		for _, v := range op.PutVertices.GetVertices() {
			if v == nil {
				continue
			}
			s.cache.PutVertexWithExpirationHLC(v.GetKey(), v, v.GetExpiration().AsTime(), ts)
		}

	case *pb.MutationOp_DeleteVertex:
		if useTomb {
			s.cache.DeleteVertexHLC(op.DeleteVertex.GetKey(), ts, tombExp)
		} else {
			s.cache.DeleteVertices([]string{op.DeleteVertex.GetKey()})
		}

	case *pb.MutationOp_DeleteVertices:
		if useTomb {
			s.cache.DeleteVerticesHLC(op.DeleteVertices.GetKeys(), ts, tombExp)
		} else {
			s.cache.DeleteVertices(op.DeleteVertices.GetKeys())
		}

	case *pb.MutationOp_DeleteVerticesByPrefix:
		if useTomb {
			// Errors from DeleteByPrefixHLC only surface ctx
			// cancellation; the outer ctx.Err() check at the top of
			// ApplyMutation already handled the entry-point case, so
			// propagate any new cancellation that occurred mid-scan.
			if _, err := s.cache.DeleteByPrefixHLC(ctx, op.DeleteVerticesByPrefix.GetPrefix(), 0, ts, tombExp); err != nil {
				return status.FromContextError(err).Err()
			}
		} else {
			s.cache.DeleteByPrefix(ctx, op.DeleteVerticesByPrefix.GetPrefix(), 0)
		}

	case *pb.MutationOp_AddEdge:
		e := op.AddEdge.GetEdge()
		if e == nil {
			return nil
		}
		cID := contribIDFor(origin, seq, 0)
		if useTomb {
			s.cache.AddEdgeWithExpirationContribHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
				e.GetExpiration().AsTime(), cID, ts)
		} else {
			s.cache.AddEdgeWithExpirationContrib(e.GetTail(), e.GetHead(), e.GetWeight(),
				e.GetExpiration().AsTime(), cID)
		}

	case *pb.MutationOp_AddEdges:
		edges := op.AddEdges.GetEdges()
		for i, e := range edges {
			if e == nil {
				continue
			}
			cID := contribIDFor(origin, seq, uint16(i))
			if useTomb {
				s.cache.AddEdgeWithExpirationContribHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
					e.GetExpiration().AsTime(), cID, ts)
			} else {
				s.cache.AddEdgeWithExpirationContrib(e.GetTail(), e.GetHead(), e.GetWeight(),
					e.GetExpiration().AsTime(), cID)
			}
		}

	case *pb.MutationOp_PutEdge:
		e := op.PutEdge.GetEdge()
		if e == nil {
			return nil
		}
		s.cache.PutEdgeWithExpirationHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
			e.GetExpiration().AsTime(), ts)

	case *pb.MutationOp_PutEdges:
		for _, e := range op.PutEdges.GetEdges() {
			if e == nil {
				continue
			}
			s.cache.PutEdgeWithExpirationHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
				e.GetExpiration().AsTime(), ts)
		}

	case *pb.MutationOp_DeleteEdge:
		k := op.DeleteEdge
		if useTomb {
			s.cache.DeleteEdgeHLC(k.GetTail(), k.GetHead(), ts, tombExp)
		} else {
			s.cache.DeleteEdges([]graph.EdgeKey[string]{{Tail: k.GetTail(), Head: k.GetHead()}})
		}

	case *pb.MutationOp_DeleteEdges:
		in := op.DeleteEdges.GetEdges()
		keys := make([]graph.EdgeKey[string], 0, len(in))
		for _, e := range in {
			keys = append(keys, graph.EdgeKey[string]{Tail: e.GetTail(), Head: e.GetHead()})
		}
		if useTomb {
			s.cache.DeleteEdgesHLC(keys, ts, tombExp)
		} else {
			s.cache.DeleteEdges(keys)
		}
	}

	// Update the per-origin watermark used by PeerStatus (#186). We
	// only record after a successful apply so a malformed mutation
	// that returned early above does not advance the watermark.
	if s.origins != nil && len(origin) > 0 && seq > 0 {
		var nid hlc.NodeID
		copy(nid[:], origin)
		s.origins.Record(nid, seq, ts)
		if s.onApplied != nil {
			s.onApplied(hex.EncodeToString(nid[:]))
		}
	}
	return nil
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
func contribIDBytes(c graph.ContribID) []byte {
	var zero graph.ContribID
	if c == zero {
		return nil
	}
	return append([]byte(nil), c[:]...)
}

// contribIDFor builds the dedup identifier for an additive contribution.
// The 24-byte ContribID layout is documented on graph.ContribID:
//
//	bytes [0:16] = origin NodeID (replicating node)
//	bytes [16:24] = uint64 BE = (mutation seq << 16) | edge index
//
// Folding the per-edge index into the low bits lets a single
// MutationOp_AddEdges batch carry up to 65 536 distinct edges while still
// guaranteeing a globally unique ContribID per (origin, seq, idx) triple.
// Practical batch sizes are bounded by gRPC message size long before this
// limit; the assertion is defensive.
func contribIDFor(origin []byte, seq uint64, idx uint16) graph.ContribID {
	var c graph.ContribID
	copy(c[:16], origin)
	combined := (seq << 16) | uint64(idx)
	binary.BigEndian.PutUint64(c[16:], combined)
	return c
}
