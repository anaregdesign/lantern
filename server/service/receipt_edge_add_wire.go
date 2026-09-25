package service

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"slices"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func receiptEdgeAddDigest(edge *pb.Edge, contribID graphcache.ContribID) ([32]byte, error) {
	if edge == nil || contribID.IsZero() {
		return [32]byte{}, fmt.Errorf("nil Edge or zero ContribID")
	}
	if edge.GetTail() == "" || edge.GetHead() == "" ||
		!utf8.ValidString(edge.GetTail()) || !utf8.ValidString(edge.GetHead()) ||
		len(edge.GetTail()) > receiptVertexWALMaxBytes ||
		len(edge.GetHead()) > receiptVertexWALMaxBytes {
		return [32]byte{}, fmt.Errorf("Edge Add identity must be nonempty bounded UTF-8")
	}
	weight := float64(edge.GetWeight())
	if math.IsNaN(weight) || math.IsInf(weight, 0) {
		return [32]byte{}, fmt.Errorf("Edge Add weight must be finite")
	}
	if edge.GetExpiration() != nil && edge.GetExpiration().CheckValid() != nil {
		return [32]byte{}, fmt.Errorf("Edge Add expiration is invalid")
	}
	if err := rejectProtoUnknownFields(edge.ProtoReflect()); err != nil {
		return [32]byte{}, fmt.Errorf("Edge Add %w", err)
	}
	canonical := make([]byte, 0, 66+len(edge.GetTail())+len(edge.GetHead()))
	canonical = append(canonical, byte(mutationreceipt.AddEdge))
	canonical = appendReceiptCanonicalString(canonical, edge.GetTail())
	canonical = appendReceiptCanonicalString(canonical, edge.GetHead())
	canonical = binary.BigEndian.AppendUint32(canonical, math.Float32bits(edge.GetWeight()))
	if edge.GetExpiration() == nil {
		canonical = append(canonical, 0)
	} else {
		canonical = append(canonical, 1)
		canonical = appendReceiptCanonicalTime(
			canonical,
			edge.GetExpiration().GetSeconds(),
			edge.GetExpiration().GetNanos(),
		)
	}
	canonical = appendReceiptCanonicalBytes(canonical, contribID[:])
	return mutationreceipt.IntentDigest(canonical), nil
}

func receiptEdgeAddMutation(e *graphAddEffectEnvelope) *pb.Mutation {
	items := make([]*pb.ReplicatedReceiptEdgeAddItem, len(e.Receipts))
	accepted := make([]bool, len(e.Receipts))
	for _, index := range e.AcceptedIndexes {
		if int(index) < len(accepted) {
			accepted[index] = true
		}
	}
	for i, receipt := range e.Receipts {
		effective := float32(0)
		if len(receipt.Result) == 4 {
			effective = math.Float32frombits(binary.BigEndian.Uint32(receipt.Result))
		}
		items[i] = &pb.ReplicatedReceiptEdgeAddItem{
			Original:         proto.Clone(e.Original[i]).(*pb.Edge),
			ContribId:        append([]byte(nil), e.ContribIDs[i][:]...),
			CausallyAccepted: accepted[i],
			Receipt: &pb.MutationReceipt{
				OperationId:    append([]byte(nil), receipt.ID[:]...),
				LogicalCallId:  append([]byte(nil), receipt.Group[:]...),
				ItemIndex:      receipt.Index,
				ItemCount:      receipt.Count,
				IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
				DeadlineUnixMs: uint64(receipt.DeadlineMillis),
				OriginalResult: &pb.ReceiptResult{
					Result: &pb.ReceiptResult_AddEdgeEffectiveWeight{
						AddEdgeEffectiveWeight: effective,
					},
				},
			},
		}
	}
	return &pb.Mutation{
		Origin: append([]byte(nil), e.Origin[:]...),
		Seq:    e.OriginSeq,
		Hlc:    hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeAdd{
			ReplicatedReceiptEdgeAdd: &pb.ReplicatedReceiptEdgeAdd{
				DeploymentEpoch:   append([]byte(nil), e.Epoch[:]...),
				PolicyFingerprint: append([]byte(nil), e.PolicyFingerprint[:]...),
				Items:             items,
			},
		}},
	}
}

