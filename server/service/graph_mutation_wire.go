package service

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/mutationlog"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/prototime"
	"google.golang.org/protobuf/encoding/protowire"
	"google.golang.org/protobuf/proto"
)

const graphMutationMaxBytes = 1 << 20

var errGraphMutation = errors.New("service: invalid mixed graph mutation")

type graphMutationEnvelope struct {
	Mutation *pb.Mutation
	Receipts []mutationreceipt.Receipt
}

func (e *graphMutationEnvelope) GraphMutation() *pb.Mutation { return e.Mutation }
func (e *graphMutationEnvelope) ReplicationMutation() (*pb.Mutation, error) {
	if _, err := decodeGraphMutation(e.Mutation); err != nil {
		return nil, err
	}
	return proto.Clone(e.Mutation).(*pb.Mutation), nil
}
func (e *graphMutationEnvelope) replicationFrameSize() (int, error) {
	if _, err := decodeGraphMutation(e.Mutation); err != nil {
		return 0, err
	}
	// One length-delimited SubscribeResponse.mutation with a bounded varint.
	return protowire.SizeTag(1) + protowire.SizeBytes(proto.Size(e.Mutation)), nil
}

func graphMutationDigest(item *pb.GraphMutationItem, namespace string) ([32]byte, mutationreceipt.Kind, error) {
	if item == nil || nilOneofWrapper(item.GetOperation()) || rejectProtoUnknownFields(item.ProtoReflect()) != nil {
		return [32]byte{}, 0, errGraphMutation
	}
	var digest [32]byte
	var kind mutationreceipt.Kind
	var err error
	switch operation := item.GetOperation().(type) {
	case *pb.GraphMutationItem_PutVertex:
		kind = mutationreceipt.PutVertex
		digest, err = vertexPutDigest(operation.PutVertex, false, namespace)
	case *pb.GraphMutationItem_AddEdge:
		kind = mutationreceipt.AddEdge
		add := operation.AddEdge
		if add == nil || len(add.GetContribId()) != len(graphcache.ContribID{}) {
			return digest, kind, errGraphMutation
		}
		var id graphcache.ContribID
		copy(id[:], add.GetContribId())
		digest, err = receiptEdgeAddEffectDigest(add.GetEdge(), id, true, namespace)
	default:
		return digest, 0, errGraphMutation
	}
	if err != nil {
		return digest, kind, err
	}
	return mutationreceipt.IntentDigest(append([]byte("lantern/mixed-graph-item/v1\x00"), digest[:]...)), kind, nil
}

func validateGraphMutationItems(items []*pb.GraphMutationItem, namespace string) (int, error) {
	if len(items) < 2 || len(items) > graphcache.GraphMutationMaxItems {
		return 0, errGraphMutation
	}
	vertices := 0
	keys := make(map[string]bool, len(items))
	ids := make(map[graphcache.ContribID]bool, len(items))
	for i, item := range items {
		_, kind, err := graphMutationDigest(item, namespace)
		if err != nil {
			return 0, err
		}
		if kind == mutationreceipt.PutVertex {
			key := item.GetPutVertex().GetKey()
			if vertices != i || keys[key] {
				return 0, errGraphMutation
			}
			keys[key] = true
			vertices++
		} else {
			var id graphcache.ContribID
			copy(id[:], item.GetAddEdge().GetContribId())
			if ids[id] {
				return 0, errGraphMutation
			}
			ids[id] = true
		}
	}
	if vertices == 0 || vertices == len(items) {
		return 0, errGraphMutation
	}
	return vertices, nil
}

func graphMutationResource(item *pb.GraphMutationItem, namespace string) (mutationreceipt.ResourceIdentity, error) {
	if v := item.GetPutVertex(); v != nil {
		return receiptResourceIdentity(namespace, v.GetKey(), "")
	}
	edge := item.GetAddEdge().GetEdge()
	return receiptResourceIdentity(namespace, edge.GetTail(), edge.GetHead())
}

