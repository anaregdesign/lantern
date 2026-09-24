package service

import (
	"encoding/binary"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationlog"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// graphPutEffectEnvelope is private FileWAL evidence for the Put effects this
// receiver actually accepted. Mutation remains the original relay/Subscribe
// projection; an omitted slot was rejected locally and must not be retried
// against a later, possibly expired causal floor. No serving writer selects
// this envelope and no recovery path applies it yet.
type graphPutEffectEnvelope struct {
	Mutation *pb.Mutation
	Accepted []graphPutAcceptedEffect
}

type graphPutEffectKind uint8

const (
	graphPutEffectLive    graphPutEffectKind = 1
	graphPutEffectBarrier graphPutEffectKind = 2
)

type graphPutAcceptedEffect struct {
	Index uint32
	Kind  graphPutEffectKind
}

func (e *graphPutEffectEnvelope) GraphMutation() *pb.Mutation {
	if e == nil {
		return nil
	}
	return e.Mutation
}

// newGraphPutEffectEnvelope consumes outcomes returned from GraphCache's final
// HLC application lock in non-nil wire-slot order. Replication apply compacts
// nil plural slots before calling GraphCache; this constructor maps the compact
// results back to their original positions. It copies the mutation before a
// later relay or retry can mutate its backing slices.
func newGraphPutEffectEnvelope(m *pb.Mutation, outcomes []graphcache.PutOutcome) (*graphPutEffectEnvelope, error) {
	if err := validateReceiptWALGraph(m); err != nil {
		return nil, err
	}
	slots, err := graphPutSlots(m)
	if err != nil {
		return nil, err
	}
	var nonNil int
	for _, slot := range slots {
		if slot != graphPutSlotNil {
			nonNil++
		}
	}
	if len(outcomes) != nonNil {
		return nil, receiptWALUnionError("graph Put outcome count differs from non-nil wire slots")
	}
	e := &graphPutEffectEnvelope{Mutation: cloneQueuedMutation(m)}
	outcomeIndex := 0
	for i, slot := range slots {
		if slot == graphPutSlotNil {
			continue
		}
		outcome := outcomes[outcomeIndex]
		outcomeIndex++
		var kind graphPutEffectKind
		switch outcome {
		case graphcache.PutOutcomeAppliedAndLive:
			kind = graphPutEffectLive
		case graphcache.PutOutcomeExpired:
			kind = graphPutEffectBarrier
		case graphcache.PutOutcomeSuperseded, graphcache.PutOutcomeConditionNotMet:
			continue
		default:
			return nil, receiptWALUnionError("unknown graph Put outcome %d at index %d", outcome, i)
		}
		if uint64(i) > math.MaxUint32 {
			return nil, receiptWALUnionError("graph Put index exceeds WAL range")
		}
		e.Accepted = append(e.Accepted, graphPutAcceptedEffect{Index: uint32(i), Kind: kind})
	}
	if err := validateGraphPutEffectEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

type graphPutSlotKind uint8

const (
	graphPutSlotNil graphPutSlotKind = iota
	graphPutSlotLive
	graphPutSlotBarrier
)

// graphPutSlots preserves original positions, including nil plural entries.
// A ReplicatedPut* causal barrier can only produce a barrier effect; a live
// payload can become a barrier if it expired at this receiver's apply sample.
// The codec can represent nil replicated entries, but current serving apply
// rejects that mutation shape; a future producer must align those contracts.
// Conditional local Put is published as ReplicatedPutVertices accepted slots,
// so raw if_absent requests are not a valid receiver-side WAL shape here.
func graphPutSlots(m *pb.Mutation) ([]graphPutSlotKind, error) {
	if m == nil || m.GetOp() == nil {
		return nil, receiptWALUnionError("graph Put mutation is missing")
	}
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		if op == nil || op.PutVertex == nil || op.PutVertex.GetIfAbsent() {
			break
		}
		if op.PutVertex.GetVertex() == nil {
			return []graphPutSlotKind{graphPutSlotNil}, nil
		}
		return []graphPutSlotKind{graphPutSlotLive}, nil
	case *pb.MutationOp_PutVertices:
		if op == nil || op.PutVertices == nil || op.PutVertices.GetIfAbsent() {
			break
		}
		slots := make([]graphPutSlotKind, len(op.PutVertices.GetVertices()))
		for i, v := range op.PutVertices.GetVertices() {
			if v != nil {
				slots[i] = graphPutSlotLive
			}
		}
		return slots, nil
	case *pb.MutationOp_PutEdge:
		if op == nil || op.PutEdge == nil {
			break
		}
		if op.PutEdge.GetEdge() == nil {
			return []graphPutSlotKind{graphPutSlotNil}, nil
		}
		return []graphPutSlotKind{graphPutSlotLive}, nil
	case *pb.MutationOp_PutEdges:
		if op == nil || op.PutEdges == nil {
			break
		}
		slots := make([]graphPutSlotKind, len(op.PutEdges.GetEdges()))
		for i, edge := range op.PutEdges.GetEdges() {
			if edge != nil {
				slots[i] = graphPutSlotLive
			}
		}
		return slots, nil
	case *pb.MutationOp_ReplicatedPutVertices:
		if op == nil || op.ReplicatedPutVertices == nil {
			break
		}
		slots := make([]graphPutSlotKind, len(op.ReplicatedPutVertices.GetEntries()))
		for i, entry := range op.ReplicatedPutVertices.GetEntries() {
			if entry == nil {
				continue
			}
			switch outcome := entry.GetOutcome().(type) {
			case *pb.ReplicatedPutVertex_Live:
				if outcome.Live == nil {
					return nil, receiptWALUnionError("nil replicated Vertex Put live payload")
				}
				slots[i] = graphPutSlotLive
			case *pb.ReplicatedPutVertex_CausalBarrier:
				if outcome.CausalBarrier == nil {
					return nil, receiptWALUnionError("nil replicated Vertex Put barrier")
				}
				slots[i] = graphPutSlotBarrier
			default:
				return nil, receiptWALUnionError("replicated Vertex Put slot has no outcome")
			}
		}
		return slots, nil
	case *pb.MutationOp_ReplicatedPutEdges:
		if op == nil || op.ReplicatedPutEdges == nil {
			break
		}
		slots := make([]graphPutSlotKind, len(op.ReplicatedPutEdges.GetEntries()))
		for i, entry := range op.ReplicatedPutEdges.GetEntries() {
			if entry == nil {
				continue
			}
			switch outcome := entry.GetOutcome().(type) {
			case *pb.ReplicatedPutEdge_Live:
				if outcome.Live == nil {
					return nil, receiptWALUnionError("nil replicated Edge Put live payload")
				}
				slots[i] = graphPutSlotLive
			case *pb.ReplicatedPutEdge_CausalBarrier:
				if outcome.CausalBarrier == nil {
					return nil, receiptWALUnionError("nil replicated Edge Put barrier")
				}
				slots[i] = graphPutSlotBarrier
			default:
				return nil, receiptWALUnionError("replicated Edge Put slot has no outcome")
			}
		}
		return slots, nil
	}
	return nil, receiptWALUnionError("graph Put effect requires a supported Put arm")
}

func isAnyGraphPut(m *pb.Mutation) bool {
	if m == nil || m.GetOp() == nil {
		return false
	}
	switch m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex, *pb.MutationOp_PutVertices,
		*pb.MutationOp_PutEdge, *pb.MutationOp_PutEdges,
		*pb.MutationOp_ReplicatedPutVertices, *pb.MutationOp_ReplicatedPutEdges:
		return true
	default:
		return false
	}
}

