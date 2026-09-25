package service

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"

	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
)

const (
	receiptVertexWALMaxBytes = 8 << 20
	// A valid canonical Delete item consumes at least 121 bytes on the wire
	// (Put consumes at least 123). Keep the hard item cap below both the byte
	// ceiling and the production default plural-RPC limit.
	receiptVertexWALMinCanonicalItemBytes = 121
	receiptVertexProductionBatchMaxItems  = 10_000
	receiptVertexWALMaxItems              = min(
		receiptVertexWALMaxBytes/receiptVertexWALMinCanonicalItemBytes,
		receiptVertexProductionBatchMaxItems,
	)
)

const (
	receiptVertexPutMutationArm    protoreflect.FieldNumber = 16
	receiptVertexDeleteMutationArm protoreflect.FieldNumber = 17
	receiptVertexItemsField        protoreflect.FieldNumber = 4
)

var (
	errReceiptVertexPutWAL          = errors.New("service: invalid receipt Vertex Put WAL payload")
	errReceiptVertexPutWireCapacity = errors.New("receipt Vertex Put wire frame exceeds 8 MiB")
	errReceiptVertexWALCapacity     = errors.New("service: receipt Vertex WAL payload exceeds capacity")
)

func receiptVertexPutWALError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errReceiptVertexPutWAL, fmt.Sprintf(format, args...))
}

func receiptVertexPutWALCapacityError() error {
	return errors.Join(errReceiptVertexPutWAL, errReceiptVertexWALCapacity)
}

func worstCaseReceiptVertexWALReceipt(result *pb.ReceiptResult) *pb.MutationReceipt {
	return &pb.MutationReceipt{
		OperationId:    make([]byte, len(mutationreceipt.ID{})),
		LogicalCallId:  make([]byte, len(mutationreceipt.GroupID{})),
		ItemIndex:      math.MaxUint32,
		ItemCount:      math.MaxUint32,
		IntentSha256:   make([]byte, sha256.Size),
		DeadlineUnixMs: math.MaxUint64,
		OriginalResult: result,
	}
}

func worstCaseReceiptVertexWALMutationSize(
	callSize int,
	arm protoreflect.FieldNumber,
) int {
	base := proto.Size(&pb.Mutation{
		Seq: math.MaxUint64,
		Hlc: &pb.HLCTimestamp{
			WallNs:  math.MaxInt64,
			Logical: math.MaxUint32,
			NodeId:  make([]byte, len(hlc.NodeID{})),
		},
		Origin: make([]byte, len(hlc.NodeID{})),
	})
	opSize := protowire.SizeTag(protowire.Number(arm)) + protowire.SizeBytes(callSize)
	return base + protowire.SizeTag(4) + protowire.SizeBytes(opSize)
}

// validateReceiptVertexPutWALRequestCapacity proves that the largest possible
// receiver-local projection (every item accepted live) fits before Store,
// graph, clock, origin, or WAL state is touched. It references request values
// without cloning their payload bytes and retains no per-item allocation.
func validateReceiptVertexPutWALRequestCapacity(vertices []*pb.Vertex) error {
	if len(vertices) == 0 || len(vertices) > receiptVertexWALMaxItems {
		return receiptVertexPutWALError("invalid request item count")
	}
	callSize := proto.Size(&pb.ReplicatedReceiptVertexPut{
		DeploymentEpoch:   make([]byte, len(mutationreceipt.Epoch{})),
		PolicyFingerprint: make([]byte, sha256.Size),
		IfAbsent:          true,
	})
	receipt := worstCaseReceiptVertexWALReceipt(&pb.ReceiptResult{
		Result: &pb.ReceiptResult_PutVertexOutcome{
			PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_SUPERSEDED,
		},
	})
	for _, vertex := range vertices {
		itemSize := proto.Size(&pb.ReplicatedReceiptVertexPutItem{
			Original: vertex,
			Receipt:  receipt,
			Accepted: &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_Live{Live: vertex},
			},
		})
		fieldSize := protowire.SizeTag(4) + protowire.SizeBytes(itemSize)
		if callSize > receiptVertexWALMaxBytes ||
			fieldSize > receiptVertexWALMaxBytes-callSize {
			return receiptVertexPutWALCapacityError()
		}
		callSize += fieldSize
	}
	if worstCaseReceiptVertexWALMutationSize(
		callSize,
		receiptVertexPutMutationArm,
	) > receiptVertexWALMaxBytes {
		return receiptVertexPutWALCapacityError()
	}
	return nil
}

