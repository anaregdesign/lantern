package client

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type explicitAddClient struct {
	graphv1connect.LanternServiceClient
	requests []*pb.AddEdgesRequest
	failAt   int
}

func (c *explicitAddClient) AddEdges(_ context.Context, request *connect.Request[pb.AddEdgesRequest]) (*connect.Response[pb.AddEdgesResponse], error) {
	c.requests = append(c.requests, request.Msg)
	if len(c.requests) == c.failAt {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
	}
	effective := make([]float32, len(request.Msg.GetEdges()))
	for i, edge := range request.Msg.GetEdges() {
		effective[i] = edge.GetWeight()
	}
	return connect.NewResponse(&pb.AddEdgesResponse{Written: int32(len(effective)), EffectiveWeights: effective}), nil
}

func TestExplicitContributionAddPreservesCallerIDsAcrossChunks(t *testing.T) {
	capture := &explicitAddClient{}
	l := &Lantern{client: capture, opts: options{batchChunkSize: 2}}
	inputs := []EdgeAddInput{
		{Edge: EdgeInput{Tail: "a", Head: "b", Weight: 1}, ContribID: testContribID(1)},
		{Edge: EdgeInput{Tail: "a", Head: "b", Weight: 2}, ContribID: testContribID(2)},
		{Edge: EdgeInput{Tail: "c", Head: "d", Weight: 3}, ContribID: testContribID(3)},
	}
	weights, err := l.AddEdgesWithIDs(context.Background(), inputs)
	if err != nil || !reflect.DeepEqual(weights, []float32{1, 2, 3}) {
		t.Fatalf("effective weights = (%v, %v)", weights, err)
	}
	if len(capture.requests) != 2 || len(capture.requests[0].GetContribIds()) != 2 ||
		len(capture.requests[1].GetContribIds()) != 1 {
		t.Fatalf("Add chunks = %+v", capture.requests)
	}
	for i, request := range capture.requests {
		for j, wireID := range request.GetContribIds() {
			want := inputs[i*2+j].ContribID.Bytes()
			if !reflect.DeepEqual(wireID, want) {
				t.Fatalf("contrib_id[%d/%d] = %x, want %x", i, j, wireID, want)
			}
		}
	}
}

func TestExplicitContributionAddSingularAndInvalidInputs(t *testing.T) {
	capture := &explicitAddClient{}
	l := &Lantern{client: capture}
	expiration := time.Now().Add(time.Minute).UTC().Truncate(time.Second)
	effective, err := l.AddEdgeAtWithID(context.Background(), "tail", "head", 2.5, expiration, testContribID(4))
	if err != nil || effective != 2.5 || len(capture.requests) != 1 ||
		!capture.requests[0].GetEdges()[0].GetExpiration().AsTime().Equal(expiration) {
		t.Fatalf("singular explicit Add = (%v, %v), request=%+v", effective, err, capture.requests)
	}
	for _, input := range []EdgeAddInput{
		{Edge: EdgeInput{Tail: "t", Head: "h", Weight: 1}},
		{Edge: EdgeInput{Tail: "", Head: "h", Weight: 1}, ContribID: testContribID(1)},
		{Edge: EdgeInput{Tail: "t", Head: string([]byte{0xff}), Weight: 1}, ContribID: testContribID(1)},
	} {
		if _, err := l.AddEdgesWithIDs(context.Background(), []EdgeAddInput{input}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("AddEdgesWithIDs(%+v) = %v, want ErrInvalidArgument", input, err)
		}
	}
	if len(capture.requests) != 1 {
		t.Fatalf("invalid inputs sent %d requests", len(capture.requests)-1)
	}
}

func TestExplicitContributionAddResponseLossIsNotRetried(t *testing.T) {
	capture := &explicitAddClient{failAt: 1}
	policy := RetryPolicy{MaxAttempts: 3, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
	l := &Lantern{client: capture, opts: options{retry: &policy}}
	id := testContribID(5)
	if _, err := l.AddEdgeWithID(context.Background(), "tail", "head", 1, 0, id); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("lost response = %v", err)
	}
	if len(capture.requests) != 1 || !reflect.DeepEqual(capture.requests[0].GetContribIds()[0], id.Bytes()) {
		t.Fatalf("unexpected retries or lost caller ID: %+v", capture.requests)
	}
}
