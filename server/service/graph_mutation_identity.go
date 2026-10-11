package service

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// projectGraphMutationIdentities emits the existing typed invalidations in
// operation order. Chunk/item indexes span both families and only the final
// chunk closes the one origin sequence. WatchChanges filters this stream before
// adding its single final visible cursor, without exposing private positions.
func projectGraphMutationIdentities(m *pb.Mutation, send func(*pb.SubscribeResponse) error) error {
	e, err := decodeGraphMutation(m)
	if err != nil {
		return err
	}
	vertices, _, edges, _ := graphMutationEffects(e, true)
	category := pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY
	if len(vertices) > 0 {
		category = pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX
	} else if len(edges) > 0 {
		category = pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE
	}
	p := newIdentityProjector(m, category, send)
	for _, v := range vertices {
		if err := p.addVertex(v.Key); err != nil {
			return err
		}
	}
	if len(vertices) > 0 && len(edges) > 0 {
		if err := p.flush(false); err != nil {
			return err
		}
		p.index++
		p.category = pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE
		p.startChunk()
	}
	for _, e := range edges {
		if err := p.addEdge(e.Tail, e.Head); err != nil {
			return err
		}
	}
	return p.flush(true)
}