func decodeGraphMutation(m *pb.Mutation) (*graphMutationEnvelope, error) {
	if m == nil || m.GetOp() == nil || nilOneofWrapper(m.GetOp().GetOp()) || m.GetOp().GetNoEndpointCreation() || m.GetTombstoneExpiration() != nil ||
		rejectProtoUnknownFields(m.ProtoReflect()) != nil || validateDataFormat(m.GetNamespaceFormat()) != nil ||
		proto.Size(m) > graphMutationMaxBytes-receiptWALUnionHeaderSize || m.GetSeq() == 0 || len(m.GetOrigin()) != 16 ||
		m.GetHlc() == nil || m.GetHlc().GetWallNs() <= 0 || !bytes.Equal(m.GetOrigin(), m.GetHlc().GetNodeId()) {
		return nil, errGraphMutation
	}
	call := m.GetOp().GetGraphMutation()
	if call == nil || call.GetVersion() != 1 || len(call.GetDeploymentEpoch()) != 16 || len(call.GetPolicyFingerprint()) != 32 ||
		len(call.GetItems()) < 2 || len(call.GetItems()) > graphcache.GraphMutationMaxItems {
		return nil, errGraphMutation
	}
	var epoch mutationreceipt.Epoch
	copy(epoch[:], call.GetDeploymentEpoch())
	var origin hlc.NodeID
	copy(origin[:], m.GetOrigin())
	var policy [32]byte
	copy(policy[:], call.GetPolicyFingerprint())
	if epoch == (mutationreceipt.Epoch{}) || origin == (hlc.NodeID{}) || policy == ([32]byte{}) {
		return nil, errGraphMutation
	}
	original := make([]*pb.GraphMutationItem, len(call.GetItems()))
	for i, item := range call.GetItems() {
		if item == nil {
			return nil, errGraphMutation
		}
		original[i] = item.GetOriginal()
	}
	vertexCount, err := validateGraphMutationItems(original, m.GetNamespaceFormat())
	if err != nil {
		return nil, err
	}
	e := &graphMutationEnvelope{Mutation: m, Receipts: make([]mutationreceipt.Receipt, len(original))}
	var group mutationreceipt.GroupID
	seen := make(map[mutationreceipt.ID]bool, len(original))
	var retention int64
	for i, item := range call.GetItems() {
		wire := item.GetReceipt()
		if wire == nil || len(wire.GetOperationId()) != 49 || len(wire.GetLogicalCallId()) != 16 || len(wire.GetIntentSha256()) != 32 || wire.GetDeadlineUnixMs() > math.MaxInt64 {
			return nil, errGraphMutation
		}
		r := &e.Receipts[i]
		copy(r.ID[:], wire.GetOperationId())
		copy(r.Group[:], wire.GetLogicalCallId())
		copy(r.Digest[:], wire.GetIntentSha256())
		r.Index, r.Count, r.DeadlineMillis = wire.GetItemIndex(), wire.GetItemCount(), int64(wire.GetDeadlineUnixMs())
		digest, kind, err := graphMutationDigest(original[i], m.GetNamespaceFormat())
		if err != nil {
			return nil, err
		}
		r.Kind = kind
		r.Resource, err = graphMutationResource(original[i], m.GetNamespaceFormat())
		if err != nil {
			return nil, err
		}
		r.LifecycleReduction = item.GetLifecycleReduced()
		if i == 0 {
			group = r.Group
		}
		if r.Group != group || seen[r.ID] {
			return nil, errGraphMutation
		}
		seen[r.ID] = true
		// The shared row validator handles ID/epoch/group/index/digest/deadline.
		horizon, err := validateReceiptVertexRow(*r, kind, i, len(original), epoch, digest)
		if err != nil || (i > 0 && horizon != retention) {
			return nil, errGraphMutation
		}
		retention = horizon
		if i < vertexCount {
			result, ok := wire.GetOriginalResult().GetResult().(*pb.ReceiptResult_PutVertexOutcome)
			if !ok || result == nil || item.GetEdgeAccepted() {
				return nil, errGraphMutation
			}
			switch result.PutVertexOutcome {
			case pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE, pb.PutOutcome_PUT_OUTCOME_SUPERSEDED:
			case pb.PutOutcome_PUT_OUTCOME_EXPIRED:
				if original[i].GetPutVertex().GetExpiration() == nil {
					return nil, errGraphMutation
				}
			default:
				return nil, errGraphMutation
			}
			if r.LifecycleReduction && (m.GetNamespaceFormat() == "" || result.PutVertexOutcome == pb.PutOutcome_PUT_OUTCOME_SUPERSEDED) {
				return nil, errGraphMutation
			}
			if m.GetNamespaceFormat() != "" && result.PutVertexOutcome == pb.PutOutcome_PUT_OUTCOME_EXPIRED && !r.LifecycleReduction {
				return nil, errGraphMutation
			}
			r.Result = []byte{byte(result.PutVertexOutcome)}
		} else {
			result, ok := wire.GetOriginalResult().GetResult().(*pb.ReceiptResult_AddEdgeEffectiveWeight)
			if !ok || result == nil || r.LifecycleReduction {
				return nil, errGraphMutation
			}
			r.Result = binary.BigEndian.AppendUint32(nil, math.Float32bits(result.AddEdgeEffectiveWeight))
			r.HasContrib = true
			copy(r.ContribID[:], original[i].GetAddEdge().GetContribId())
		}
	}
	previous := -1
	for _, effect := range call.GetProjection() {
		if effect == nil || nilOneofWrapper(effect.GetEffect()) {
			return nil, errGraphMutation
		}
		i := int(effect.GetItemIndex())
		if i <= previous || i >= len(original) {
			return nil, errGraphMutation
		}
		previous = i
		switch applied := effect.GetEffect().(type) {
		case *pb.GraphMutationProjection_PutVertex:
			if i >= vertexCount || applied.PutVertex == nil || nilOneofWrapper(applied.PutVertex.GetOutcome()) {
				return nil, errGraphMutation
			}
			outcome := pb.PutOutcome(e.Receipts[i].Result[0])
			if outcome == pb.PutOutcome_PUT_OUTCOME_SUPERSEDED {
				return nil, errGraphMutation
			}
			switch v := applied.PutVertex.GetOutcome().(type) {
			case *pb.ReplicatedPutVertex_Live:
				if outcome != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE || !proto.Equal(v.Live, original[i].GetPutVertex()) {
					return nil, errGraphMutation
				}
			case *pb.ReplicatedPutVertex_CausalBarrier:
				if v.CausalBarrier == nil || v.CausalBarrier.GetKey() != original[i].GetPutVertex().GetKey() || (outcome == pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE && original[i].GetPutVertex().GetExpiration() == nil) {
					return nil, errGraphMutation
				}
			default:
				return nil, errGraphMutation
			}
		case *pb.GraphMutationProjection_AddEdge:
			if i < vertexCount || !applied.AddEdge || !call.GetItems()[i].GetEdgeAccepted() {
				return nil, errGraphMutation
			}
		default:
			return nil, errGraphMutation
		}
	}
	return e, nil
}

