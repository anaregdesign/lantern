package service

import (
	"bytes"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

var errReceiptVertexDeleteWAL = errors.New("service: invalid receipt Vertex Delete WAL payload")

func receiptVertexDeleteWALError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", errReceiptVertexDeleteWAL, fmt.Sprintf(format, args...))
}

func (e *vertexDeleteReceiptEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if _, err := validateReceiptVertexDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	return receiptVertexDeleteReplicationMutation(e), nil
}

func receiptVertexDeleteReplicationMutation(e *vertexDeleteReceiptEnvelope) *pb.Mutation {
	accepted := make([]bool, len(e.OriginalKeys))
	for _, item := range e.Accepted {
		accepted[item.Index] = true
	}
	items := make([]*pb.ReplicatedReceiptVertexDeleteItem, len(e.Receipts))
	group := e.Receipts[0].Group
	for i, receipt := range e.Receipts {
		items[i] = &pb.ReplicatedReceiptVertexDeleteItem{
			Key: e.OriginalKeys[i],
			Receipt: &pb.MutationReceipt{
				OperationId:   append([]byte(nil), receipt.ID[:]...),
				LogicalCallId: append([]byte(nil), group[:]...),
				ItemIndex:     receipt.Index, ItemCount: receipt.Count,
				IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
				DeadlineUnixMs: uint64(receipt.DeadlineMillis),
				OriginalResult: &pb.ReceiptResult{
					Result: &pb.ReceiptResult_DeleteVertexExisted{
						DeleteVertexExisted: receipt.Result[0] == 1,
					},
				},
			},
			CausallyAccepted: accepted[i],
		}
	}
	return &pb.Mutation{
		Seq: e.OriginSeq, Origin: append([]byte(nil), e.Origin[:]...), Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptVertexDelete{
			ReplicatedReceiptVertexDelete: &pb.ReplicatedReceiptVertexDelete{
				DeploymentEpoch:     append([]byte(nil), e.Epoch[:]...),
				PolicyFingerprint:   append([]byte(nil), e.PolicyFingerprint[:]...),
				TombstoneExpiration: timestamppb.New(e.TombstoneExpiration),
				Items:               items,
			},
		}},
	}
}

func acceptedReceiptVertexDeleteKeys(m *pb.Mutation) ([]string, error) {
	envelope, err := decodeReceiptVertexDeleteMutation(m)
	if err != nil {
		return nil, err
	}
	keys := make([]string, len(envelope.Accepted))
	for i, item := range envelope.Accepted {
		keys[i] = item.Key
	}
	return keys, nil
}

