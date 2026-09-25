package service

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

// graphAddEffectEnvelope is private evidence of the Add slots this receiver
// actually applied. Mutation stays the original relay/Subscribe projection.
// Every serving graph Add publication selects this envelope.
type graphAddEffectEnvelope struct {
	Mutation          *pb.Mutation
	AcceptedIndexes   []uint32
	Origin            hlc.NodeID
	OriginSeq         uint64
	HLC               hlc.Timestamp
	Epoch             mutationreceipt.Epoch
	PolicyFingerprint [32]byte
	Original          []*pb.Edge
	ContribIDs        []graphcache.ContribID
	Receipts          []mutationreceipt.Receipt
}

func (e *graphAddEffectEnvelope) GraphMutation() *pb.Mutation {
	if e == nil {
		return nil
	}
	return e.Mutation
}

func (e *graphAddEffectEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if err := validateGraphAddEffectEnvelope(e); err != nil {
		return nil, err
	}
	return proto.Clone(e.Mutation).(*pb.Mutation), nil
}

func (e *graphAddEffectEnvelope) receiptBearing() bool {
	return e != nil && e.Mutation != nil && e.Mutation.GetOp() != nil &&
		e.Mutation.GetOp().GetReplicatedReceiptEdgeAdd() != nil
}

// newGraphAddEffectEnvelope maps GraphCache results from compact non-nil item
// order back to original wire positions. The caller must obtain those results
// from the final GraphCache application lock, before relay or retry can change
// the source Mutation. A false result is an exact graph no-op (#1367).
func newGraphAddEffectEnvelope(m *pb.Mutation, accepted []bool) (*graphAddEffectEnvelope, error) {
	if err := validateReceiptWALGraph(m); err != nil {
		return nil, err
	}
	slots, err := graphAddSlots(m)
	if err != nil {
		return nil, err
	}
	var nonNil int
	for _, present := range slots {
		if present {
			nonNil++
		}
	}
	if len(accepted) != nonNil {
		return nil, receiptWALUnionError("graph Add result count differs from non-nil wire slots")
	}
	e := &graphAddEffectEnvelope{Mutation: cloneQueuedMutation(m)}
	compact := 0
	for i, present := range slots {
		if !present {
			continue
		}
		applied := accepted[compact]
		compact++
		if !applied {
			continue
		}
		if uint64(i) > math.MaxUint32 {
			return nil, receiptWALUnionError("graph Add index exceeds WAL range")
		}
		e.AcceptedIndexes = append(e.AcceptedIndexes, uint32(i))
	}
	if err := validateGraphAddEffectEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

func isAnyGraphAdd(m *pb.Mutation) bool {
	if m == nil || m.GetOp() == nil {
		return false
	}
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_AddEdge, *pb.MutationOp_AddEdges,
		*pb.MutationOp_ReplicatedReceiptEdgeAdd:
		return true
	default:
		return false
	}
}

// graphAddSlots retains nil plural positions because synthesized ContribIDs
// use the original wire index, not the compact GraphCache item index.
func graphAddSlots(m *pb.Mutation) ([]bool, error) {
	if m == nil || m.GetOp() == nil {
		return nil, receiptWALUnionError("graph Add mutation is missing")
	}
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_AddEdge:
		if op != nil && op.AddEdge != nil && op.AddEdge.GetReceiptContext() == nil {
			return []bool{op.AddEdge.GetEdge() != nil}, nil
		}
	case *pb.MutationOp_AddEdges:
		if op != nil && op.AddEdges != nil && op.AddEdges.GetReceiptContext() == nil {
			slots := make([]bool, len(op.AddEdges.GetEdges()))
			for i, edge := range op.AddEdges.GetEdges() {
				slots[i] = edge != nil
			}
			return slots, nil
		}
	case *pb.MutationOp_ReplicatedReceiptEdgeAdd:
		if op != nil && op.ReplicatedReceiptEdgeAdd != nil {
			slots := make([]bool, len(op.ReplicatedReceiptEdgeAdd.GetItems()))
			for i, item := range op.ReplicatedReceiptEdgeAdd.GetItems() {
				slots[i] = item != nil && item.GetOriginal() != nil
			}
			return slots, nil
		}
	}
	return nil, receiptWALUnionError("graph Add effect requires an AddEdge or AddEdges arm")
}