func (e *vertexPutReceiptEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if _, err := validateReceiptVertexPutWALEnvelope(e); err != nil {
		return nil, err
	}
	return receiptVertexPutReplicationMutation(e), nil
}

func receiptVertexPutReplicationMutation(e *vertexPutReceiptEnvelope) *pb.Mutation {
	accepted := make(map[int]*pb.ReplicatedPutVertex, len(e.Accepted))
	for _, item := range e.Accepted {
		switch item.Outcome {
		case graphcache.PutOutcomeAppliedAndLive:
			accepted[item.Index] = &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_Live{Live: proto.Clone(item.Item.Value).(*pb.Vertex)},
			}
		case graphcache.PutOutcomeExpired:
			accepted[item.Index] = &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_CausalBarrier{
					CausalBarrier: &pb.VertexCausalBarrier{Key: item.Item.Key},
				},
			}
		}
	}
	items := make([]*pb.ReplicatedReceiptVertexPutItem, len(e.Receipts))
	group := e.Receipts[0].Group
	for i, receipt := range e.Receipts {
		items[i] = &pb.ReplicatedReceiptVertexPutItem{
			Original: proto.Clone(e.Original[i]).(*pb.Vertex),
			Receipt: &pb.MutationReceipt{
				OperationId:   append([]byte(nil), receipt.ID[:]...),
				LogicalCallId: append([]byte(nil), group[:]...),
				ItemIndex:     receipt.Index, ItemCount: receipt.Count,
				IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
				DeadlineUnixMs: uint64(receipt.DeadlineMillis),
				OriginalResult: &pb.ReceiptResult{
					Result: &pb.ReceiptResult_PutVertexOutcome{
						PutVertexOutcome: pb.PutOutcome(receipt.Result[0]),
					},
				},
			},
			Accepted: accepted[i],
		}
	}
	return &pb.Mutation{
		Seq: e.OriginSeq, Origin: append([]byte(nil), e.Origin[:]...), Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptVertexPut{
			ReplicatedReceiptVertexPut: &pb.ReplicatedReceiptVertexPut{
				DeploymentEpoch:   append([]byte(nil), e.Epoch[:]...),
				PolicyFingerprint: append([]byte(nil), e.PolicyFingerprint[:]...),
				IfAbsent:          e.IfAbsent, Items: items,
			},
		}},
	}
}

func receiptVertexPutGraphMutation(e *vertexPutReceiptEnvelope) *pb.Mutation {
	entries := make([]*pb.ReplicatedPutVertex, len(e.Accepted))
	for i, item := range e.Accepted {
		switch item.Outcome {
		case graphcache.PutOutcomeAppliedAndLive:
			entries[i] = &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_Live{Live: proto.Clone(item.Item.Value).(*pb.Vertex)},
			}
		case graphcache.PutOutcomeExpired:
			entries[i] = &pb.ReplicatedPutVertex{
				Outcome: &pb.ReplicatedPutVertex_CausalBarrier{
					CausalBarrier: &pb.VertexCausalBarrier{Key: item.Item.Key},
				},
			}
		}
	}
	return &pb.Mutation{
		Origin: append([]byte(nil), e.Origin[:]...), Seq: e.OriginSeq, Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedPutVertices{
			ReplicatedPutVertices: &pb.ReplicatedPutVertices{Entries: entries},
		}},
	}
}