func decodeReceiptVertexDeleteMutation(m *pb.Mutation) (*vertexDeleteReceiptEnvelope, error) {
	if m == nil || m.GetOp() == nil {
		return nil, receiptVertexDeleteWALError("invalid wire envelope header")
	}
	if err := rejectReceiptWALUnknownFields(m.ProtoReflect()); err != nil {
		return nil, err
	}
	call := m.GetOp().GetReplicatedReceiptVertexDelete()
	if call == nil || m.GetSeq() == 0 || len(m.GetOrigin()) != len(hlc.NodeID{}) ||
		m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 ||
		len(m.GetHlc().GetNodeId()) != len(hlc.NodeID{}) ||
		!bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) ||
		m.GetTombstoneExpiration() != nil ||
		len(call.GetItems()) == 0 || len(call.GetItems()) > receiptVertexWALMaxItems ||
		proto.Size(m) > receiptVertexWALMaxBytes ||
		len(call.GetDeploymentEpoch()) != len(mutationreceipt.Epoch{}) ||
		len(call.GetPolicyFingerprint()) != 32 ||
		call.GetTombstoneExpiration() == nil ||
		call.GetTombstoneExpiration().CheckValid() != nil {
		return nil, receiptVertexDeleteWALError("invalid wire envelope header")
	}
	e := &vertexDeleteReceiptEnvelope{
		OriginSeq: m.GetSeq(), HLC: hlcFromProto(m.GetHlc()),
		TombstoneExpiration: call.GetTombstoneExpiration().AsTime(),
		OriginalKeys:        make([]string, len(call.GetItems())),
		Receipts:            make([]mutationreceipt.Receipt, len(call.GetItems())),
	}
	copy(e.Origin[:], m.GetOrigin())
	copy(e.Epoch[:], call.GetDeploymentEpoch())
	copy(e.PolicyFingerprint[:], call.GetPolicyFingerprint())
	for i, item := range call.GetItems() {
		if item == nil || item.GetReceipt() == nil {
			return nil, receiptVertexDeleteWALError("invalid wire item %d", i)
		}
		wireReceipt := item.GetReceipt()
		if len(wireReceipt.GetOperationId()) != len(mutationreceipt.ID{}) ||
			len(wireReceipt.GetLogicalCallId()) != len(mutationreceipt.GroupID{}) ||
			len(wireReceipt.GetIntentSha256()) != 32 ||
			wireReceipt.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, receiptVertexDeleteWALError("invalid wire item %d metadata", i)
		}
		result, ok := wireReceipt.GetOriginalResult().GetResult().(*pb.ReceiptResult_DeleteVertexExisted)
		if !ok {
			return nil, receiptVertexDeleteWALError("invalid wire item %d result", i)
		}
		e.OriginalKeys[i] = item.GetKey()
		receipt := &e.Receipts[i]
		copy(receipt.ID[:], wireReceipt.GetOperationId())
		copy(receipt.Group[:], wireReceipt.GetLogicalCallId())
		receipt.Index, receipt.Count, receipt.Kind = wireReceipt.GetItemIndex(),
			wireReceipt.GetItemCount(), mutationreceipt.DeleteVertex
		copy(receipt.Digest[:], wireReceipt.GetIntentSha256())
		receipt.DeadlineMillis = int64(wireReceipt.GetDeadlineUnixMs())
		if result.DeleteVertexExisted {
			receipt.Result = []byte{1}
		} else {
			receipt.Result = []byte{0}
		}
		if item.GetCausallyAccepted() {
			e.Accepted = append(e.Accepted,
				graphcache.IndexedVertexDelete[string]{Index: i, Key: item.GetKey()})
		}
	}
	e.Mutation = receiptVertexDeleteGraphMutation(e)
	if _, err := validateReceiptVertexDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

func encodeReceiptVertexDeleteWAL(op mutationlog.MutationOp) ([]byte, error) {
	envelope, ok := op.(*vertexDeleteReceiptEnvelope)
	if !ok {
		return nil, receiptVertexDeleteWALError("unexpected operation type %T", op)
	}
	mutation, err := envelope.ReplicationMutation()
	if err != nil {
		return nil, err
	}
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(mutation)
	if err != nil {
		return nil, receiptVertexDeleteWALError("marshal: %v", err)
	}
	if len(raw) == 0 || len(raw) > receiptVertexWALMaxBytes {
		return nil, receiptVertexDeleteWALError("invalid encoded size %d", len(raw))
	}
	return raw, nil
}

func decodeReceiptVertexDeleteWAL(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) == 0 || len(raw) > receiptVertexWALMaxBytes {
		return nil, receiptVertexDeleteWALError("invalid payload size %d", len(raw))
	}
	itemCount, err := preflightReceiptVertexWAL(
		raw,
		receiptVertexDeleteMutationArm,
		"graph.v1.ReplicatedReceiptVertexDelete",
	)
	if err != nil {
		return nil, receiptVertexDeleteWALError("preflight: %v", err)
	}
	var mutation pb.Mutation
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, &mutation); err != nil {
		return nil, receiptVertexDeleteWALError("unmarshal: %v", err)
	}
	envelope, err := decodeReceiptVertexDeleteMutation(&mutation)
	if err != nil {
		return nil, err
	}
	if len(envelope.Receipts) != itemCount {
		return nil, receiptVertexDeleteWALError("preflight item count drift")
	}
	canonical, err := encodeReceiptVertexDeleteWAL(envelope)
	if err != nil {
		return nil, err
	}
	if !bytes.Equal(raw, canonical) {
		return nil, receiptVertexDeleteWALError("payload is not canonically encoded")
	}
	return envelope, nil
}

