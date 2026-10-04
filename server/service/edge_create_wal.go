package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"time"
	"unicode/utf8"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/edgeweight"
	"github.com/anaregdesign/lantern/server/internal/prototime"
	"google.golang.org/protobuf/proto"
)

// This envelope is local accepted-effect evidence. Its typed wire arm supports
// WAL/CDC, but peer ApplyMutation always rejects it until HA arbitration exists.
type edgeCreateEnvelope struct {
	Mutation *pb.Mutation
	Receipts []mutationreceipt.Receipt
}

func (e *edgeCreateEnvelope) GraphMutation() *pb.Mutation {
	if e == nil {
		return nil
	}
	return e.Mutation
}
func (e *edgeCreateEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if _, err := decodeEdgeCreateMutation(e.GraphMutation()); err != nil {
		return nil, err
	}
	return proto.Clone(e.Mutation).(*pb.Mutation), nil
}
func validCreateEdgeOutcome(outcome pb.CreateEdgeOutcome) bool {
	return outcome >= pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE && outcome <= pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EXPIRED
}
func edgeCreateDigest(edge *pb.Edge, namespace string) ([32]byte, error) {
	if edge == nil || edge.GetTail() == "" || edge.GetHead() == "" || !utf8.ValidString(edge.GetTail()) || !utf8.ValidString(edge.GetHead()) || len(edge.GetTail())+len(edge.GetHead()) > mutationreceipt.MaxResourceIdentityBytes || !edgeweight.IsFiniteSource(edge.GetWeight()) || edge.GetWeight() == 0 {
		return [32]byte{}, errors.New("Create requires bounded nonempty UTF-8 endpoints and a finite nonzero weight")
	}
	if err := rejectProtoUnknownFields(edge.ProtoReflect()); err != nil {
		return [32]byte{}, err
	}
	if err := validateDurableGraphExpiration("Edge Create", edge.GetExpiration()); err != nil {
		return [32]byte{}, err
	}
	tail, err := receiptLogicalKey(edge.GetTail(), []string{namespace})
	if err != nil {
		return [32]byte{}, err
	}
	head, err := receiptLogicalKey(edge.GetHead(), []string{namespace})
	if err != nil {
		return [32]byte{}, err
	}
	canonical := []byte{byte(mutationreceipt.CreateEdge)}
	canonical = appendReceiptCanonicalString(canonical, tail)
	canonical = appendReceiptCanonicalString(canonical, head)
	canonical = binary.BigEndian.AppendUint32(canonical, math.Float32bits(edge.GetWeight()))
	if edge.GetExpiration() == nil {
		canonical = append(canonical, 0)
	} else {
		canonical = append(canonical, 1)
		canonical = appendReceiptCanonicalTime(canonical, edge.GetExpiration().GetSeconds(), edge.GetExpiration().GetNanos())
	}
	return mutationreceipt.IntentDigest(canonical), nil
}
func newEdgeCreateEnvelope(format string, origin hlc.NodeID, seq uint64, ts hlc.Timestamp, original []*pb.Edge, outcomes []pb.CreateEdgeOutcome, receipts []mutationreceipt.Receipt, runtime *receiptServingRuntime) (*edgeCreateEnvelope, error) {
	if len(original) != len(outcomes) || len(receipts) != 0 && len(receipts) != len(original) {
		return nil, receiptWALUnionError("Create result alignment drift")
	}
	call := &pb.EdgeCreateEffect{Items: make([]*pb.EdgeCreateEffectItem, len(original))}
	if runtime != nil {
		fingerprint := runtime.store.PolicyFingerprint()
		call.DeploymentEpoch = append([]byte(nil), runtime.epoch[:]...)
		call.PolicyFingerprint = append([]byte(nil), fingerprint[:]...)
	}
	for i, edge := range original {
		call.Items[i] = &pb.EdgeCreateEffectItem{Original: proto.Clone(edge).(*pb.Edge), Outcome: outcomes[i]}
		if len(receipts) != 0 {
			status, err := receiptStatusProto(receipts[i].ID, mutationreceipt.Observation{Status: mutationreceipt.Confirmed, Receipt: receipts[i]})
			if err != nil {
				return nil, err
			}
			call.Items[i].Receipt = status.Receipt
		}
	}
	m := &pb.Mutation{NamespaceFormat: format, Origin: append([]byte(nil), origin[:]...), Seq: seq, Hlc: hlcToProto(ts), Op: &pb.MutationOp{Op: &pb.MutationOp_EdgeCreateEffect{EdgeCreateEffect: call}}}
	return decodeEdgeCreateMutation(m)
}
func decodeEdgeCreateMutation(m *pb.Mutation) (*edgeCreateEnvelope, error) {
	if err := validateReceiptWALGraph(m); err != nil {
		return nil, err
	}
	if err := validateDataFormat(m.GetNamespaceFormat()); err != nil {
		return nil, err
	}
	call := m.GetOp().GetEdgeCreateEffect()
	if call == nil || len(call.GetItems()) == 0 || len(call.GetItems()) > receiptVertexWALMaxItems || proto.Size(m) > receiptVertexWALMaxBytes || m.GetTombstoneExpiration() != nil {
		return nil, receiptWALUnionError("invalid Create accepted-effect header")
	}
	bearing := len(call.GetDeploymentEpoch()) != 0 || len(call.GetPolicyFingerprint()) != 0
	var epoch mutationreceipt.Epoch
	if bearing {
		if len(call.GetDeploymentEpoch()) != len(epoch) || len(call.GetPolicyFingerprint()) != 32 || bytes.Equal(call.GetDeploymentEpoch(), make([]byte, 16)) || bytes.Equal(call.GetPolicyFingerprint(), make([]byte, 32)) {
			return nil, receiptWALUnionError("invalid Create receipt epoch or policy")
		}
		copy(epoch[:], call.GetDeploymentEpoch())
	}
	e := &edgeCreateEnvelope{Mutation: proto.Clone(m).(*pb.Mutation)}
	seenEdges := map[graphcache.EdgeKey[string]]bool{}
	seenIDs := map[mutationreceipt.ID]bool{}
	var group mutationreceipt.GroupID
	var retention int64
	for i, item := range call.GetItems() {
		if item == nil || !validCreateEdgeOutcome(item.GetOutcome()) {
			return nil, receiptWALUnionError("invalid Create outcome at %d", i)
		}
		digest, err := edgeCreateDigest(item.GetOriginal(), m.GetNamespaceFormat())
		if err != nil {
			return nil, receiptWALUnionError("invalid Create item %d: %v", i, err)
		}
		edge := item.GetOriginal()
		if item.GetOutcome() == pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE {
			key := graphcache.EdgeKey[string]{Tail: edge.GetTail(), Head: edge.GetHead()}
			if seenEdges[key] {
				return nil, receiptWALUnionError("duplicate accepted Create identity")
			}
			seenEdges[key] = true
		}
		wire := item.GetReceipt()
		if !bearing {
			if wire != nil {
				return nil, receiptWALUnionError("graph Create carries partial receipt evidence")
			}
			continue
		}
		if wire == nil || len(wire.GetIntentSha256()) != 32 || wire.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, receiptWALUnionError("missing Create receipt evidence")
		}
		id, err := mutationreceipt.DecodeID(wire.GetOperationId())
		if err != nil || !bytes.Equal(id[1:17], epoch[:]) || seenIDs[id] {
			return nil, receiptWALUnionError("invalid or duplicate Create operation ID")
		}
		seenIDs[id] = true
		currentGroup, err := mutationreceipt.DecodeGroupID(wire.GetLogicalCallId())
		if err != nil || i != 0 && group != currentGroup {
			return nil, receiptWALUnionError("invalid Create logical call ID")
		}
		group = currentGroup
		issued := binary.BigEndian.Uint64(id[17:25])
		deadline := int64(wire.GetDeadlineUnixMs())
		if issued > math.MaxInt64 || deadline <= int64(issued) {
			return nil, receiptWALUnionError("invalid Create receipt deadline")
		}
		horizon := deadline - int64(issued)
		if horizon < int64(time.Hour/time.Millisecond) || horizon > int64(30*24*time.Hour/time.Millisecond) || i != 0 && retention != horizon {
			return nil, receiptWALUnionError("invalid Create receipt retention")
		}
		retention = horizon
		originalResult, ok := wire.GetOriginalResult().GetResult().(*pb.ReceiptResult_CreateEdgeOutcome)
		if !ok || originalResult.CreateEdgeOutcome != item.GetOutcome() || wire.GetItemIndex() != uint32(i) || wire.GetItemCount() != uint32(len(call.GetItems())) || !bytes.Equal(wire.GetIntentSha256(), digest[:]) {
			return nil, receiptWALUnionError("Create original result or intent drift")
		}
		resource, err := receiptResourceIdentity(m.GetNamespaceFormat(), edge.GetTail(), edge.GetHead())
		if err != nil {
			return nil, err
		}
		e.Receipts = append(e.Receipts, mutationreceipt.Receipt{Intent: mutationreceipt.Intent{ID: id, Group: group, Index: uint32(i), Count: uint32(len(call.GetItems())), Kind: mutationreceipt.CreateEdge, Digest: digest, Resource: resource}, Result: []byte{byte(item.GetOutcome())}, DeadlineMillis: deadline})
	}
	return e, nil
}
func encodeEdgeCreateWAL(e *edgeCreateEnvelope) ([]byte, error) {
	if _, err := decodeEdgeCreateMutation(e.GraphMutation()); err != nil {
		return nil, err
	}
	return encodeReceiptWALGraph(e.Mutation)
}
func decodeEdgeCreateWAL(body []byte) (*edgeCreateEnvelope, error) {
	m, err := decodeReceiptWALGraphWithReceiptAdd(body, true)
	if err != nil {
		return nil, err
	}
	return decodeEdgeCreateMutation(m)
}
func replayEdgeCreateEffect(graph *graphcache.GraphCache[string, *pb.Vertex], e *edgeCreateEnvelope) error {
	verified, err := decodeEdgeCreateMutation(e.GraphMutation())
	if err != nil {
		return err
	}
	items := []graphcache.EdgeItem[string]{}
	for _, item := range verified.Mutation.GetOp().GetEdgeCreateEffect().GetItems() {
		if item.GetOutcome() != pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE {
			continue
		}
		edge := item.GetOriginal()
		items = append(items, graphcache.EdgeItem[string]{Tail: edge.GetTail(), Head: edge.GetHead(), Weight: edge.GetWeight(), Expiration: prototime.Expiration(edge.GetExpiration())})
	}
	if len(items) == 0 {
		return nil
	}
	tx, err := graph.BeginEdgeCreateReplay(items, hlcFromProto(e.Mutation.GetHlc()))
	if err != nil {
		return fmt.Errorf("Create accepted effect replay: %w", err)
	}
	defer tx.Abort()
	tx.Commit()
	return nil
}