func validateGraphPutEffectEnvelope(e *graphPutEffectEnvelope) error {
	if e == nil {
		return receiptWALUnionError("nil graph Put effect envelope")
	}
	if err := validateReceiptWALGraph(e.Mutation); err != nil {
		return err
	}
	slots, err := graphPutSlots(e.Mutation)
	if err != nil {
		return err
	}
	if len(e.Accepted) > len(slots) {
		return receiptWALUnionError("accepted graph Put count exceeds request count")
	}
	var previous uint32
	for i, effect := range e.Accepted {
		if uint64(effect.Index) >= uint64(len(slots)) || (i > 0 && effect.Index <= previous) {
			return receiptWALUnionError("accepted graph Put index is out of range or unordered")
		}
		slot := slots[effect.Index]
		if slot == graphPutSlotNil || (effect.Kind != graphPutEffectLive && effect.Kind != graphPutEffectBarrier) ||
			(slot == graphPutSlotBarrier && effect.Kind != graphPutEffectBarrier) {
			return receiptWALUnionError("accepted graph Put effect conflicts with original slot")
		}
		previous = effect.Index
	}
	return nil
}

// The inner body has its own strict version boundary within the union kind:
// magic[8], graph length[4], effect count[4], graph body, then fixed
// index[4]/effect[1]/reserved[3] records. The graph body preserves nil slots.
const (
	graphPutEffectWALMagic      = "LGPU\x01\x00\x00\x00"
	graphPutEffectWALHeaderSize = 16
	graphPutEffectWALItemSize   = 8
)