func decodeReceiptEdgeAddMutation(m *pb.Mutation) (*graphAddEffectEnvelope, error) {
	if m == nil || m.GetOp() == nil {
		return nil, receiptWALUnionError("invalid receipt Add wire envelope header")
	}
	if err := rejectReceiptWALUnknownFields(m.ProtoReflect()); err != nil {
		return nil, err
	}
	call := m.GetOp().GetReplicatedReceiptEdgeAdd()
	if call == nil || m.GetSeq() == 0 || len(m.GetOrigin()) != len(hlc.NodeID{}) ||
		m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 ||
		len(m.GetHlc().GetNodeId()) != len(hlc.NodeID{}) ||
		!bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) ||
		m.GetTombstoneExpiration() != nil ||
		len(call.GetItems()) == 0 || len(call.GetItems()) > receiptVertexWALMaxItems ||
		proto.Size(m) > receiptVertexWALMaxBytes ||
		len(call.GetDeploymentEpoch()) != len(mutationreceipt.Epoch{}) ||
		len(call.GetPolicyFingerprint()) != 32 {
		return nil, receiptWALUnionError("invalid receipt Add wire envelope header")
	}
	e := &graphAddEffectEnvelope{
		Mutation:   proto.Clone(m).(*pb.Mutation),
		OriginSeq:  m.GetSeq(),
		HLC:        hlcFromProto(m.GetHlc()),
		Original:   make([]*pb.Edge, len(call.GetItems())),
		ContribIDs: make([]graphcache.ContribID, len(call.GetItems())),
		Receipts:   make([]mutationreceipt.Receipt, len(call.GetItems())),
	}
	copy(e.Origin[:], m.GetOrigin())
	copy(e.Epoch[:], call.GetDeploymentEpoch())
	copy(e.PolicyFingerprint[:], call.GetPolicyFingerprint())
	for i, item := range call.GetItems() {
		if item == nil || item.GetOriginal() == nil || item.GetReceipt() == nil ||
			len(item.GetContribId()) != len(graphcache.ContribID{}) {
			return nil, receiptWALUnionError("invalid receipt Add wire item %d", i)
		}
		e.Original[i] = proto.Clone(item.GetOriginal()).(*pb.Edge)
		copy(e.ContribIDs[i][:], item.GetContribId())
		if e.ContribIDs[i].IsZero() {
			return nil, receiptWALUnionError("zero receipt Add ContribID at item %d", i)
		}
		wireReceipt := item.GetReceipt()
		if len(wireReceipt.GetOperationId()) != len(mutationreceipt.ID{}) ||
			len(wireReceipt.GetLogicalCallId()) != len(mutationreceipt.GroupID{}) ||
			len(wireReceipt.GetIntentSha256()) != 32 ||
			wireReceipt.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, receiptWALUnionError("invalid receipt Add wire item %d metadata", i)
		}
		result, ok := wireReceipt.GetOriginalResult().GetResult().(*pb.ReceiptResult_AddEdgeEffectiveWeight)
		if !ok {
			return nil, receiptWALUnionError("invalid receipt Add wire item %d result", i)
		}
		receipt := &e.Receipts[i]
		copy(receipt.ID[:], wireReceipt.GetOperationId())
		copy(receipt.Group[:], wireReceipt.GetLogicalCallId())
		receipt.Index, receipt.Count = wireReceipt.GetItemIndex(), wireReceipt.GetItemCount()
		receipt.Kind = mutationreceipt.AddEdge
		receipt.HasContrib = true
		copy(receipt.ContribID[:], item.GetContribId())
		copy(receipt.Digest[:], wireReceipt.GetIntentSha256())
		receipt.DeadlineMillis = int64(wireReceipt.GetDeadlineUnixMs())
		receipt.Result = make([]byte, 4)
		binary.BigEndian.PutUint32(
			receipt.Result,
			math.Float32bits(result.AddEdgeEffectiveWeight),
		)
		if item.GetCausallyAccepted() {
			e.AcceptedIndexes = append(e.AcceptedIndexes, uint32(i))
		}
	}
	if _, err := validateReceiptEdgeAddEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

