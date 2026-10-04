package service

import (
	"testing"

	"google.golang.org/protobuf/proto"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/keyspace"
)

func TestDataIntentRemainsLogical(t *testing.T) {
	logical := &pb.Vertex{Key: "sys:user", Value: &pb.Vertex_Int64{Int64: 42}}
	physical := proto.Clone(logical).(*pb.Vertex)
	physical.Key = "data:" + logical.Key
	want, err := vertexPutDigest(logical, true)
	if err != nil {
		t.Fatal(err)
	}
	got, err := vertexPutDigest(physical, true, keyspace.Version)
	if err != nil || got != want {
		t.Fatal("physical namespace changed the receipt Vertex Put intent")
	}
	if got := vertexDeleteDigest(physical.Key, keyspace.Version); got != vertexDeleteDigest(logical.Key) {
		t.Fatal("physical namespace changed the Vertex Delete intent")
	}
	if got := edgeDeleteDigest("data:sys:user", "data:data:user", keyspace.Version); got != edgeDeleteDigest("sys:user", "data:user") {
		t.Fatal("physical namespace changed the Edge Delete intent")
	}
	key := graphcache.EdgeContributionKey[string]{Tail: "sys:user", Head: "data:user", ContribID: graphcache.ContribID{1}}
	physKey := key
	physKey.Tail, physKey.Head = "data:"+key.Tail, "data:"+key.Head
	if edgeContributionDeleteDigest(physKey, keyspace.Version) != edgeContributionDeleteDigest(key) {
		t.Fatal("physical namespace changed the contribution Delete intent")
	}
	edge := &pb.Edge{Tail: key.Tail, Head: key.Head, Weight: 2}
	physEdge := proto.Clone(edge).(*pb.Edge)
	physEdge.Tail, physEdge.Head = physKey.Tail, physKey.Head
	want, err = receiptEdgeAddDigest(edge, key.ContribID)
	if err != nil {
		t.Fatal(err)
	}
	got, err = receiptEdgeAddDigest(physEdge, key.ContribID, keyspace.Version)
	if err != nil || got != want {
		t.Fatal("physical namespace changed the Add intent")
	}
	if _, err := vertexPutDigest(logical, false, keyspace.Version); err == nil {
		t.Fatal("unencoded physical receipt identity accepted")
	}
	if _, err := vertexPutDigest(physical, false, "unknown"); err == nil {
		t.Fatal("unknown receipt namespace format accepted")
	}
}

func TestPhysicalDataIdentityValidation(t *testing.T) {
	for _, frame := range []*pb.Mutation{
		{Op: &pb.MutationOp{Op: &pb.MutationOp_PutVertex{PutVertex: &pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "sys:roles"}}}}},
		{Op: &pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "data:x", Head: "sys:roles"}}}}},
		{Op: &pb.MutationOp{Op: &pb.MutationOp_DeleteVertices{DeleteVertices: &pb.DeleteVerticesRequest{Keys: []string{"data:x", "legacy"}}}}},
	} {
		if err := validatePhysicalDataIdentities(frame.ProtoReflect()); err == nil {
			t.Fatal("internal generic graph mutation escaped the data domain")
		}
	}
	frame := &pb.Mutation{Op: &pb.MutationOp{Op: &pb.MutationOp_PutEdge{PutEdge: &pb.PutEdgeRequest{Edge: &pb.Edge{Tail: "data:sys:x", Head: "data:data:y"}}}}}
	if err := validatePhysicalDataIdentities(frame.ProtoReflect()); err != nil {
		t.Fatal(err)
	}
}