func acceptedReceiptVertexPutKeys(m *pb.Mutation) ([]string, error) {
	envelope, err := decodeReceiptVertexPutMutation(m)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(envelope.Accepted))
	for i, item := range envelope.Accepted {
		keys[i] = item.Item.Key
	}
	return keys, nil
}

func decodeReceiptVertexPutMutation(m *pb.Mutation) (*vertexPutReceiptEnvelope, error) {
	if m == nil || m.GetOp() == nil {
		return nil, receiptVertexPutWALError("invalid wire envelope header")
	}
	if err := rejectReceiptWALUnknownFields(m.ProtoReflect()); err != nil {
		return nil, err
	}
	call := m.GetOp().GetReplicatedReceiptVertexPut()
	if call == nil || m.GetSeq() == 0 || len(m.GetOrigin()) != len(hlc.NodeID{}) ||
		m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 ||
		len(m.GetHlc().GetNodeId()) != len(hlc.NodeID{}) ||
		!bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) ||
		m.GetTombstoneExpiration() != nil ||
		len(call.GetItems()) == 0 || len(call.GetItems()) > receiptVertexWALMaxItems ||
		proto.Size(m) > receiptVertexWALMaxBytes ||
		len(call.GetDeploymentEpoch()) != len(mutationreceipt.Epoch{}) ||
		len(call.GetPolicyFingerprint()) != 32 {
		return nil, receiptVertexPutWALError("invalid wire envelope header")
	}
	e := &vertexPutReceiptEnvelope{
		OriginSeq: m.GetSeq(), HLC: hlcFromProto(m.GetHlc()), IfAbsent: call.GetIfAbsent(),
		Original: make([]*pb.Vertex, len(call.GetItems())),
		Receipts: make([]mutationreceipt.Receipt, len(call.GetItems())),
	}
	copy(e.Origin[:], m.GetOrigin())
	copy(e.Epoch[:], call.GetDeploymentEpoch())
	copy(e.PolicyFingerprint[:], call.GetPolicyFingerprint())
	for i, item := range call.GetItems() {
		if item == nil || item.GetOriginal() == nil || item.GetReceipt() == nil {
			return nil, receiptVertexPutWALError("invalid wire item %d", i)
		}
		wireReceipt := item.GetReceipt()
		if len(wireReceipt.GetOperationId()) != len(mutationreceipt.ID{}) ||
			len(wireReceipt.GetLogicalCallId()) != len(mutationreceipt.GroupID{}) ||
			len(wireReceipt.GetIntentSha256()) != 32 ||
			wireReceipt.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, receiptVertexPutWALError("invalid wire item %d metadata", i)
		}
		result, ok := wireReceipt.GetOriginalResult().GetResult().(*pb.ReceiptResult_PutVertexOutcome)
		if !ok || result.PutVertexOutcome < pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
			result.PutVertexOutcome > pb.PutOutcome_PUT_OUTCOME_SUPERSEDED {
			return nil, receiptVertexPutWALError("invalid wire item %d result", i)
		}
		original := proto.Clone(item.GetOriginal()).(*pb.Vertex)
		e.Original[i] = original
		receipt := &e.Receipts[i]
		copy(receipt.ID[:], wireReceipt.GetOperationId())
		copy(receipt.Group[:], wireReceipt.GetLogicalCallId())
		receipt.Index, receipt.Count, receipt.Kind = wireReceipt.GetItemIndex(),
			wireReceipt.GetItemCount(), mutationreceipt.PutVertex
		copy(receipt.Digest[:], wireReceipt.GetIntentSha256())
		receipt.DeadlineMillis = int64(wireReceipt.GetDeadlineUnixMs())
		receipt.Result = []byte{byte(result.PutVertexOutcome)}
		if item.GetAccepted() == nil {
			continue
		}
		if nilOneofWrapper(item.GetAccepted().GetOutcome()) {
			return nil, receiptVertexPutWALError("wire item %d has typed-nil accepted effect", i)
		}
		var accepted graphcache.IndexedVertexPut[string, *pb.Vertex]
		accepted.Index = i
		switch effect := item.GetAccepted().GetOutcome().(type) {
		case *pb.ReplicatedPutVertex_Live:
			if effect.Live == nil {
				return nil, receiptVertexPutWALError("wire item %d has nil live effect", i)
			}
			accepted.Outcome = graphcache.PutOutcomeAppliedAndLive
			accepted.Item = graphcache.VertexItem[string, *pb.Vertex]{
				Key: effect.Live.GetKey(), Value: proto.Clone(effect.Live).(*pb.Vertex),
				Expiration: prototime.Expiration(effect.Live.GetExpiration()),
			}
		case *pb.ReplicatedPutVertex_CausalBarrier:
			if effect.CausalBarrier == nil {
				return nil, receiptVertexPutWALError("wire item %d has nil barrier effect", i)
			}
			accepted.Outcome = graphcache.PutOutcomeExpired
			accepted.Item = graphcache.VertexItem[string, *pb.Vertex]{
				Key: effect.CausalBarrier.GetKey(), CausalBarrier: true,
			}
		default:
			return nil, receiptVertexPutWALError("wire item %d has empty accepted effect", i)
		}
		e.Accepted = append(e.Accepted, accepted)
	}
	e.Mutation = receiptVertexPutGraphMutation(e)
	if _, err := validateReceiptVertexPutWALEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

