package service

import (
	"context"
	"encoding/binary"
	"encoding/hex"
	"log/slog"
	"time"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ApplyMutation is the internal entry point used by the peer-pump (#184)
// and the snapshot bootstrap (#183) to replay a Mutation produced on a
// remote node against the local cache. It is intentionally NOT exposed
// as an RPC: external clients use the regular write RPCs which append to
// the local log via logMutation.
//
// Under the Reading B contract (#415), a successfully-applied remote
// mutation is ALSO appended to the local mutation log so external
// Subscribe consumers on any replica observe every committed cluster
// mutation. Replication loops are prevented by an atomic per-origin
// watermark check at the top of the function: a (origin, seq) pair
// that another peer hop already covered is short-circuited before any
// cache or log mutation happens.
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
		return ctxToConnect(err)
	}
	if m == nil || m.GetOp() == nil {
		return nil
	}

	ts := hlcFromProto(m.GetHlc())
	origin := m.GetOrigin()
	seq := m.GetSeq()

	// Reading B watermark check (#415, B-3). Record CAS-advances the
	// per-origin (last_seq, last_hlc) watermark when seq is strictly
	// greater than what we have already seen for this origin. The
	// bool return drives two later decisions:
	//
	//   - log Append: only on advance, so external Subscribe streams
	//     do not see the same (origin, seq) replayed when the same
	//     mutation arrived through two different peer hops in a
	//     fan-out triangle.
	//   - onApplied hook: only on advance, so peer-status counters
	//     reflect distinct cluster mutations rather than redelivery
	//     volume.
	//
	// The cache apply itself runs unconditionally: it is HLC LWW
	// (Put*) or ContribID-deduped (Add*), so out-of-order or
	// duplicate deliveries are absorbed without divergence. This is
	// the convergence invariant that
	// TestApplyMutation_Convergence stresses.
	var nid hlc.NodeID
	advanced := false
	if s.origins != nil && len(origin) > 0 && seq > 0 {
		copy(nid[:], origin)
		advanced = s.origins.Record(nid, seq, ts)
	}

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
			return nil
		}
		applied := s.cache.PutVertexWithExpirationHLC(v.GetKey(), v, v.GetExpiration().AsTime(), ts)
		if !applied && useTomb && s.onTombstoneClampReject != nil {
			s.onTombstoneClampReject()
		}
		opName = "PutVertex"

	case *pb.MutationOp_PutVertices:
		for _, v := range op.PutVertices.GetVertices() {
			if v == nil {
				continue
			}
			applied := s.cache.PutVertexWithExpirationHLC(v.GetKey(), v, v.GetExpiration().AsTime(), ts)
			if !applied && useTomb && s.onTombstoneClampReject != nil {
				s.onTombstoneClampReject()
			}
		}
		opName = "PutVertices"

	case *pb.MutationOp_DeleteVertex:
		if useTomb {
			s.cache.DeleteVertexHLC(op.DeleteVertex.GetKey(), ts, tombExp)
		} else {
			s.cache.DeleteVertices([]string{op.DeleteVertex.GetKey()})
		}
		opName = "DeleteVertex"

	case *pb.MutationOp_DeleteVertices:
		if useTomb {
			s.cache.DeleteVerticesHLC(op.DeleteVertices.GetKeys(), ts, tombExp)
		} else {
			s.cache.DeleteVertices(op.DeleteVertices.GetKeys())
		}
		opName = "DeleteVertices"

	case *pb.MutationOp_DeleteVerticesByPrefix:
		if useTomb {
			if _, err := s.cache.DeleteByPrefixHLC(ctx, op.DeleteVerticesByPrefix.GetPrefix(), 0, ts, tombExp); err != nil {
				return ctxToConnect(err)
			}
		} else {
			s.cache.DeleteByPrefix(ctx, op.DeleteVerticesByPrefix.GetPrefix(), 0)
		}
		opName = "DeleteVerticesByPrefix"

	case *pb.MutationOp_AddEdge:
		e := op.AddEdge.GetEdge()
		if e == nil {
			return nil
		}
		// Prefer a client-supplied ContribID carried on the wire (#588) so a
		// retried idempotent Add dedups identically on every replica; fall
		// back to the synthesized per-entry id for legacy writes.
		cID := contribIDFromBytes(op.AddEdge.GetContribId())
		if cID.IsZero() {
			cID = contribIDFor(origin, seq, 0)
		}
		if useTomb {
			applied := s.cache.AddEdgeWithExpirationContribHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
				e.GetExpiration().AsTime(), cID, ts)
			if !applied && s.onTombstoneClampReject != nil {
				s.onTombstoneClampReject()
			}
		} else {
			s.cache.AddEdgeWithExpirationContrib(e.GetTail(), e.GetHead(), e.GetWeight(),
				e.GetExpiration().AsTime(), cID)
		}
		opName = "AddEdge"

	case *pb.MutationOp_AddEdges:
		edges := op.AddEdges.GetEdges()
		contribIDs := op.AddEdges.GetContribIds()
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
			if useTomb {
				applied := s.cache.AddEdgeWithExpirationContribHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
					e.GetExpiration().AsTime(), cID, ts)
				if !applied && s.onTombstoneClampReject != nil {
					s.onTombstoneClampReject()
				}
			} else {
				s.cache.AddEdgeWithExpirationContrib(e.GetTail(), e.GetHead(), e.GetWeight(),
					e.GetExpiration().AsTime(), cID)
			}
		}
		opName = "AddEdges"

	case *pb.MutationOp_PutEdge:
		e := op.PutEdge.GetEdge()
		if e == nil {
			return nil
		}
		applied := s.cache.PutEdgeWithExpirationHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
			e.GetExpiration().AsTime(), ts)
		if !applied && useTomb && s.onTombstoneClampReject != nil {
			s.onTombstoneClampReject()
		}
		opName = "PutEdge"

	case *pb.MutationOp_PutEdges:
		for _, e := range op.PutEdges.GetEdges() {
			if e == nil {
				continue
			}
			applied := s.cache.PutEdgeWithExpirationHLC(e.GetTail(), e.GetHead(), e.GetWeight(),
				e.GetExpiration().AsTime(), ts)
			if !applied && useTomb && s.onTombstoneClampReject != nil {
				s.onTombstoneClampReject()
			}
		}
		opName = "PutEdges"

	case *pb.MutationOp_DeleteEdge:
		k := op.DeleteEdge
		if useTomb {
			s.cache.DeleteEdgeHLC(k.GetTail(), k.GetHead(), ts, tombExp)
		} else {
			s.cache.DeleteEdges([]graphcache.EdgeKey[string]{{Tail: k.GetTail(), Head: k.GetHead()}})
		}
		opName = "DeleteEdge"

	case *pb.MutationOp_DeleteEdges:
		in := op.DeleteEdges.GetEdges()
		keys := make([]graphcache.EdgeKey[string], 0, len(in))
		for _, e := range in {
			keys = append(keys, graphcache.EdgeKey[string]{Tail: e.GetTail(), Head: e.GetHead()})
		}
		if useTomb {
			s.cache.DeleteEdgesHLC(keys, ts, tombExp)
		} else {
			s.cache.DeleteEdges(keys)
		}
		opName = "DeleteEdges"
	}

	if opName != "" && s.onReplicationApply != nil {
		s.onReplicationApply(opName)
	}

	// Reading B (#415, B-3). After a successful apply of an entry we
	// have not seen before, also append the mutation to the local log
	// so external Subscribe consumers on this replica observe it. The
	// advanced check above ensures the same (origin, seq) is appended
	// at most once even when it reaches us through multiple peer hops.
	//
	// The append uses the remote HLC unchanged so Entry.HLC.NodeID
	// equals the originating writer's NodeID — that is the field
	// downstream actors (the peer pump, external CDC consumers) read
	// to attribute each entry to its origin.
	//
	// Append errors after a successful apply are logged but not
	// surfaced: the apply already committed and reverting it would
	// leak inconsistency. The next remote receipt of the same
	// mutation will short-circuit at the watermark check.
	if advanced && s.log != nil {
		if _, err := s.log.Append(m, ts); err != nil {
			l := s.logger
			if l == nil {
				l = slog.Default()
			}
			l.Warn("mutation log append on remote apply failed",
				slog.Any("err", err),
				slog.String("origin", hex.EncodeToString(nid[:])),
				slog.Uint64("seq", seq))
		}
	}

	if advanced && s.onApplied != nil {
		s.onApplied(hex.EncodeToString(nid[:]))
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
// Practical batch sizes are bounded by the Connect/gRPC message size cap
// long before this limit; the assertion is defensive.
func contribIDFor(origin []byte, seq uint64, idx uint16) graphcache.ContribID {
	var c graphcache.ContribID
	copy(c[:16], origin)
	combined := (seq << 16) | uint64(idx)
	binary.BigEndian.PutUint64(c[16:], combined)
	return c
}
