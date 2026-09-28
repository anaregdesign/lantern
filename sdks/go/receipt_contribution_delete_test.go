package client

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type contributionReceiptClient struct {
	graphv1connect.LanternServiceClient
	capabilityFn func() (*pb.GetReceiptCapabilityResponse, error)
	deleteFn     func(*pb.DeleteEdgeContributionsRequest) (*pb.DeleteEdgeContributionsResponse, error)
	capabilityN  int
	deleteN      int
	requests     []*pb.DeleteEdgeContributionsRequest
}

func (c *contributionReceiptClient) GetReceiptCapability(context.Context, *connect.Request[pb.GetReceiptCapabilityRequest]) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	c.capabilityN++
	response, err := c.capabilityFn()
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *contributionReceiptClient) DeleteEdgeContributions(_ context.Context, request *connect.Request[pb.DeleteEdgeContributionsRequest]) (*connect.Response[pb.DeleteEdgeContributionsResponse], error) {
	c.deleteN++
	c.requests = append(c.requests, proto.Clone(request.Msg).(*pb.DeleteEdgeContributionsRequest))
	response, err := c.deleteFn(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *contributionReceiptClient) DeleteEdgeContribution(context.Context, *connect.Request[pb.DeleteEdgeContributionRequest]) (*connect.Response[pb.DeleteEdgeContributionResponse], error) {
	panic("singular contribution Delete RPC must not be called")
}

func TestContributionDeleteReceiptPreservesOriginalResultsAcrossResponseLoss(t *testing.T) {
	capability := testReceiptCapability(0x31)
	contextValue := testReceiptContextForMutation(t, capability, ReceiptMutationDeleteEdgeContribution, 2, 0x41)
	ref := EdgeContributionRef{Tail: "t", Head: "h", ContribID: testContribID(1)}
	refs := []EdgeContributionRef{ref, ref}
	fake := &contributionReceiptClient{}
	fake.capabilityFn = func() (*pb.GetReceiptCapabilityResponse, error) {
		return testReceiptCapabilityProto(capability), nil
	}
	fake.deleteFn = func(request *pb.DeleteEdgeContributionsRequest) (*pb.DeleteEdgeContributionsResponse, error) {
		if len(request.GetContributions()) != 2 ||
			!reflect.DeepEqual(request.GetContributions()[0].GetContribId(), ref.ContribID.Bytes()) ||
			request.GetReceiptContext() == nil {
			t.Fatalf("receipt request = %+v", request)
		}
		if fake.deleteN == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("committed response lost"))
		}
		return &pb.DeleteEdgeContributionsResponse{Deleted: 1, Existed: []bool{true, false}}, nil
	}
	policy := RetryPolicy{MaxAttempts: 2, sleepFn: noSleep, randFn: fixedRand(0)}.normalized()
	l := &Lantern{client: fake, opts: options{retry: &policy, batchChunkSize: 1}}
	results, err := l.DeleteEdgeContributionsWithReceipt(context.Background(), refs, contextValue)
	want := []EdgeContributionDeleteReceiptResult{
		{Contribution: ref, OperationID: contextValue.OperationIDs[0], Existed: true},
		{Contribution: ref, OperationID: contextValue.OperationIDs[1], Existed: false},
	}
	if err != nil || !reflect.DeepEqual(results, want) || fake.deleteN != 2 || fake.capabilityN != 2 ||
		!proto.Equal(fake.requests[0], fake.requests[1]) {
		t.Fatalf("results=%+v err=%v calls=%d/%d requests=%+v", results, err, fake.capabilityN, fake.deleteN, fake.requests)
	}
}