func acceptedReceiptEdgeAddEdges(m *pb.Mutation) ([]*pb.Edge, error) {
	envelope, err := decodeReceiptEdgeAddMutation(m)
	if err != nil {
		return nil, err
	}
	edges := make([]*pb.Edge, len(envelope.AcceptedIndexes))
	for i, index := range envelope.AcceptedIndexes {
		edges[i] = proto.Clone(envelope.Original[index]).(*pb.Edge)
	}
	return edges, nil
}

func hydrateReceiptEdgeAddEnvelope(e *graphAddEffectEnvelope) error {
	parsed, err := decodeReceiptEdgeAddMutation(e.Mutation)
	if err != nil {
		return err
	}
	if !slices.Equal(e.AcceptedIndexes, parsed.AcceptedIndexes) {
		return receiptWALUnionError("receipt Add accepted projection differs from WAL sidecar")
	}
	e.Origin, e.OriginSeq, e.HLC = parsed.Origin, parsed.OriginSeq, parsed.HLC
	e.Epoch, e.PolicyFingerprint = parsed.Epoch, parsed.PolicyFingerprint
	e.Original, e.ContribIDs, e.Receipts = parsed.Original, parsed.ContribIDs, parsed.Receipts
	return nil
}

func validateReceiptEdgeAddEnvelope(e *graphAddEffectEnvelope) (int64, error) {
	if e == nil || e.Origin == (hlc.NodeID{}) || e.OriginSeq == 0 ||
		e.HLC.WallNs <= 0 || e.HLC.NodeID != e.Origin ||
		e.Epoch == (mutationreceipt.Epoch{}) || e.PolicyFingerprint == ([32]byte{}) {
		return 0, receiptWALUnionError("invalid receipt Add origin, HLC, epoch, or policy metadata")
	}
	count := len(e.Receipts)
	if count == 0 || count > receiptVertexWALMaxItems ||
		len(e.Original) != count || len(e.ContribIDs) != count ||
		len(e.AcceptedIndexes) > count {
		return 0, receiptWALUnionError("invalid receipt Add request alignment or item count")
	}
	group := e.Receipts[0].Group
	if group == (mutationreceipt.GroupID{}) {
		return 0, receiptWALUnionError("zero receipt Add logical-call ID")
	}
	seenIDs := make(map[mutationreceipt.ID]struct{}, count)
	seenContribs := make(map[graphcache.ContribID]struct{}, count)
	var retentionMS int64
	for i, receipt := range e.Receipts {
		if e.Original[i] == nil {
			return 0, receiptWALUnionError("nil receipt Add original item %d", i)
		}
		if err := validateDurableGraphExpiration(
			fmt.Sprintf("receipt Add edge %d", i),
			e.Original[i].GetExpiration(),
		); err != nil {
			return 0, receiptWALUnionError("%v", err)
		}
		digest, err := receiptEdgeAddDigest(e.Original[i], e.ContribIDs[i])
		if err != nil {
			return 0, receiptWALUnionError("invalid receipt Add item %d: %v", i, err)
		}
		if receipt.Group != group {
			return 0, receiptWALUnionError("receipt Add logical-call ID drift at item %d", i)
		}
		if _, duplicate := seenIDs[receipt.ID]; duplicate {
			return 0, receiptWALUnionError("duplicate receipt Add operation ID at item %d", i)
		}
		seenIDs[receipt.ID] = struct{}{}
		if e.ContribIDs[i].IsZero() {
			return 0, receiptWALUnionError("zero receipt Add ContribID at item %d", i)
		}
		if _, duplicate := seenContribs[e.ContribIDs[i]]; duplicate {
			return 0, receiptWALUnionError("duplicate receipt Add ContribID at item %d", i)
		}
		seenContribs[e.ContribIDs[i]] = struct{}{}
		horizon, err := validateReceiptEdgeAddRow(
			receipt, i, count, e.Epoch, digest, e.ContribIDs[i],
		)
		if err != nil {
			return 0, receiptWALUnionError("%v", err)
		}
		if i != 0 && horizon != retentionMS {
			return 0, receiptWALUnionError("inconsistent receipt Add retention at item %d", i)
		}
		retentionMS = horizon
		if len(receipt.Result) != 4 {
			return 0, receiptWALUnionError("invalid receipt Add result at item %d", i)
		}
	}
	previous := -1
	for i, index := range e.AcceptedIndexes {
		if int(index) <= previous || int(index) >= count {
			return 0, receiptWALUnionError("invalid receipt Add accepted index at item %d", i)
		}
		previous = int(index)
	}
	if e.Mutation == nil || e.Mutation.GetOp() == nil ||
		e.Mutation.GetOp().GetReplicatedReceiptEdgeAdd() == nil {
		return 0, receiptWALUnionError("receipt Add envelope has no wire mutation")
	}
	expected := receiptEdgeAddMutation(e)
	gotRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(e.Mutation)
	if err != nil {
		return 0, receiptWALUnionError("marshal receipt Add mutation: %v", err)
	}
	expectedRaw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(expected)
	if err != nil {
		return 0, receiptWALUnionError("marshal canonical receipt Add mutation: %v", err)
	}
	if !bytes.Equal(gotRaw, expectedRaw) {
		return 0, receiptWALUnionError("receipt Add wire mutation differs from canonical evidence")
	}
	return retentionMS, nil
}