func validateGraphAddEffectEnvelope(e *graphAddEffectEnvelope) error {
	if e == nil {
		return receiptWALUnionError("nil graph Add effect envelope")
	}
	if err := validateReceiptWALGraph(e.Mutation); err != nil {
		return err
	}
	slots, err := graphAddSlots(e.Mutation)
	if err != nil {
		return err
	}
	if len(e.AcceptedIndexes) > len(slots) {
		return receiptWALUnionError("accepted graph Add count exceeds request count")
	}
	var previous uint32
	for i, index := range e.AcceptedIndexes {
		if uint64(index) >= uint64(len(slots)) || !slots[index] || (i > 0 && index <= previous) {
			return receiptWALUnionError("accepted graph Add index is nil, out of range, or unordered")
		}
		previous = index
	}
	if e.receiptBearing() {
		if _, err := validateReceiptEdgeAddEnvelope(e); err != nil {
			return err
		}
	} else if e.Origin != (hlc.NodeID{}) || e.OriginSeq != 0 ||
		e.HLC != (hlc.Timestamp{}) || e.Epoch != (mutationreceipt.Epoch{}) ||
		e.PolicyFingerprint != ([32]byte{}) || len(e.Original) != 0 ||
		len(e.ContribIDs) != 0 || len(e.Receipts) != 0 {
		return receiptWALUnionError("graph-only Add effect carries receipt evidence")
	}
	return nil
}

// The inner body has an independent version boundary within the WAL union
// kind: magic[8], graph length[4], accepted count[4], graph body, indexes[4].
const (
	graphAddEffectWALMagic      = "LGAD\x01\x00\x00\x00"
	graphAddEffectWALHeaderSize = 16
)

func encodeGraphAddEffectWAL(op mutationlog.MutationOp) ([]byte, error) {
	e, ok := op.(*graphAddEffectEnvelope)
	if !ok {
		return nil, receiptWALUnionError("unexpected graph Add effect type %T", op)
	}
	if err := validateGraphAddEffectEnvelope(e); err != nil {
		return nil, err
	}
	graph, err := encodeReceiptWALGraph(e.Mutation)
	if err != nil {
		return nil, err
	}
	maxBody := receiptWALUnionMaxBytes - receiptWALUnionHeaderSize
	if len(graph) > math.MaxUint32 || len(e.AcceptedIndexes) > math.MaxUint32 ||
		uint64(len(graph))+graphAddEffectWALHeaderSize+4*uint64(len(e.AcceptedIndexes)) > uint64(maxBody) {
		return nil, receiptWALUnionError("graph Add effect exceeds FileWAL frame limit")
	}
	body := make([]byte, graphAddEffectWALHeaderSize+len(graph)+4*len(e.AcceptedIndexes))
	copy(body[:8], graphAddEffectWALMagic)
	binary.BigEndian.PutUint32(body[8:12], uint32(len(graph)))
	binary.BigEndian.PutUint32(body[12:16], uint32(len(e.AcceptedIndexes)))
	copy(body[graphAddEffectWALHeaderSize:], graph)
	offset := graphAddEffectWALHeaderSize + len(graph)
	for i, index := range e.AcceptedIndexes {
		binary.BigEndian.PutUint32(body[offset+4*i:], index)
	}
	return body, nil
}

func decodeGraphAddEffectWAL(body []byte) (*graphAddEffectEnvelope, error) {
	if len(body) < graphAddEffectWALHeaderSize || string(body[:8]) != graphAddEffectWALMagic {
		return nil, receiptWALUnionError("unknown or truncated graph Add effect version")
	}
	graphSize := uint64(binary.BigEndian.Uint32(body[8:12]))
	count := uint64(binary.BigEndian.Uint32(body[12:16]))
	if graphSize > uint64(len(body)-graphAddEffectWALHeaderSize) ||
		count > uint64((len(body)-graphAddEffectWALHeaderSize-int(graphSize))/4) ||
		uint64(graphAddEffectWALHeaderSize)+graphSize+4*count != uint64(len(body)) {
		return nil, receiptWALUnionError("graph Add effect length mismatch")
	}
	graphEnd := graphAddEffectWALHeaderSize + int(graphSize)
	m, err := decodeReceiptWALGraph(body[graphAddEffectWALHeaderSize:graphEnd])
	if err != nil {
		return nil, err
	}
	e := &graphAddEffectEnvelope{Mutation: m}
	if count != 0 {
		e.AcceptedIndexes = make([]uint32, int(count))
	}
	for i := range e.AcceptedIndexes {
		e.AcceptedIndexes[i] = binary.BigEndian.Uint32(body[graphEnd+4*i:])
	}
	if m.GetOp().GetReplicatedReceiptEdgeAdd() != nil {
		if err := hydrateReceiptEdgeAddEnvelope(e); err != nil {
			return nil, err
		}
	}
	if err := validateGraphAddEffectEnvelope(e); err != nil {
		return nil, fmt.Errorf("graph Add effect: %w", err)
	}
	return e, nil
}
