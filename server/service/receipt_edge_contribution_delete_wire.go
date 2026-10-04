package service

import (
	"bytes"
	"errors"
	"fmt"
	"math"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

var errReceiptEdgeContributionDeleteWireCapacity = errors.New(
	"receipt contribution Delete wire frame exceeds 8 MiB",
)

// A receipt-bearing arm is mandatory: a peer that does not understand this
// family cannot advance its origin frontier using a graph-only projection.
func (e *edgeContributionDeleteReceiptEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if _, err := validateReceiptEdgeContributionDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	graph := e.Mutation.GetOp().GetDeleteEdgeContributions()
	if graph == nil || len(graph.GetContributions()) != len(e.Accepted) {
		return nil, errors.New("receipt contribution Delete WAL envelope has no graph projection")
	}
	expiration := timestamppb.New(e.TombstoneExpiration)
	if expiration.CheckValid() != nil {
		return nil, errors.New("receipt contribution Delete WAL envelope has invalid tombstone expiration")
	}
	accepted := make([]bool, len(e.OriginalKeys))
	for _, entry := range e.Accepted {
		accepted[entry.Index] = true
	}
	items := make([]*pb.ReplicatedReceiptEdgeContributionDeleteItem, len(e.Receipts))
	group := e.Receipts[0].Group
	for i, receipt := range e.Receipts {
		items[i] = &pb.ReplicatedReceiptEdgeContributionDeleteItem{
			Key: receiptEdgeContributionDeleteWireKey(e.OriginalKeys[i]),
			Receipt: &pb.MutationReceipt{
				OperationId: receipt.ID.Bytes(), LogicalCallId: append([]byte(nil), group[:]...),
				ItemIndex: receipt.Index, ItemCount: receipt.Count,
				IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
				DeadlineUnixMs: uint64(receipt.DeadlineMillis),
				OriginalResult: &pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeContributionExisted{
					DeleteEdgeContributionExisted: receipt.Result[0] == 1,
				}},
			},
			CausallyAccepted: accepted[i],
		}
	}
	wired := &pb.Mutation{
		NamespaceFormat: e.NamespaceFormat,
		Origin:          append([]byte(nil), e.Origin[:]...), Seq: e.OriginSeq, Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeContributionDelete{
			ReplicatedReceiptEdgeContributionDelete: &pb.ReplicatedReceiptEdgeContributionDelete{
				DeploymentEpoch:     append([]byte(nil), e.Epoch[:]...),
				PolicyFingerprint:   append([]byte(nil), e.PolicyFingerprint[:]...),
				TombstoneExpiration: expiration,
				Items:               items,
			},
		}},
	}
	if proto.Size(wired) > receiptEdgeContributionDeleteWALMaxBytes {
		return nil, errReceiptEdgeContributionDeleteWireCapacity
	}
	return wired, nil
}

func acceptedReceiptEdgeContributionDeleteKeys(m *pb.Mutation) ([]*pb.EdgeKey, error) {
	envelope, err := decodeReceiptEdgeContributionDeleteMutation(m)
	if err != nil {
		return nil, err
	}
	accepted := make([]*pb.EdgeKey, len(envelope.Accepted))
	for i, item := range envelope.Accepted {
		accepted[i] = &pb.EdgeKey{Tail: item.Key.Tail, Head: item.Key.Head}
	}
	return accepted, nil
}