func encodeReceiptVertexPutWAL(op mutationlog.MutationOp) ([]byte, error) {
	envelope, ok := op.(*vertexPutReceiptEnvelope)
	if !ok {
		return nil, receiptVertexPutWALError("unexpected operation type %T", op)
	}
	mutation, err := envelope.ReplicationMutation()
	if err != nil {
		return nil, err
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(mutation)
	if err != nil {
		return nil, receiptVertexPutWALError("marshal: %v", err)
	}
	if len(raw) == 0 || len(raw) > receiptVertexWALMaxBytes {
		return nil, receiptVertexPutWALError("invalid encoded size %d", len(raw))
	}
	return raw, nil
}

func decodeReceiptVertexPutWAL(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) == 0 || len(raw) > receiptVertexWALMaxBytes {
		return nil, receiptVertexPutWALError("invalid payload size %d", len(raw))
	}
	itemCount, err := preflightReceiptVertexWAL(
		raw,
		receiptVertexPutMutationArm,
		"graph.v1.ReplicatedReceiptVertexPut",
	)
	if err != nil {
		return nil, receiptVertexPutWALError("preflight: %v", err)
	}
	var mutation pb.Mutation
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, &mutation); err != nil {
		return nil, receiptVertexPutWALError("unmarshal: %v", err)
	}
	envelope, err := decodeReceiptVertexPutMutation(&mutation)
	if err != nil {
		return nil, err
	}
	if len(envelope.Receipts) != itemCount {
		return nil, receiptVertexPutWALError("preflight item count drift")
	}
	canonical, err := encodeReceiptVertexPutWAL(envelope)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, receiptVertexPutWALError("payload is not canonically encoded")
	}
	return envelope, nil
}

func validateReceiptVertexPutWALEntry(entry mutationlog.Entry) error {
	if entry.Seq == 0 {
		return receiptVertexPutWALError("zero FileWAL local seq")
	}
	envelope, ok := entry.Op.(*vertexPutReceiptEnvelope)
	if !ok {
		return receiptVertexPutWALError("unexpected operation type %T", entry.Op)
	}
	if !entry.HLC.Equal(envelope.HLC) {
		return receiptVertexPutWALError("FileWAL HLC differs from envelope HLC")
	}
	_, err := validateReceiptVertexPutWALEnvelope(envelope)
	return err
}