func validateReceiptVertexDeleteWALEntry(entry mutationlog.Entry) error {
	if entry.Seq == 0 {
		return receiptVertexDeleteWALError("zero FileWAL local seq")
	}
	envelope, ok := entry.Op.(*vertexDeleteReceiptEnvelope)
	if !ok {
		return receiptVertexDeleteWALError("unexpected operation type %T", entry.Op)
	}
	if !entry.HLC.Equal(envelope.HLC) {
		return receiptVertexDeleteWALError("FileWAL HLC differs from envelope HLC")
	}
	_, err := validateReceiptVertexDeleteWALEnvelope(envelope)
	return err
}

func validateReceiptVertexDeleteWALEnvelope(e *vertexDeleteReceiptEnvelope) (int, error) {
	if e == nil || e.Origin == (hlc.NodeID{}) || e.OriginSeq == 0 ||
		e.HLC.WallNs <= 0 || e.HLC.NodeID != e.Origin ||
		e.Epoch == (mutationreceipt.Epoch{}) || e.PolicyFingerprint == ([32]byte{}) {
		return 0, receiptVertexDeleteWALError("invalid origin, HLC, epoch, or policy metadata")
	}
	expirationNS := e.TombstoneExpiration.UnixNano()
	if e.TombstoneExpiration.IsZero() || expirationNS <= 0 ||
		!time.Unix(0, expirationNS).Equal(e.TombstoneExpiration) {
		return 0, receiptVertexDeleteWALError("invalid tombstone expiration")
	}
	count := len(e.Receipts)
	if count == 0 || count > receiptVertexWALMaxItems ||
		len(e.OriginalKeys) != count || len(e.Accepted) > count {
		return 0, receiptVertexDeleteWALError("invalid request alignment or item count")
	}
	group := e.Receipts[0].Group
	if group == (mutationreceipt.GroupID{}) {
		return 0, receiptVertexDeleteWALError("zero logical-call ID")
	}
	seen := make(map[mutationreceipt.ID]struct{}, count)
	var retentionMS int64
	for i, receipt := range e.Receipts {
		key := e.OriginalKeys[i]
		if key == "" || !utf8.ValidString(key) || len(key) > receiptVertexWALMaxBytes {
			return 0, receiptVertexDeleteWALError("invalid key at item %d", i)
		}
		if receipt.Group != group {
			return 0, receiptVertexDeleteWALError("logical-call ID drift at item %d", i)
		}
		if _, duplicate := seen[receipt.ID]; duplicate {
			return 0, receiptVertexDeleteWALError("duplicate operation ID at item %d", i)
		}
		seen[receipt.ID] = struct{}{}
		horizon, err := validateReceiptVertexRow(
			receipt, mutationreceipt.DeleteVertex, i, count, e.Epoch, vertexDeleteDigest(key),
		)
		if err != nil {
			return 0, receiptVertexDeleteWALError("%v", err)
		}
		if i != 0 && horizon != retentionMS {
			return 0, receiptVertexDeleteWALError("inconsistent retention at item %d", i)
		}
		retentionMS = horizon
		if len(receipt.Result) != 1 || receipt.Result[0] > 1 {
			return 0, receiptVertexDeleteWALError("invalid result at item %d", i)
		}
	}
	previous := -1
	for i, accepted := range e.Accepted {
		if accepted.Index <= previous || accepted.Index < 0 || accepted.Index >= count ||
			accepted.Key != e.OriginalKeys[accepted.Index] {
			return 0, receiptVertexDeleteWALError("accepted index or key drift at item %d", i)
		}
		previous = accepted.Index
	}
	if !proto.Equal(e.Mutation, receiptVertexDeleteGraphMutation(e)) {
		return 0, receiptVertexDeleteWALError("graph projection drift")
	}
	size := proto.Size(receiptVertexDeleteReplicationMutation(e))
	if size == 0 || size > receiptVertexWALMaxBytes {
		return 0, receiptVertexDeleteWALError("payload exceeds size limit")
	}
	return size, nil
}