func validateReceiptEdgeAddRow(
	receipt mutationreceipt.Receipt,
	index, count int,
	epoch mutationreceipt.Epoch,
	digest [32]byte,
	contribID graphcache.ContribID,
) (int64, error) {
	id, err := mutationreceipt.DecodeID(receipt.ID[:])
	if err != nil || id != receipt.ID || !bytes.Equal(receipt.ID[1:17], epoch[:]) {
		return 0, fmt.Errorf("invalid receipt Add operation ID or epoch at item %d", index)
	}
	issued := binary.BigEndian.Uint64(receipt.ID[17:25])
	if issued > math.MaxInt64 || receipt.DeadlineMillis <= int64(issued) {
		return 0, fmt.Errorf("invalid receipt Add deadline at item %d", index)
	}
	horizon := receipt.DeadlineMillis - int64(issued)
	if horizon < int64(time.Hour/time.Millisecond) ||
		horizon > int64((30*24*time.Hour)/time.Millisecond) {
		return 0, fmt.Errorf("invalid receipt Add retention at item %d", index)
	}
	var receiptContrib graphcache.ContribID
	copy(receiptContrib[:], receipt.ContribID[:])
	if receipt.Group == (mutationreceipt.GroupID{}) ||
		receipt.Index != uint32(index) || receipt.Count != uint32(count) ||
		receipt.Kind != mutationreceipt.AddEdge || !receipt.HasContrib ||
		receiptContrib != contribID || receipt.Digest != digest {
		return 0, fmt.Errorf("receipt Add intent or index drift at item %d", index)
	}
	return horizon, nil
}

// sameReceiptEdgeAddIntent excludes the sender-local accepted projection.
// Each relay persists the same origin evidence but computes its own causal
// projection against local graph history.
func sameReceiptEdgeAddIntent(a, b *graphAddEffectEnvelope) bool {
	if a == nil || b == nil || a.Origin != b.Origin || a.OriginSeq != b.OriginSeq ||
		a.HLC != b.HLC || a.Epoch != b.Epoch ||
		a.PolicyFingerprint != b.PolicyFingerprint ||
		len(a.Original) != len(b.Original) ||
		len(a.ContribIDs) != len(b.ContribIDs) ||
		len(a.Receipts) != len(b.Receipts) {
		return false
	}
	for i := range a.Original {
		left, err := (proto.MarshalOptions{Deterministic: true}).Marshal(a.Original[i])
		if err != nil {
			return false
		}
		right, err := (proto.MarshalOptions{Deterministic: true}).Marshal(b.Original[i])
		if err != nil || !bytes.Equal(left, right) || a.ContribIDs[i] != b.ContribIDs[i] {
			return false
		}
		ar, br := a.Receipts[i], b.Receipts[i]
		if ar.Intent != br.Intent || ar.DeadlineMillis != br.DeadlineMillis ||
			!bytes.Equal(ar.Result, br.Result) {
			return false
		}
	}
	return true
}