func validateReceiptVertexPutWALEnvelope(e *vertexPutReceiptEnvelope) (int, error) {
	if e == nil || e.Origin == (hlc.NodeID{}) || e.OriginSeq == 0 ||
		e.HLC.WallNs <= 0 || e.HLC.NodeID != e.Origin ||
		e.Epoch == (mutationreceipt.Epoch{}) || e.PolicyFingerprint == ([32]byte{}) {
		return 0, receiptVertexPutWALError("invalid origin, HLC, epoch, or policy metadata")
	}
	count := len(e.Receipts)
	if count == 0 || count > receiptVertexWALMaxItems ||
		len(e.Original) != count || len(e.Accepted) > count {
		return 0, receiptVertexPutWALError("invalid request alignment or item count")
	}
	group := e.Receipts[0].Group
	if group == (mutationreceipt.GroupID{}) {
		return 0, receiptVertexPutWALError("zero logical-call ID")
	}
	seen := make(map[mutationreceipt.ID]struct{}, count)
	var retentionMS int64
	for i, receipt := range e.Receipts {
		digest, err := vertexPutDigest(e.Original[i], e.IfAbsent)
		if err != nil {
			return 0, receiptVertexPutWALError("invalid original item %d: %v", i, err)
		}
		if receipt.Group != group {
			return 0, receiptVertexPutWALError("logical-call ID drift at item %d", i)
		}
		if _, duplicate := seen[receipt.ID]; duplicate {
			return 0, receiptVertexPutWALError("duplicate operation ID at item %d", i)
		}
		seen[receipt.ID] = struct{}{}
		horizon, err := validateReceiptVertexRow(
			receipt, mutationreceipt.PutVertex, i, count, e.Epoch, digest,
		)
		if err != nil {
			return 0, receiptVertexPutWALError("%v", err)
		}
		if i != 0 && horizon != retentionMS {
			return 0, receiptVertexPutWALError("inconsistent retention at item %d", i)
		}
		retentionMS = horizon
		if len(receipt.Result) != 1 ||
			receipt.Result[0] < byte(pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE) ||
			receipt.Result[0] > byte(pb.PutOutcome_PUT_OUTCOME_SUPERSEDED) ||
			(!e.IfAbsent && receipt.Result[0] == byte(pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET)) ||
			(receipt.Result[0] == byte(pb.PutOutcome_PUT_OUTCOME_EXPIRED) &&
				e.Original[i].GetExpiration() == nil) {
			return 0, receiptVertexPutWALError("invalid result at item %d", i)
		}
	}
	previous := -1
	for i, accepted := range e.Accepted {
		if accepted.Index <= previous || accepted.Index < 0 || accepted.Index >= count ||
			accepted.Item.Key != e.Original[accepted.Index].GetKey() {
			return 0, receiptVertexPutWALError("accepted index or key drift at item %d", i)
		}
		previous = accepted.Index
		originalOutcome := pb.PutOutcome(e.Receipts[accepted.Index].Result[0])
		switch accepted.Outcome {
		case graphcache.PutOutcomeAppliedAndLive:
			acceptedDigest, err := vertexPutDigest(accepted.Item.Value, e.IfAbsent)
			if err != nil || originalOutcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE ||
				accepted.Item.CausalBarrier ||
				acceptedDigest != e.Receipts[accepted.Index].Digest ||
				!accepted.Item.Expiration.Equal(prototime.Expiration(e.Original[accepted.Index].GetExpiration())) {
				return 0, receiptVertexPutWALError("accepted live effect drift at item %d", i)
			}
		case graphcache.PutOutcomeExpired:
			if originalOutcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE &&
				originalOutcome != pb.PutOutcome_PUT_OUTCOME_EXPIRED {
				return 0, receiptVertexPutWALError("accepted barrier result drift at item %d", i)
			}
			if originalOutcome == pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE &&
				e.Original[accepted.Index].GetExpiration() == nil {
				return 0, receiptVertexPutWALError("permanent live result became a barrier at item %d", i)
			}
			if !accepted.Item.CausalBarrier || accepted.Item.Value != nil ||
				!accepted.Item.Expiration.IsZero() {
				return 0, receiptVertexPutWALError("accepted barrier effect drift at item %d", i)
			}
		default:
			return 0, receiptVertexPutWALError("accepted outcome drift at item %d", i)
		}
	}
	expectedGraph := receiptVertexPutGraphMutation(e)
	if !sameReceiptVertexPutGraphMutation(e.Mutation, expectedGraph) {
		return 0, receiptVertexPutWALError("graph projection drift")
	}
	size := proto.Size(receiptVertexPutReplicationMutation(e))
	if size == 0 {
		return 0, receiptVertexPutWALError("payload exceeds size limit")
	}
	if size > receiptVertexWALMaxBytes {
		return 0, fmt.Errorf("%w: %w (size %d)", errReceiptVertexPutWAL, errReceiptVertexPutWireCapacity, size)
	}
	return size, nil
}