func encodeGraphPutEffectWAL(op mutationlog.MutationOp) ([]byte, error) {
	e, ok := op.(*graphPutEffectEnvelope)
	if !ok {
		return nil, receiptWALUnionError("unexpected graph Put effect type %T", op)
	}
	if err := validateGraphPutEffectEnvelope(e); err != nil {
		return nil, err
	}
	graph, err := encodeReceiptWALGraph(e.Mutation)
	if err != nil {
		return nil, err
	}
	maxBody := receiptWALUnionMaxBytes - receiptWALUnionHeaderSize
	if len(graph) > math.MaxUint32 || len(e.Accepted) > math.MaxUint32 ||
		uint64(len(graph))+graphPutEffectWALHeaderSize+graphPutEffectWALItemSize*uint64(len(e.Accepted)) > uint64(maxBody) {
		return nil, receiptWALUnionError("graph Put effect exceeds FileWAL frame limit")
	}
	out := make([]byte, graphPutEffectWALHeaderSize+len(graph)+graphPutEffectWALItemSize*len(e.Accepted))
	copy(out[:8], graphPutEffectWALMagic)
	binary.BigEndian.PutUint32(out[8:12], uint32(len(graph)))
	binary.BigEndian.PutUint32(out[12:16], uint32(len(e.Accepted)))
	copy(out[graphPutEffectWALHeaderSize:], graph)
	offset := graphPutEffectWALHeaderSize + len(graph)
	for _, effect := range e.Accepted {
		binary.BigEndian.PutUint32(out[offset:offset+4], effect.Index)
		out[offset+4] = byte(effect.Kind)
		offset += graphPutEffectWALItemSize
	}
	return out, nil
}

func decodeGraphPutEffectWAL(body []byte) (*graphPutEffectEnvelope, error) {
	if len(body) < graphPutEffectWALHeaderSize || string(body[:8]) != graphPutEffectWALMagic {
		return nil, receiptWALUnionError("unknown or truncated graph Put effect version")
	}
	graphSize := uint64(binary.BigEndian.Uint32(body[8:12]))
	count := uint64(binary.BigEndian.Uint32(body[12:16]))
	if graphSize > uint64(len(body)-graphPutEffectWALHeaderSize) ||
		count > uint64((len(body)-graphPutEffectWALHeaderSize-int(graphSize))/graphPutEffectWALItemSize) ||
		uint64(graphPutEffectWALHeaderSize)+graphSize+graphPutEffectWALItemSize*count != uint64(len(body)) {
		return nil, receiptWALUnionError("graph Put effect length mismatch")
	}
	graphEnd := graphPutEffectWALHeaderSize + int(graphSize)
	m, err := decodeReceiptWALGraph(body[graphPutEffectWALHeaderSize:graphEnd])
	if err != nil {
		return nil, err
	}
	e := &graphPutEffectEnvelope{Mutation: m}
	if count != 0 {
		e.Accepted = make([]graphPutAcceptedEffect, int(count))
	}
	for i := range e.Accepted {
		offset := graphEnd + i*graphPutEffectWALItemSize
		if body[offset+5] != 0 || body[offset+6] != 0 || body[offset+7] != 0 {
			return nil, receiptWALUnionError("nonzero graph Put effect reserved bytes")
		}
		e.Accepted[i] = graphPutAcceptedEffect{
			Index: binary.BigEndian.Uint32(body[offset : offset+4]),
			Kind:  graphPutEffectKind(body[offset+4]),
		}
	}
	if err := validateGraphPutEffectEnvelope(e); err != nil {
		return nil, fmt.Errorf("graph Put effect: %w", err)
	}
	return e, nil
}
