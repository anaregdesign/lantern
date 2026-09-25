package service

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// graphDeleteEffectEnvelope is private WAL evidence for one graph-only exact
// Delete. Mutation remains the original Subscribe/relay operation; the sidecar
// records which request positions the local GraphCache causally accepted.
// Existed alone cannot distinguish an accepted absent-key tombstone from a
// rejected Delete. Prefix RPCs already project accepted victims into an exact
// DeleteVertices/DeleteEdges Mutation before this envelope is constructed.
// Every serving graph Delete publication selects this payload.
type graphDeleteEffectEnvelope struct {
	Mutation        *pb.Mutation
	AcceptedIndexes []uint32
}

func (e *graphDeleteEffectEnvelope) GraphMutation() *pb.Mutation {
	if e == nil {
		return nil
	}
	return e.Mutation
}

// newGraphDeleteEffectEnvelope detaches both pieces of the decision before a
// future WAL append or retry can observe caller mutation. The caller must get
// acceptedIndexes from the same GraphCache lock as its Delete effect.
func newGraphDeleteEffectEnvelope(m *pb.Mutation, acceptedIndexes []int) (*graphDeleteEffectEnvelope, error) {
	if err := validateReceiptWALGraph(m); err != nil {
		return nil, err
	}
	e := &graphDeleteEffectEnvelope{Mutation: cloneQueuedMutation(m), AcceptedIndexes: make([]uint32, len(acceptedIndexes))}
	for i, index := range acceptedIndexes {
		if index < 0 || uint64(index) > math.MaxUint32 {
			return nil, receiptWALUnionError("invalid accepted Delete index %d", index)
		}
		e.AcceptedIndexes[i] = uint32(index)
	}
	if err := validateGraphDeleteEffectEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

func graphDeleteRequestCount(m *pb.Mutation) (int, bool) {
	if m == nil || m.GetOp() == nil {
		return 0, false
	}
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_DeleteVertex:
		if op != nil && op.DeleteVertex != nil {
			return 1, true
		}
	case *pb.MutationOp_DeleteVertices:
		if op != nil && op.DeleteVertices != nil {
			return len(op.DeleteVertices.GetKeys()), true
		}
	case *pb.MutationOp_DeleteEdge:
		if op != nil && op.DeleteEdge != nil {
			return 1, true
		}
	case *pb.MutationOp_DeleteEdges:
		if op != nil && op.DeleteEdges != nil {
			return len(op.DeleteEdges.GetEdges()), true
		}
	}
	return 0, false
}

func isAnyGraphDelete(m *pb.Mutation) bool {
	if m == nil || m.GetOp() == nil {
		return false
	}
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_DeleteVertex, *pb.MutationOp_DeleteVertices,
		*pb.MutationOp_DeleteVerticesByPrefix, *pb.MutationOp_DeleteEdge,
		*pb.MutationOp_DeleteEdges, *pb.MutationOp_DeleteEdgesByPrefix:
		return true
	default:
		return false
	}
}

func validateGraphDeleteEffectEnvelope(e *graphDeleteEffectEnvelope) error {
	if e == nil {
		return receiptWALUnionError("nil graph Delete effect envelope")
	}
	if err := validateReceiptWALGraph(e.Mutation); err != nil {
		return err
	}
	count, ok := graphDeleteRequestCount(e.Mutation)
	if !ok {
		return receiptWALUnionError("graph Delete effect requires an exact singular or plural Delete arm")
	}
	if len(e.AcceptedIndexes) > count {
		return receiptWALUnionError("accepted Delete count exceeds request count")
	}
	if e.Mutation.GetTombstoneExpiration() == nil && len(e.AcceptedIndexes) != count {
		return receiptWALUnionError("non-tombstone Delete must accept every request position")
	}
	var previous uint32
	for i, index := range e.AcceptedIndexes {
		if uint64(index) >= uint64(count) || (i > 0 && index <= previous) {
			return receiptWALUnionError("accepted Delete index is out of range or unordered")
		}
		if e.Mutation.GetTombstoneExpiration() == nil && index != uint32(i) {
			return receiptWALUnionError("non-tombstone Delete accepted indexes must be complete and ordered")
		}
		previous = index
	}
	return nil
}

// The union version owns the kind/version boundary. The inner body stores the
// existing graph codec body followed by a length-checked indexed sidecar:
// graph length (u32), graph body, accepted count (u32), accepted indexes (u32).
// There is no second deadline: Mutation.tombstone_expiration is authoritative.
func encodeGraphDeleteEffectWAL(op mutationlog.MutationOp) ([]byte, error) {
	e, ok := op.(*graphDeleteEffectEnvelope)
	if !ok {
		return nil, receiptWALUnionError("unexpected graph Delete effect type %T", op)
	}
	if err := validateGraphDeleteEffectEnvelope(e); err != nil {
		return nil, err
	}
	graph, err := encodeReceiptWALGraph(e.Mutation)
	if err != nil {
		return nil, err
	}
	if len(graph) > math.MaxUint32 || len(e.AcceptedIndexes) > math.MaxUint32 ||
		uint64(len(graph))+8+4*uint64(len(e.AcceptedIndexes)) > uint64(receiptWALUnionMaxBytes-receiptWALUnionHeaderSize) {
		return nil, receiptWALUnionError("graph Delete effect exceeds FileWAL frame limit")
	}
	result := make([]byte, 8+len(graph)+4*len(e.AcceptedIndexes))
	binary.BigEndian.PutUint32(result[:4], uint32(len(graph)))
	copy(result[4:], graph)
	countOffset := 4 + len(graph)
	binary.BigEndian.PutUint32(result[countOffset:], uint32(len(e.AcceptedIndexes)))
	for i, index := range e.AcceptedIndexes {
		binary.BigEndian.PutUint32(result[countOffset+4+i*4:], index)
	}
	return result, nil
}

func decodeGraphDeleteEffectWAL(body []byte) (*graphDeleteEffectEnvelope, error) {
	if len(body) < 8 {
		return nil, receiptWALUnionError("graph Delete effect body is truncated")
	}
	graphSize := uint64(binary.BigEndian.Uint32(body[:4]))
	if graphSize > uint64(len(body)-8) {
		return nil, receiptWALUnionError("graph Delete effect graph length exceeds body")
	}
	countOffset := 4 + int(graphSize)
	count := uint64(binary.BigEndian.Uint32(body[countOffset:]))
	if count > uint64((len(body)-countOffset-4)/4) || 8+graphSize+4*count != uint64(len(body)) {
		return nil, receiptWALUnionError("graph Delete effect index length mismatch")
	}
	m, err := decodeReceiptWALGraph(body[4:countOffset])
	if err != nil {
		return nil, err
	}
	e := &graphDeleteEffectEnvelope{Mutation: m, AcceptedIndexes: make([]uint32, int(count))}
	for i := range e.AcceptedIndexes {
		e.AcceptedIndexes[i] = binary.BigEndian.Uint32(body[countOffset+4+i*4:])
	}
	if err := validateGraphDeleteEffectEnvelope(e); err != nil {
		return nil, fmt.Errorf("graph Delete effect: %w", err)
	}
	return e, nil
}
