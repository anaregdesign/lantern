package dataruntime

import (
	"fmt"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// tarpMutationKeys returns a conservative set of physical identities. It is
// safe to refresh a rejected original item: the store reads the actual graph
// state at publication. Prefix deletes are logged as their exact victims.
func tarpMutationKeys(m *pb.Mutation) (graphcache.StorageKeys[string], error) {
	var keys graphcache.StorageKeys[string]
	vertex := func(v *pb.Vertex) {
		if v != nil {
			keys.Vertices = append(keys.Vertices, v.GetKey())
		}
	}
	edge := func(tail, head string) {
		keys.Edges = append(keys.Edges, graphcache.EdgeKey[string]{Tail: tail, Head: head})
	}
	value := func(e *pb.Edge) {
		if e != nil {
			edge(e.GetTail(), e.GetHead())
		}
	}
	contribution := func(tail, head string, id []byte) {
		edge(tail, head)
		keys.Contributions = append(keys.Contributions, graphcache.EdgeContributionKey[string]{
			Tail: tail, Head: head, ContribID: contribIDFromBytes(id),
		})
	}
	switch op := m.GetOp().GetOp().(type) {
	case *pb.MutationOp_PutVertex:
		vertex(op.PutVertex.GetVertex())
	case *pb.MutationOp_PutVertices:
		for _, v := range op.PutVertices.GetVertices() {
			vertex(v)
		}
	case *pb.MutationOp_ReplicatedPutVertices:
		for _, item := range op.ReplicatedPutVertices.GetEntries() {
			vertex(item.GetLive())
			if barrier := item.GetCausalBarrier(); barrier != nil {
				keys.Vertices = append(keys.Vertices, barrier.GetKey())
			}
		}
	case *pb.MutationOp_DeleteVertex:
		keys.Vertices = append(keys.Vertices, op.DeleteVertex.GetKey())
	case *pb.MutationOp_DeleteVertices:
		keys.Vertices = append(keys.Vertices, op.DeleteVertices.GetKeys()...)
	case *pb.MutationOp_AddEdge:
		value(op.AddEdge.GetEdge())
	case *pb.MutationOp_AddEdges:
		for _, e := range op.AddEdges.GetEdges() {
			value(e)
		}
	case *pb.MutationOp_PutEdge:
		value(op.PutEdge.GetEdge())
	case *pb.MutationOp_PutEdges:
		for _, e := range op.PutEdges.GetEdges() {
			value(e)
		}
	case *pb.MutationOp_ReplicatedPutEdges:
		for _, item := range op.ReplicatedPutEdges.GetEntries() {
			value(item.GetLive())
			if barrier := item.GetCausalBarrier(); barrier != nil {
				edge(barrier.GetTail(), barrier.GetHead())
			}
		}
	case *pb.MutationOp_DeleteEdge:
		edge(op.DeleteEdge.GetTail(), op.DeleteEdge.GetHead())
	case *pb.MutationOp_DeleteEdges:
		for _, e := range op.DeleteEdges.GetEdges() {
			if e != nil {
				edge(e.GetTail(), e.GetHead())
			}
		}
	case *pb.MutationOp_DeleteEdgeContribution:
		e := op.DeleteEdgeContribution
		contribution(e.GetTail(), e.GetHead(), e.GetContribId())
	case *pb.MutationOp_DeleteEdgeContributions:
		for _, e := range op.DeleteEdgeContributions.GetContributions() {
			if e != nil {
				contribution(e.GetTail(), e.GetHead(), e.GetContribId())
			}
		}
	case *pb.MutationOp_ReplicatedReceiptEdgeDelete:
		for _, item := range op.ReplicatedReceiptEdgeDelete.GetItems() {
			e := item.GetKey()
			edge(e.GetTail(), e.GetHead())
		}
	case *pb.MutationOp_ReplicatedReceiptEdgeContributionDelete:
		for _, item := range op.ReplicatedReceiptEdgeContributionDelete.GetItems() {
			e := item.GetKey()
			contribution(e.GetTail(), e.GetHead(), e.GetContribId())
		}
	case *pb.MutationOp_ReplicatedReceiptVertexDelete:
		for _, item := range op.ReplicatedReceiptVertexDelete.GetItems() {
			keys.Vertices = append(keys.Vertices, item.GetKey())
		}
	case *pb.MutationOp_ReplicatedReceiptVertexPut:
		for _, item := range op.ReplicatedReceiptVertexPut.GetItems() {
			vertex(item.GetOriginal())
		}
	case *pb.MutationOp_ReplicatedReceiptEdgeAdd:
		for _, item := range op.ReplicatedReceiptEdgeAdd.GetItems() {
			value(item.GetOriginal())
		}
	case *pb.MutationOp_EdgeCreateEffect:
		for _, item := range op.EdgeCreateEffect.GetItems() {
			value(item.GetOriginal())
		}
	case *pb.MutationOp_GraphMutation:
		for _, item := range op.GraphMutation.GetItems() {
			vertex(item.GetOriginal().GetPutVertex())
			value(item.GetOriginal().GetAddEdge().GetEdge())
		}
	default:
		return keys, fmt.Errorf("unsupported stored mutation %T", op)
	}
	return keys, nil
}