func TestContributionDeleteReceiptSingularUsesPluralAndRejectsInvalid(t *testing.T) {
	capability := testReceiptCapability(0x32)
	contextValue := testReceiptContextForMutation(t, capability, ReceiptMutationDeleteEdgeContribution, 1, 0x42)
	id := testContribID(4)
	fake := &contributionReceiptClient{
		capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
			return testReceiptCapabilityProto(capability), nil
		},
		deleteFn: func(request *pb.DeleteEdgeContributionsRequest) (*pb.DeleteEdgeContributionsResponse, error) {
			if len(request.GetContributions()) != 1 {
				t.Fatalf("singular facade sent %d keys", len(request.GetContributions()))
			}
			return &pb.DeleteEdgeContributionsResponse{Existed: []bool{false}}, nil
		},
	}
	l := &Lantern{client: fake}
	result, err := l.DeleteEdgeContributionWithReceipt(context.Background(), "t", "h", id, contextValue)
	if err != nil || result.Existed || result.OperationID != contextValue.OperationIDs[0] || result.Contribution.ContribID != id ||
		fake.deleteN != 1 {
		t.Fatalf("false original result = (%+v, %v), calls=%d", result, err, fake.deleteN)
	}
	otherKind := contextValue.Clone()
	otherKind.Mutation = ReceiptMutationDeleteEdge
	for _, tc := range []struct {
		refs []EdgeContributionRef
		ctx  ReceiptContext
	}{
		{nil, contextValue},
		{[]EdgeContributionRef{{Tail: "t", Head: "h"}}, contextValue},
		{[]EdgeContributionRef{{Tail: "t", Head: "h", ContribID: id}}, otherKind},
		{[]EdgeContributionRef{{Tail: "t", Head: "h", ContribID: id}, {Tail: "t", Head: "h", ContribID: id}}, contextValue},
	} {
		if _, err := l.DeleteEdgeContributionsWithReceipt(context.Background(), tc.refs, tc.ctx); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("invalid request %v = %v, want ErrInvalidReceipt", tc.refs, err)
		}
	}
	if fake.deleteN != 1 || fake.capabilityN != 1 {
		t.Fatalf("invalid requests reached transport: capability=%d delete=%d", fake.capabilityN, fake.deleteN)
	}
}

func TestContributionDeleteReceiptChecksContinuityCapabilityAndResponses(t *testing.T) {
	capability := testReceiptCapability(0x33)
	contextValue := testReceiptContextForMutation(t, capability, ReceiptMutationDeleteEdgeContribution, 2, 0x43)
	refs := []EdgeContributionRef{
		{Tail: "t", Head: "h", ContribID: testContribID(1)},
		{Tail: "t", Head: "h", ContribID: testContribID(2)},
	}
	for name, change := range map[string]func(*ReceiptCapability){
		"generation changed": func(c *ReceiptCapability) { c.Continuity.Generation[0]++ },
		"unsupported kind": func(c *ReceiptCapability) {
			c.SupportedMutations = c.SupportedMutations[:len(c.SupportedMutations)-1]
		},
	} {
		t.Run(name, func(t *testing.T) {
			changed := capability
			change(&changed)
			fake := &contributionReceiptClient{
				capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
					return testReceiptCapabilityProto(changed), nil
				},
				deleteFn: func(*pb.DeleteEdgeContributionsRequest) (*pb.DeleteEdgeContributionsResponse, error) {
					t.Fatal("mutation sent without matching capability")
					return nil, nil
				},
			}
			if _, err := (&Lantern{client: fake}).DeleteEdgeContributionsWithReceipt(context.Background(), refs, contextValue); !errors.Is(err, ErrReceiptReconciliationRequired) || fake.deleteN != 0 {
				t.Fatalf("continuity check = %v, sent=%d", err, fake.deleteN)
			}
		})
	}
	for name, response := range map[string]*pb.DeleteEdgeContributionsResponse{
		"short":       {Deleted: 1, Existed: []bool{true}},
		"wrong count": {Deleted: 2, Existed: []bool{true, false}},
	} {
		t.Run(name, func(t *testing.T) {
			fake := &contributionReceiptClient{
				capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
					return testReceiptCapabilityProto(capability), nil
				},
				deleteFn: func(*pb.DeleteEdgeContributionsRequest) (*pb.DeleteEdgeContributionsResponse, error) {
					return response, nil
				},
			}
			if _, err := (&Lantern{client: fake}).DeleteEdgeContributionsWithReceipt(context.Background(), refs, contextValue); !errors.Is(err, ErrReceiptProtocol) {
				t.Fatalf("malformed response = %v", err)
			}
		})
	}
}
