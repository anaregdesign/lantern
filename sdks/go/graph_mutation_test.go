package client

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

type graphMutationTestClient struct {
	graphv1connect.LanternServiceClient
	capability *pb.GetReceiptCapabilityResponse
	calls      int
	fail       bool
	received   *pb.MutateGraphRequest
}

func (c *graphMutationTestClient) GetReceiptCapability(context.Context, *connect.Request[pb.GetReceiptCapabilityRequest]) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	return connect.NewResponse(c.capability), nil
}
func (c *graphMutationTestClient) MutateGraph(_ context.Context, r *connect.Request[pb.MutateGraphRequest]) (*connect.Response[pb.MutateGraphResponse], error) {
	c.calls++
	c.received = proto.Clone(r.Msg).(*pb.MutateGraphRequest)
	if c.fail {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("lost acknowledgement"))
	}
	return connect.NewResponse(&pb.MutateGraphResponse{Results: []*pb.GraphMutationResult{
		{Result: &pb.GraphMutationResult_PutVertex{PutVertex: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}},
		{Result: &pb.GraphMutationResult_AddEdgeEffectiveWeight{AddEdgeEffectiveWeight: 2}},
	}}), nil
}
func TestGraphMutationOneShotIdentityAndTypedResults(t *testing.T) {
	cap := testReceiptCapability(1)
	cap.GraphMutation = &GraphMutationCapability{MaxItems: 64, MaxRequestBytes: 1 << 20}
	wire := testReceiptCapabilityProto(cap)
	wire.GraphMutation = &pb.GraphMutationCapability{MaxItems: 64, MaxRequestBytes: 1 << 20}
	stub := &graphMutationTestClient{capability: wire}
	l := &Lantern{client: stub}
	identity, err := l.NewGraphMutationReceiptContext(cap, 2)
	if err != nil {
		t.Fatal(err)
	}
	vertices := []VertexInput{{Key: "a", Value: "body"}}
	edges := []EdgeAddReceiptInput{{Edge: EdgeInput{Tail: "a", Head: "a", Weight: 2}, ContribID: ContribID{1}}}
	result, err := l.MutateGraph(t.Context(), vertices, edges, identity)
	if err != nil || len(result.Vertices) != 1 || len(result.Edges) != 1 || result.Edges[0].EffectiveWeight != 2 || result.Edges[0].OperationID != identity.OperationIDs[1] {
		t.Fatal(result, err)
	}
	if stub.calls != 1 || len(stub.received.Items) != 2 || stub.received.Items[0].GetPutVertex() == nil || stub.received.Items[1].GetAddEdge() == nil {
		t.Fatal("call split or order drift")
	}
	first := proto.Clone(stub.received)
	stub.fail = true
	if _, err := l.MutateGraph(t.Context(), vertices, edges, identity); !errors.Is(err, ErrUnavailable) || stub.calls != 2 || !proto.Equal(first, stub.received) {
		t.Fatal("uncertain call retried or identity changed", err, stub.calls)
	}
	stub.capability.Endpoint.Generation[0]++
	if _, err := l.MutateGraph(t.Context(), vertices, edges, identity); !errors.Is(err, ErrReceiptReconciliationRequired) || stub.calls != 2 {
		t.Fatal("generation drift sent mutation", err)
	}
}
