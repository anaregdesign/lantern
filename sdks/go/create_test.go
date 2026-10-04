package client

import (
	"context"
	"errors"
	"math"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	"google.golang.org/protobuf/proto"
)

type createTestClient struct {
	graphv1connect.LanternServiceClient
	capability *pb.GetReceiptCapabilityResponse
	requests   []*pb.CreateEdgesRequest
	call       func(*pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error)
}

func (c *createTestClient) CreateEdges(_ context.Context, request *connect.Request[pb.CreateEdgesRequest]) (*connect.Response[pb.CreateEdgesResponse], error) {
	c.requests = append(c.requests, proto.Clone(request.Msg).(*pb.CreateEdgesRequest))
	response, err := c.call(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}
func (c *createTestClient) GetReceiptCapability(context.Context, *connect.Request[pb.GetReceiptCapabilityRequest]) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	return connect.NewResponse(proto.Clone(c.capability).(*pb.GetReceiptCapabilityResponse)), nil
}

func TestCreateBlindAcceptanceCoversWholeLogicalCallAndReceiptWithoutReplay(t *testing.T) {
	for _, failLast := range []bool{false, true} {
		fake := &createTestClient{}
		fake.call = func(*pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
			if len(fake.requests) == 3 && failLast {
				return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
			}
			if len(fake.requests) == 2 {
				return &pb.CreateEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}}, nil
			}
			return &pb.CreateEdgesResponse{Outcomes: []pb.CreateEdgeOutcome{pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_CREATED_AND_LIVE}}, nil
		}
		l := &Lantern{client: fake, opts: options{batchChunkSize: 1, retry: testRetryPolicy(3)}}
		results, err := l.CreateEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}, {Tail: "a", Head: "c", Weight: 1}, {Tail: "a", Head: "d", Weight: 1}})
		if results != nil || len(fake.requests) != 3 {
			t.Fatal("blind Create exposed effects, replayed or skipped input", results, len(fake.requests))
		}
		if failLast {
			var batch *BatchError
			if !errors.As(err, &batch) || batch.Written != 2 || !errors.Is(err, ErrUnavailable) {
				t.Fatal("later failure became acceptance", err)
			}
		} else if _, accepted := err.(*MutationAcceptance); !accepted {
			t.Fatal("missing blind acceptance", err)
		}
	}
	capability := testReceiptCapability(0x18)
	capability.SupportedMutations = append(capability.SupportedMutations, ReceiptMutationCreateEdge)
	receiptContext := testReceiptContextForMutation(t, capability, ReceiptMutationCreateEdge, 1, 0x28)
	fake := &createTestClient{capability: testReceiptCapabilityProto(capability), call: func(*pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
		return &pb.CreateEdgesResponse{Acceptance: &pb.MutationAcceptance{Kind: pb.MutationAcceptanceKind_MUTATION_ACCEPTANCE_KIND_HANDLED_EFFECT_UNDISCLOSED}}, nil
	}}
	l := &Lantern{client: fake, opts: options{retry: testRetryPolicy(3)}}
	_, err := l.CreateEdgesWithReceipt(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}}, receiptContext)
	if _, accepted := err.(*MutationAcceptance); !accepted || len(fake.requests) != 1 {
		t.Fatal("receipt acceptance was replayed or decoded as an outcome", err, len(fake.requests))
	}
}
func TestCreateEdgesAlignmentValidationAndAmbiguity(t *testing.T) {
	t.Run("ordered chunks and singular facade", func(t *testing.T) {
		fake := &createTestClient{}
		fake.call = func(req *pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
			if req.ReceiptContext != nil {
				t.Fatal("ordinary Create sent receipt context")
			}
			result := make([]pb.CreateEdgeOutcome, len(req.Edges))
			for i := range result {
				result[i] = pb.CreateEdgeOutcome_CREATE_EDGE_OUTCOME_EDGE_EXISTS
			}
			return &pb.CreateEdgesResponse{Outcomes: result}, nil
		}
		l := &Lantern{client: fake, opts: options{batchChunkSize: 2, retry: testRetryPolicy(3)}}
		inputs := []EdgeInput{{Tail: "a", Head: "b", Weight: 1}, {Tail: "a", Head: "b", Weight: 2}, {Tail: "a", Head: "c", Weight: -1}}
		outcomes, err := l.CreateEdges(context.Background(), inputs)
		if err != nil || !reflect.DeepEqual(outcomes, []CreateEdgeOutcome{CreateEdgeExists, CreateEdgeExists, CreateEdgeExists}) {
			t.Fatalf("Create = %v, %v", outcomes, err)
		}
		if len(fake.requests) != 2 || len(fake.requests[0].Edges) != 2 || len(fake.requests[1].Edges) != 1 {
			t.Fatal("chunk alignment lost")
		}
		if outcome, err := l.CreateEdge(context.Background(), inputs[0]); err != nil || outcome != CreateEdgeExists {
			t.Fatalf("singular = %v, %v", outcome, err)
		}
	})
	t.Run("prevalidate complete batch", func(t *testing.T) {
		fake := &createTestClient{}
		l := &Lantern{client: fake, opts: options{batchChunkSize: 1}}
		for _, weight := range []float32{0, float32(math.NaN()), float32(math.Inf(1))} {
			if _, err := l.CreateEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}, {Tail: "b", Head: "c", Weight: weight}}); err == nil {
				t.Fatal("invalid source accepted")
			}
		}
		if len(fake.requests) != 0 {
			t.Fatal("invalid later input allowed earlier mutation")
		}
	})
	for _, bad := range [][]pb.CreateEdgeOutcome{nil, {0}, {99}, {1, 1}} {
		t.Run("malformed outcome", func(t *testing.T) {
			fake := &createTestClient{call: func(*pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
				return &pb.CreateEdgesResponse{Outcomes: bad}, nil
			}}
			l := &Lantern{client: fake, opts: options{retry: testRetryPolicy(3)}}
			if _, err := l.CreateEdge(context.Background(), EdgeInput{Tail: "a", Head: "b", Weight: 1}); err == nil {
				t.Fatal("malformed outcome accepted")
			}
			if len(fake.requests) != 1 {
				t.Fatal("malformed result retried")
			}
		})
	}
	t.Run("response loss retains completed prefix and never retries", func(t *testing.T) {
		fake := &createTestClient{}
		fake.call = func(*pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
			if len(fake.requests) == 1 {
				return &pb.CreateEdgesResponse{Outcomes: []pb.CreateEdgeOutcome{1}}, nil
			}
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("committed response lost"))
		}
		l := &Lantern{client: fake, opts: options{batchChunkSize: 1, retry: testRetryPolicy(3)}}
		outcomes, err := l.CreateEdges(context.Background(), []EdgeInput{{Tail: "a", Head: "b", Weight: 1}, {Tail: "b", Head: "c", Weight: 1}})
		var partial *BatchError
		if !errors.As(err, &partial) || !reflect.DeepEqual(outcomes, []CreateEdgeOutcome{CreateEdgeCreatedAndLive}) || partial.Written != 1 || len(fake.requests) != 2 {
			t.Fatalf("partial = %v, %v, calls %d", outcomes, err, len(fake.requests))
		}
	})
}
func TestCreateEdgesWithReceiptReplaysFrozenIntent(t *testing.T) {
	cap := testReceiptCapability(0x51)
	cap.SupportedMutations = append(cap.SupportedMutations, ReceiptMutationCreateEdge)
	ctx := testReceiptContextForMutation(t, cap, ReceiptMutationCreateEdge, 1, 0x61)
	inputs := []EdgeInput{{Tail: "a", Head: "b", Weight: 1, Expiration: time.Now().Add(time.Hour)}}
	fake := &createTestClient{capability: testReceiptCapabilityProto(cap)}
	fake.call = func(*pb.CreateEdgesRequest) (*pb.CreateEdgesResponse, error) {
		if len(fake.requests) == 1 {
			inputs[0].Tail = "changed"
			ctx.OperationIDs[0] = ReceiptOperationID{}
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
		}
		return &pb.CreateEdgesResponse{Outcomes: []pb.CreateEdgeOutcome{1}}, nil
	}
	l := &Lantern{client: fake, opts: options{retry: testRetryPolicy(2)}}
	got, err := l.CreateEdgesWithReceipt(context.Background(), inputs, ctx)
	if err != nil || !reflect.DeepEqual(got, []CreateEdgeOutcome{CreateEdgeCreatedAndLive}) || len(fake.requests) != 2 {
		t.Fatalf("receipt Create = %v, %v", got, err)
	}
	if !proto.Equal(fake.requests[0], fake.requests[1]) || fake.requests[0].Edges[0].Tail != "a" {
		t.Fatal("replay changed original input or IDs")
	}
	wrong := testReceiptContext(t, cap, 1, 0x71)
	if _, err := l.CreateEdgesWithReceipt(context.Background(), inputs, wrong); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("wrong family = %v", err)
	}
}
