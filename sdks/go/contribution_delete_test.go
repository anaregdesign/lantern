package client

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type contributionDeleteClient struct {
	graphv1connect.LanternServiceClient
	requests []*pb.DeleteEdgeContributionsRequest
	seen     map[EdgeContributionRef]bool
	failAt   int
	bad      *pb.DeleteEdgeContributionsResponse
}

func (c *contributionDeleteClient) DeleteEdgeContributions(_ context.Context, request *connect.Request[pb.DeleteEdgeContributionsRequest]) (*connect.Response[pb.DeleteEdgeContributionsResponse], error) {
	c.requests = append(c.requests, request.Msg)
	if len(c.requests) == c.failAt {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
	}
	if c.bad != nil {
		return connect.NewResponse(c.bad), nil
	}
	response := &pb.DeleteEdgeContributionsResponse{Existed: make([]bool, len(request.Msg.GetContributions()))}
	for i, key := range request.Msg.GetContributions() {
		id, err := ContribIDFromBytes(key.GetContribId())
		if err != nil {
			return nil, err
		}
		ref := EdgeContributionRef{Tail: key.GetTail(), Head: key.GetHead(), ContribID: id}
		response.Existed[i] = c.seen[ref]
		if response.Existed[i] {
			response.Deleted++
			c.seen[ref] = false
		}
	}
	return connect.NewResponse(response), nil
}

func (c *contributionDeleteClient) DeleteEdgeContribution(context.Context, *connect.Request[pb.DeleteEdgeContributionRequest]) (*connect.Response[pb.DeleteEdgeContributionResponse], error) {
	panic("singular wire RPC must not be called")
}

func TestDeleteEdgeContributionsAlignsDuplicatesMissesAndChunks(t *testing.T) {
	first := EdgeContributionRef{Tail: "tail", Head: "head", ContribID: testContribID(1)}
	second := EdgeContributionRef{Tail: "tail", Head: "head", ContribID: testContribID(2)}
	wrongPair := EdgeContributionRef{Tail: "tail", Head: "other", ContribID: first.ContribID}
	capture := &contributionDeleteClient{seen: map[EdgeContributionRef]bool{first: true, second: true}}
	l := &Lantern{client: capture, opts: options{batchChunkSize: 2}}
	existed, deleted, err := l.DeleteEdgeContributions(context.Background(), []EdgeContributionRef{
		first, first, wrongPair, second, second,
	})
	if err != nil || deleted != 2 || !reflect.DeepEqual(existed, []bool{true, false, false, true, false}) {
		t.Fatalf("results = (%v, %d, %v)", existed, deleted, err)
	}
	if len(capture.requests) != 3 ||
		len(capture.requests[0].GetContributions()) != 2 ||
		len(capture.requests[1].GetContributions()) != 2 ||
		len(capture.requests[2].GetContributions()) != 1 {
		t.Fatalf("chunk boundaries = %+v", capture.requests)
	}
	if got := capture.requests[0].GetContributions()[0]; !reflect.DeepEqual(got.GetContribId(), first.ContribID.Bytes()) {
		t.Fatalf("canonical 24-byte ID = %x, want %x", got.GetContribId(), first.ContribID)
	}
}

func TestDeleteEdgeContributionSingularAndInvalidInputs(t *testing.T) {
	ref := EdgeContributionRef{Tail: "a", Head: "b", ContribID: testContribID(4)}
	capture := &contributionDeleteClient{seen: map[EdgeContributionRef]bool{ref: true}}
	l := &Lantern{client: capture}
	first, err := l.DeleteEdgeContribution(context.Background(), ref.Tail, ref.Head, ref.ContribID)
	if err != nil || !first || len(capture.requests) != 1 {
		t.Fatalf("first delete = (%v, %v), plural calls=%d", first, err, len(capture.requests))
	}
	second, err := l.DeleteEdgeContribution(context.Background(), ref.Tail, ref.Head, ref.ContribID)
	if err != nil || second {
		t.Fatalf("second delete = (%v, %v), want false", second, err)
	}
	for _, bad := range []EdgeContributionRef{
		{Tail: "a", Head: "b"},
		{Tail: "", Head: "b", ContribID: testContribID(1)},
		{Tail: "a", Head: string([]byte{0xff}), ContribID: testContribID(1)},
	} {
		if _, _, err := l.DeleteEdgeContributions(context.Background(), []EdgeContributionRef{bad}); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("invalid reference %+v = %v", bad, err)
		}
	}
	if len(capture.requests) != 2 {
		t.Fatalf("invalid inputs sent %d requests", len(capture.requests)-2)
	}
}

func TestDeleteEdgeContributionsPartialResponseAndNoRetry(t *testing.T) {
	refs := []EdgeContributionRef{
		{Tail: "a", Head: "b", ContribID: testContribID(1)},
		{Tail: "c", Head: "d", ContribID: testContribID(2)},
		{Tail: "e", Head: "f", ContribID: testContribID(3)},
	}
	capture := &contributionDeleteClient{seen: map[EdgeContributionRef]bool{refs[0]: true}, failAt: 2}
	policy := RetryPolicy{MaxAttempts: 3, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
	l := &Lantern{client: capture, opts: options{batchChunkSize: 2, retry: &policy}}
	existed, deleted, err := l.DeleteEdgeContributions(context.Background(), refs)
	var batch *BatchError
	if !errors.Is(err, ErrUnavailable) || !errors.As(err, &batch) || batch.Written != 2 ||
		deleted != 1 || !reflect.DeepEqual(existed, []bool{true, false}) || len(capture.requests) != 2 {
		t.Fatalf("observed prefix = (%v, %d, %v), calls=%d", existed, deleted, err, len(capture.requests))
	}
	for _, bad := range []*pb.DeleteEdgeContributionsResponse{
		{Deleted: 1, Existed: []bool{true}},
		{Deleted: 2, Existed: []bool{true, false}},
	} {
		capture := &contributionDeleteClient{bad: bad}
		l := &Lantern{client: capture}
		existed, deleted, err := l.DeleteEdgeContributions(context.Background(), refs[:2])
		if err == nil || len(existed) != 0 || deleted != 0 {
			t.Fatalf("malformed response = (%v, %d, %v)", existed, deleted, err)
		}
	}
}
