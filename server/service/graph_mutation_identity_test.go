package service

import (
	"testing"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"google.golang.org/protobuf/proto"
)

func TestGraphMutationIdentitySingleFinalBoundary(t *testing.T) {
	_, _, e := graphMutationCommittedFixture(t)
	var frames []*pb.SubscribeResponse
	if err := projectMutationIdentities(e.Mutation, func(f *pb.SubscribeResponse) error { frames = append(frames, f); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 2 {
		t.Fatal(frames)
	}
	first, last := frames[0].GetIdentityChunk(), frames[1].GetIdentityChunk()
	if first.IsLast || !last.IsLast || first.Operation != pb.IdentityOperation_IDENTITY_OPERATION_PUT_VERTEX || last.Operation != pb.IdentityOperation_IDENTITY_OPERATION_ADD_EDGE || last.ChunkIndex != 1 || last.FirstItemIndex != 2 || first.Seq != last.Seq {
		t.Fatal(first, last)
	}
	m := proto.Clone(e.Mutation).(*pb.Mutation)
	m.GetOp().GetGraphMutation().Projection = nil
	frames = nil
	if err := projectMutationIdentities(m, func(f *pb.SubscribeResponse) error { frames = append(frames, f); return nil }); err != nil {
		t.Fatal(err)
	}
	if len(frames) != 1 || !frames[0].GetIdentityChunk().IsLast || frames[0].GetIdentityChunk().Operation != pb.IdentityOperation_IDENTITY_OPERATION_RECEIPT_ONLY {
		t.Fatal(frames)
	}
}

func TestGraphMutationScopedChangesCurrentImageAndHiddenProgress(t *testing.T) {
	runtime, svc, _ := newActivatedReceiptService(t, 16)
	request := graphMutationTestRequest(t, runtime, 0x67)
	request.Items[0].GetPutVertex().Key = "data:orders:a"
	request.Items[1].GetPutVertex().Key = "data:private:b"
	request.Items[2].GetAddEdge().Edge.Tail = "data:orders:a"
	request.Items[2].GetAddEdge().Edge.Head = "data:private:b"
	if _, err := svc.MutateGraph(t.Context(), request); err != nil {
		t.Fatal(err)
	}
	m := runtime.log.RetainedEntries()[0].Op.(*graphMutationEnvelope).Mutation
	if err := runtime.graph.PutVertex("data:orders:a", &pb.Vertex{Key: "data:orders:a", Value: &pb.Vertex_String_{String_: "current"}}); err != nil {
		t.Fatal(err)
	}
	for _, prefix := range []string{"orders:", "invisible:"} {
		p := newChangeProjection(svc, &pb.WatchChangesRequest{Prefix: prefix, Projection: pb.ChangeProjection_CHANGE_PROJECTION_VALUE}, nil)
		var frames []*pb.WatchChangesResponse
		advanced := 0
		err := p.project(t.Context(), m, func(visible bool) ([]byte, error) {
			advanced++
			if visible != (prefix == "orders:") {
				t.Fatal("hidden progress leaked visibility")
			}
			return []byte("opaque"), nil
		}, func(f *pb.WatchChangesResponse) error { frames = append(frames, f); return nil })
		if err != nil || advanced != 1 {
			t.Fatal(advanced, err)
		}
		if prefix == "invisible:" {
			if len(frames) != 0 {
				t.Fatal("hidden-only extra frame")
			}
			continue
		}
		if len(frames) != 1 || len(frames[0].Invalidations) != 1 || string(frames[0].Cursor) != "opaque" {
			t.Fatal("private counts or positions leaked", frames)
		}
		item := frames[0].Invalidations[0]
		if item.GetVertexKey() != "orders:a" || item.GetVertex().GetString_() != "current" {
			t.Fatal("not the current logical image", item)
		}
	}
}