func decodeReceiptEdgeContributionDeleteMutation(
	m *pb.Mutation,
) (*edgeContributionDeleteReceiptEnvelope, error) {
	if m == nil || m.GetOp() == nil {
		return nil, errors.New("invalid receipt contribution Delete wire envelope header")
	}
	if err := rejectReceiptWALUnknownFields(m.ProtoReflect()); err != nil {
		return nil, err
	}
	call := m.GetOp().GetReplicatedReceiptEdgeContributionDelete()
	if call == nil || m.GetSeq() == 0 || len(m.GetOrigin()) != 16 ||
		m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 ||
		len(m.GetHlc().GetNodeId()) != 16 ||
		!bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) ||
		m.GetTombstoneExpiration() != nil ||
		proto.Size(m) > receiptEdgeContributionDeleteWALMaxBytes ||
		len(call.GetDeploymentEpoch()) != 16 ||
		len(call.GetPolicyFingerprint()) != 32 ||
		call.GetTombstoneExpiration() == nil ||
		call.GetTombstoneExpiration().CheckValid() != nil ||
		len(call.GetItems()) == 0 ||
		len(call.GetItems()) > (receiptEdgeContributionDeleteWALMaxBytes-receiptEdgeContributionDeleteWALHeaderSize)/
			receiptEdgeContributionDeleteWALItemSize {
		return nil, errors.New("invalid receipt contribution Delete wire envelope header")
	}
	e := &edgeContributionDeleteReceiptEnvelope{
		NamespaceFormat: m.GetNamespaceFormat(),
		OriginSeq:       m.GetSeq(), HLC: hlcFromProto(m.GetHlc()),
		TombstoneExpiration: call.GetTombstoneExpiration().AsTime(),
		OriginalKeys:        make([]graphcache.EdgeContributionKey[string], len(call.GetItems())),
		Receipts:            make([]mutationreceipt.Receipt, len(call.GetItems())),
	}
	copy(e.Origin[:], m.GetOrigin())
	copy(e.Epoch[:], call.GetDeploymentEpoch())
	copy(e.PolicyFingerprint[:], call.GetPolicyFingerprint())
	for i, item := range call.GetItems() {
		if item == nil || item.GetKey() == nil || item.GetReceipt() == nil ||
			len(item.GetKey().GetContribId()) != len(graphcache.ContribID{}) {
			return nil, fmt.Errorf("invalid receipt contribution Delete wire item %d", i)
		}
		wireReceipt := item.GetReceipt()
		if len(wireReceipt.GetOperationId()) != len(mutationreceipt.ID{}) ||
			len(wireReceipt.GetLogicalCallId()) != len(mutationreceipt.GroupID{}) ||
			len(wireReceipt.GetIntentSha256()) != 32 ||
			wireReceipt.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, fmt.Errorf("invalid receipt contribution Delete wire item %d metadata", i)
		}
		result, ok := wireReceipt.GetOriginalResult().GetResult().(*pb.ReceiptResult_DeleteEdgeContributionExisted)
		if !ok {
			return nil, fmt.Errorf("invalid receipt contribution Delete wire item %d result", i)
		}
		key := graphcache.EdgeContributionKey[string]{
			Tail: item.GetKey().GetTail(), Head: item.GetKey().GetHead(),
		}
		copy(key.ContribID[:], item.GetKey().GetContribId())
		e.OriginalKeys[i] = key
		receipt := &e.Receipts[i]
		resource, resourceErr := receiptResourceIdentity(e.NamespaceFormat, key.Tail, key.Head)
		if resourceErr != nil {
			return nil, resourceErr
		}
		receipt.Resource = resource
		copy(receipt.ID[:], wireReceipt.GetOperationId())
		copy(receipt.Group[:], wireReceipt.GetLogicalCallId())
		receipt.Index, receipt.Count, receipt.Kind = wireReceipt.GetItemIndex(), wireReceipt.GetItemCount(),
			mutationreceipt.DeleteEdgeContribution
		copy(receipt.Digest[:], wireReceipt.GetIntentSha256())
		receipt.DeadlineMillis = int64(wireReceipt.GetDeadlineUnixMs())
		if result.DeleteEdgeContributionExisted {
			receipt.Result = []byte{1}
		} else {
			receipt.Result = []byte{0}
		}
		if item.GetCausallyAccepted() {
			e.Accepted = append(e.Accepted,
				graphcache.IndexedEdgeContributionDelete[string]{Index: i, Key: key})
		}
	}
	e.Mutation = receiptEdgeContributionDeleteWALMutation(e)
	if _, err := validateReceiptEdgeContributionDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	return e, nil
}

// Accepted bits are receiver-local; equality binds the sender's immutable
// original result and full target identity, not its causal projection.
func sameReceiptEdgeContributionDeleteIntent(
	a, b *edgeContributionDeleteReceiptEnvelope,
) bool {
	if a == nil || b == nil || a.Origin != b.Origin || a.OriginSeq != b.OriginSeq ||
		a.HLC != b.HLC || a.Epoch != b.Epoch || a.PolicyFingerprint != b.PolicyFingerprint ||
		!a.TombstoneExpiration.Equal(b.TombstoneExpiration) ||
		len(a.OriginalKeys) != len(b.OriginalKeys) || len(a.Receipts) != len(b.Receipts) {
		return false
	}
	for i := range a.OriginalKeys {
		if a.OriginalKeys[i] != b.OriginalKeys[i] {
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