func sameReceiptVertexPutGraphMutation(left, right *pb.Mutation) bool {
	options := proto.MarshalOptions{Deterministic: true}
	leftRaw, leftErr := options.Marshal(left)
	rightRaw, rightErr := options.Marshal(right)
	return leftErr == nil && rightErr == nil && bytes.Equal(leftRaw, rightRaw)
}

type receiptVertexWALPreflight struct {
	expectedArm  protoreflect.FieldNumber
	expectedCall protoreflect.FullName
	itemCount    int
}

func preflightReceiptVertexWAL(
	raw []byte,
	expectedArm protoreflect.FieldNumber,
	expectedCall protoreflect.FullName,
) (int, error) {
	scan := receiptVertexWALPreflight{
		expectedArm:  expectedArm,
		expectedCall: expectedCall,
	}
	if err := scan.message(raw, (&pb.Mutation{}).ProtoReflect().Descriptor(), 0); err != nil {
		return 0, err
	}
	if scan.itemCount == 0 {
		return 0, errors.New("receipt Vertex envelope has no items")
	}
	return scan.itemCount, nil
}

func (s *receiptVertexWALPreflight) message(
	raw []byte,
	descriptor protoreflect.MessageDescriptor,
	depth int,
) error {
	if depth > 32 {
		return errors.New("protobuf message nesting exceeds limit")
	}
	var oneofs map[protoreflect.FullName]struct{}
	var opCount, armCount int
	for len(raw) != 0 {
		number, wireType, tagBytes := protowire.ConsumeTag(raw)
		if tagBytes < 0 {
			return errors.New("malformed protobuf tag")
		}
		raw = raw[tagBytes:]
		field := descriptor.Fields().ByNumber(number)
		if field == nil {
			return fmt.Errorf("unknown protobuf field %d in %s", number, descriptor.FullName())
		}
		if !receiptVertexWireTypeAllowed(field, wireType) {
			return fmt.Errorf("protobuf field %s has wrong wire type", field.FullName())
		}
		if descriptor.FullName() == "graph.v1.Mutation" && number == 4 {
			opCount++
			if opCount != 1 {
				return errors.New("duplicate outer Mutation.op field")
			}
		}
		if descriptor.FullName() == "graph.v1.MutationOp" {
			armCount++
			if armCount != 1 || number != s.expectedArm {
				return errors.New("unexpected or duplicate receipt Vertex operation arm")
			}
		}
		if descriptor.FullName() == s.expectedCall && number == receiptVertexItemsField {
			s.itemCount++
			if s.itemCount > receiptVertexWALMaxItems {
				return fmt.Errorf("receipt Vertex item count exceeds %d", receiptVertexWALMaxItems)
			}
		}
		if oneof := field.ContainingOneof(); oneof != nil {
			if oneofs == nil {
				oneofs = make(map[protoreflect.FullName]struct{})
			}
			if _, duplicate := oneofs[oneof.FullName()]; duplicate {
				return fmt.Errorf("duplicate protobuf oneof arm in %s", descriptor.FullName())
			}
			oneofs[oneof.FullName()] = struct{}{}
		}
		if field.Kind() == protoreflect.MessageKind {
			value, valueBytes := protowire.ConsumeBytes(raw)
			if valueBytes < 0 {
				return fmt.Errorf("malformed protobuf message field %s", field.FullName())
			}
			if err := s.message(value, field.Message(), depth+1); err != nil {
				return err
			}
			raw = raw[valueBytes:]
			continue
		}
		valueBytes := protowire.ConsumeFieldValue(number, wireType, raw)
		if valueBytes < 0 {
			return fmt.Errorf("malformed protobuf field %s", field.FullName())
		}
		raw = raw[valueBytes:]
	}
	if descriptor.FullName() == "graph.v1.Mutation" && opCount != 1 {
		return errors.New("Mutation.op must occur exactly once")
	}
	if descriptor.FullName() == "graph.v1.MutationOp" && armCount != 1 {
		return errors.New("MutationOp must contain exactly one receipt Vertex arm")
	}
	return nil
}

