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

// ReplicationMutation projects the private WAL envelope as a receipt-bearing
// MutationOp. It never falls back to the graph-only DeleteEdges projection:
// an older peer must reject the unknown oneof arm before advancing its origin
// watermark. The producer is not wired to a public write RPC yet.
func (e *edgeDeleteReceiptEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if _, err := validateReceiptEdgeDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	graph := e.Mutation.GetOp().GetDeleteEdges()
	expiration := timestamppb.New(e.TombstoneExpiration)
	if expiration.CheckValid() != nil {
		return nil, errors.New("receipt Edge Delete WAL envelope has invalid tombstone expiration")
	}
	accepted := make([]bool, len(e.OriginalKeys))
	if graph == nil || len(graph.GetEdges()) != len(e.Accepted) {
		return nil, errors.New("receipt Edge Delete WAL envelope has no graph projection")
	}
	for _, entry := range e.Accepted {
		accepted[entry.Index] = true
	}
	items := make([]*pb.ReplicatedReceiptEdgeDeleteItem, len(e.Receipts))
	group := e.Receipts[0].Group
	for i, receipt := range e.Receipts {
		key := e.OriginalKeys[i]
		items[i] = &pb.ReplicatedReceiptEdgeDeleteItem{
			Key: &pb.EdgeKey{Tail: key.Tail, Head: key.Head},
			Receipt: &pb.MutationReceipt{
				OperationId: receipt.ID.Bytes(), LogicalCallId: append([]byte(nil), group[:]...),
				ItemIndex: receipt.Index, ItemCount: receipt.Count,
				IntentSha256:   append([]byte(nil), receipt.Digest[:]...),
				DeadlineUnixMs: uint64(receipt.DeadlineMillis),
				OriginalResult: &pb.ReceiptResult{Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: receipt.Result[0] == 1}},
			},
			CausallyAccepted: accepted[i],
		}
	}
	wired := &pb.Mutation{
		Seq: e.OriginSeq, Origin: append([]byte(nil), e.Origin[:]...), Hlc: hlcToProto(e.HLC),
		Op: &pb.MutationOp{Op: &pb.MutationOp_ReplicatedReceiptEdgeDelete{
			ReplicatedReceiptEdgeDelete: &pb.ReplicatedReceiptEdgeDelete{
				DeploymentEpoch:     append([]byte(nil), e.Epoch[:]...),
				PolicyFingerprint:   append([]byte(nil), e.PolicyFingerprint[:]...),
				TombstoneExpiration: expiration, Items: items,
			},
		}},
	}
	if proto.Size(wired) > receiptEdgeDeleteWALMaxBytes {
		return nil, errors.New("receipt Edge Delete wire frame exceeds 8 MiB")
	}
	return wired, nil
}

// acceptedReceiptEdgeDeleteKeys validates the wire's ordered receipt/graph
// relationship before identity-only CDC can advance a cursor. It shares the
// WAL codec's structural and cross-field validation; remote installation
// will additionally bind the local policy, capacity, and atomic WAL commit.
func acceptedReceiptEdgeDeleteKeys(m *pb.Mutation) ([]*pb.EdgeKey, error) {
	call := m.GetOp().GetReplicatedReceiptEdgeDelete()
	if call == nil || m.GetSeq() == 0 || len(m.GetOrigin()) != 16 ||
		m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 || len(m.GetHlc().GetNodeId()) != 16 ||
		!bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) ||
		proto.Size(m) > receiptEdgeDeleteWALMaxBytes ||
		len(call.GetDeploymentEpoch()) != 16 || len(call.GetPolicyFingerprint()) != 32 ||
		call.GetTombstoneExpiration() == nil || call.GetTombstoneExpiration().CheckValid() != nil ||
		len(call.GetItems()) == 0 || len(call.GetItems()) > (receiptEdgeDeleteWALMaxBytes-receiptEdgeDeleteWALHeaderSize)/receiptEdgeDeleteWALItemSize {
		return nil, errors.New("invalid receipt Edge Delete wire envelope header")
	}
	e := &edgeDeleteReceiptEnvelope{
		OriginSeq: m.GetSeq(), HLC: hlcFromProto(m.GetHlc()),
		TombstoneExpiration: call.GetTombstoneExpiration().AsTime(),
		OriginalKeys:        make([]graphcache.EdgeKey[string], len(call.GetItems())),
		Receipts:            make([]mutationreceipt.Receipt, len(call.GetItems())),
	}
	copy(e.Origin[:], m.GetOrigin())
	copy(e.Epoch[:], call.GetDeploymentEpoch())
	copy(e.PolicyFingerprint[:], call.GetPolicyFingerprint())
	accepted := make([]*pb.EdgeKey, 0, len(call.GetItems()))
	for i, item := range call.GetItems() {
		if item == nil || item.GetKey() == nil || item.GetReceipt() == nil {
			return nil, fmt.Errorf("invalid receipt Edge Delete wire item %d", i)
		}
		wireReceipt := item.GetReceipt()
		if len(wireReceipt.GetOperationId()) != 49 || len(wireReceipt.GetLogicalCallId()) != 16 ||
			len(wireReceipt.GetIntentSha256()) != 32 || wireReceipt.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, fmt.Errorf("invalid receipt Edge Delete wire item %d metadata", i)
		}
		result, ok := wireReceipt.GetOriginalResult().GetResult().(*pb.ReceiptResult_DeleteEdgeExisted)
		if !ok {
			return nil, fmt.Errorf("invalid receipt Edge Delete wire item %d result", i)
		}
		key := graphcache.EdgeKey[string]{Tail: item.GetKey().GetTail(), Head: item.GetKey().GetHead()}
		e.OriginalKeys[i] = key
		receipt := &e.Receipts[i]
		copy(receipt.ID[:], wireReceipt.GetOperationId())
		copy(receipt.Group[:], wireReceipt.GetLogicalCallId())
		receipt.Index, receipt.Count, receipt.Kind = wireReceipt.GetItemIndex(), wireReceipt.GetItemCount(), mutationreceipt.DeleteEdge
		copy(receipt.Digest[:], wireReceipt.GetIntentSha256())
		receipt.DeadlineMillis = int64(wireReceipt.GetDeadlineUnixMs())
		if result.DeleteEdgeExisted {
			receipt.Result = []byte{1}
		} else {
			receipt.Result = []byte{0}
		}
		if item.GetCausallyAccepted() {
			e.Accepted = append(e.Accepted, graphcache.IndexedEdgeDelete[string]{Index: i, Key: key})
			accepted = append(accepted, item.GetKey())
		}
	}
	e.Mutation = receiptEdgeDeleteWALMutation(e)
	if _, err := validateReceiptEdgeDeleteWALEnvelope(e); err != nil {
		return nil, err
	}
	return accepted, nil
}