func encodeGraphMutationWAL(op mutationlog.MutationOp) ([]byte, error) {
	e, ok := op.(*graphMutationEnvelope)
	if !ok || e == nil {
		return nil, errGraphMutation
	}
	validated, err := decodeGraphMutation(e.Mutation)
	if err != nil {
		return nil, err
	}
	if len(e.Receipts) != len(validated.Receipts) {
		return nil, errGraphMutation
	}
	for i := range e.Receipts {
		if !sameReceiptWALDecision(e.Receipts[i], validated.Receipts[i]) {
			return nil, errGraphMutation
		}
	}
	return (proto.MarshalOptions{Deterministic: true}).Marshal(e.Mutation)
}

func decodeGraphMutationWAL(raw []byte) (mutationlog.MutationOp, error) {
	if len(raw) == 0 || len(raw) > graphMutationMaxBytes-receiptWALUnionHeaderSize {
		return nil, errGraphMutation
	}
	count, err := preflightReceiptVertexWAL(raw, 24, "graph.v1.ReplicatedGraphMutation")
	if err != nil || count > graphcache.GraphMutationMaxItems {
		return nil, errGraphMutation
	}
	var m pb.Mutation
	if err := (proto.UnmarshalOptions{DiscardUnknown: false}).Unmarshal(raw, &m); err != nil {
		return nil, err
	}
	e, err := decodeGraphMutation(&m)
	if err != nil {
		return nil, err
	}
	canonical, err := encodeGraphMutationWAL(e)
	if err != nil || !bytes.Equal(raw, canonical) {
		return nil, errGraphMutation
	}
	return e, nil
}