func receiptVertexWireTypeAllowed(
	field protoreflect.FieldDescriptor,
	wireType protowire.Type,
) bool {
	if field.IsList() && field.IsPacked() && wireType == protowire.BytesType {
		return true
	}
	switch field.Kind() {
	case protoreflect.BoolKind,
		protoreflect.EnumKind,
		protoreflect.Int32Kind,
		protoreflect.Sint32Kind,
		protoreflect.Int64Kind,
		protoreflect.Sint64Kind,
		protoreflect.Uint32Kind,
		protoreflect.Uint64Kind:
		return wireType == protowire.VarintType
	case protoreflect.Fixed32Kind, protoreflect.Sfixed32Kind, protoreflect.FloatKind:
		return wireType == protowire.Fixed32Type
	case protoreflect.Fixed64Kind, protoreflect.Sfixed64Kind, protoreflect.DoubleKind:
		return wireType == protowire.Fixed64Type
	case protoreflect.StringKind, protoreflect.BytesKind, protoreflect.MessageKind:
		return wireType == protowire.BytesType
	default:
		return false
	}
}

func validateReceiptVertexRow(
	receipt mutationreceipt.Receipt,
	kind mutationreceipt.Kind,
	index, count int,
	epoch mutationreceipt.Epoch,
	digest [32]byte,
) (int64, error) {
	id, err := mutationreceipt.DecodeID(receipt.ID[:])
	if err != nil || id != receipt.ID || !bytes.Equal(receipt.ID[1:17], epoch[:]) {
		return 0, fmt.Errorf("invalid operation ID or epoch at item %d", index)
	}
	issued := binary.BigEndian.Uint64(receipt.ID[17:25])
	if issued > math.MaxInt64 || receipt.DeadlineMillis <= int64(issued) {
		return 0, fmt.Errorf("invalid receipt deadline at item %d", index)
	}
	horizon := receipt.DeadlineMillis - int64(issued)
	if horizon < int64(time.Hour/time.Millisecond) ||
		horizon > int64((30*24*time.Hour)/time.Millisecond) {
		return 0, fmt.Errorf("invalid retention at item %d", index)
	}
	if receipt.Group == (mutationreceipt.GroupID{}) ||
		receipt.Index != uint32(index) || receipt.Count != uint32(count) ||
		receipt.Kind != kind || receipt.HasContrib ||
		receipt.ContribID != (mutationreceipt.ContribID{}) || receipt.Digest != digest {
		return 0, fmt.Errorf("intent or index drift at item %d", index)
	}
	return horizon, nil
}