func graphMutationEffects(e *graphMutationEnvelope, projection bool) ([]graphcache.VertexItem[string, *pb.Vertex], []int, []graphcache.EdgeItem[string], []int) {
	var vertices []graphcache.VertexItem[string, *pb.Vertex]
	var vi, ei []int
	var edges []graphcache.EdgeItem[string]
	call := e.Mutation.GetOp().GetGraphMutation()
	selected := make(map[int]*pb.GraphMutationProjection, len(call.GetProjection()))
	for _, effect := range call.GetProjection() {
		selected[int(effect.GetItemIndex())] = effect
	}
	for i, item := range call.GetItems() {
		effect, ok := selected[i]
		if projection && !ok {
			continue
		}
		if v := item.GetOriginal().GetPutVertex(); v != nil {
			outcome := pb.PutOutcome(e.Receipts[i].Result[0])
			if outcome == pb.PutOutcome_PUT_OUTCOME_SUPERSEDED {
				continue
			}
			barrier := outcome == pb.PutOutcome_PUT_OUTCOME_EXPIRED
			if projection {
				barrier = effect.GetPutVertex().GetCausalBarrier() != nil
			}
			graph := graphcache.VertexItem[string, *pb.Vertex]{Key: v.GetKey(), CausalBarrier: barrier}
			if !barrier {
				graph.Value = v
				graph.Expiration = prototime.Expiration(v.GetExpiration())
			}
			vertices = append(vertices, graph)
			vi = append(vi, i)
		} else if item.GetEdgeAccepted() {
			add := item.GetOriginal().GetAddEdge()
			v := add.GetEdge()
			var id graphcache.ContribID
			copy(id[:], add.GetContribId())
			edges = append(edges, graphcache.EdgeItem[string]{Tail: v.GetTail(), Head: v.GetHead(), Weight: v.GetWeight(), Expiration: prototime.Expiration(v.GetExpiration()), ContribID: id, NoEndpointCreation: true})
			ei = append(ei, i)
		}
	}
	return vertices, vi, edges, ei
}

func graphMutationProjection(result graphcache.GraphMutationResult[string, *pb.Vertex], vi, ei []int) ([]*pb.GraphMutationProjection, error) {
	accepted, err := normalizedVertexPutAccepted(result.Vertices.Accepted, vi)
	if err != nil {
		return nil, err
	}
	out := make([]*pb.GraphMutationProjection, 0, len(accepted)+len(result.Edges.Accepted))
	for _, v := range accepted {
		effect := &pb.ReplicatedPutVertex{}
		if v.Item.CausalBarrier {
			effect.Outcome = &pb.ReplicatedPutVertex_CausalBarrier{CausalBarrier: &pb.VertexCausalBarrier{Key: v.Item.Key}}
		} else {
			effect.Outcome = &pb.ReplicatedPutVertex_Live{Live: v.Item.Value}
		}
		out = append(out, &pb.GraphMutationProjection{ItemIndex: uint32(v.Index), Effect: &pb.GraphMutationProjection_PutVertex{PutVertex: effect}})
	}
	for _, edge := range result.Edges.Accepted {
		if edge.Index < 0 || edge.Index >= len(ei) {
			return nil, fmt.Errorf("%w: Edge projection index", errGraphMutation)
		}
		out = append(out, &pb.GraphMutationProjection{ItemIndex: uint32(ei[edge.Index]), Effect: &pb.GraphMutationProjection_AddEdge{AddEdge: true}})
	}
	return out, nil
}
