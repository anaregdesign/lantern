package client

import (
	"bytes"
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type vertexReceiptClient struct {
	graphv1connect.LanternServiceClient

	capability          *pb.GetReceiptCapabilityResponse
	capabilityCalls     int
	putVerticesFn       func(*pb.PutVerticesRequest) (*pb.PutVerticesResponse, error)
	putVerticesCalls    int
	deleteVerticesFn    func(*pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error)
	deleteVerticesCalls int
	singularPutCalls    int
	singularDeleteCalls int
}

func (c *vertexReceiptClient) GetReceiptCapability(
	context.Context,
	*connect.Request[pb.GetReceiptCapabilityRequest],
) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	c.capabilityCalls++
	return connect.NewResponse(proto.Clone(c.capability).(*pb.GetReceiptCapabilityResponse)), nil
}

func (c *vertexReceiptClient) PutVertices(
	_ context.Context,
	request *connect.Request[pb.PutVerticesRequest],
) (*connect.Response[pb.PutVerticesResponse], error) {
	c.putVerticesCalls++
	response, err := c.putVerticesFn(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *vertexReceiptClient) DeleteVertices(
	_ context.Context,
	request *connect.Request[pb.DeleteVerticesRequest],
) (*connect.Response[pb.DeleteVerticesResponse], error) {
	c.deleteVerticesCalls++
	response, err := c.deleteVerticesFn(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *vertexReceiptClient) PutVertex(
	context.Context,
	*connect.Request[pb.PutVertexRequest],
) (*connect.Response[pb.PutVertexResponse], error) {
	c.singularPutCalls++
	return nil, errors.New("singular Vertex Put RPC must not be called")
}

func (c *vertexReceiptClient) DeleteVertex(
	context.Context,
	*connect.Request[pb.DeleteVertexRequest],
) (*connect.Response[pb.DeleteVertexResponse], error) {
	c.singularDeleteCalls++
	return nil, errors.New("singular Vertex Delete RPC must not be called")
}

func TestVertexPutWithReceiptReplaysByteIdenticalRequest(t *testing.T) {
	capability := testReceiptCapability(0x80)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		3,
		0x81,
	)
	payload := []byte{1, 2, 3}
	inputs := []VertexInput{
		{Key: "live", Value: payload, Expiration: capability.ServerTime.Add(time.Hour)},
		{Key: "blocked", Value: "new"},
		{Key: "expired", Value: "dead", Expiration: capability.ServerTime.Add(-time.Hour)},
	}
	var requests [][]byte
	fake := &vertexReceiptClient{capability: testReceiptCapabilityProto(capability)}
	fake.putVerticesFn = func(request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
		encoded, err := proto.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		requests = append(requests, encoded)
		if len(requests) == 1 {
			payload[0] = 9
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
		}
		return &pb.PutVerticesResponse{Outcomes: []pb.PutOutcome{
			pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
			pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET,
			pb.PutOutcome_PUT_OUTCOME_EXPIRED,
		}}, nil
	}
	l := &Lantern{
		client: fake,
		opts:   options{retry: testRetryPolicy(2)},
	}

	results, err := l.PutVerticesIfAbsentWithReceipt(
		context.Background(),
		inputs,
		receiptContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	if fake.capabilityCalls != 2 || fake.putVerticesCalls != 2 {
		t.Fatalf(
			"capability/mutation calls = %d/%d, want 2/2",
			fake.capabilityCalls,
			fake.putVerticesCalls,
		)
	}
	if len(requests) != 2 || !bytes.Equal(requests[0], requests[1]) {
		t.Fatalf("replayed requests differ:\n%x\n%x", requests[0], requests[1])
	}
	wantOutcomes := []PutOutcome{
		PutOutcomeAppliedAndLive,
		PutOutcomeConditionNotMet,
		PutOutcomeExpired,
	}
	for i, result := range results {
		if result.Key != inputs[i].Key ||
			result.OperationID != receiptContext.OperationIDs[i] ||
			result.Outcome != wantOutcomes[i] {
			t.Fatalf("result[%d] = %+v", i, result)
		}
	}
}

func TestVertexPutWithReceiptRelativeTTLIsStableAcrossInvocations(t *testing.T) {
	capability := testReceiptCapability(0x8f)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		1,
		0x90,
	)
	var requests [][]byte
	fake := &vertexReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		putVerticesFn: func(request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
			encoded, err := proto.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			requests = append(requests, encoded)
			return &pb.PutVerticesResponse{
				Outcomes: []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE},
			}, nil
		},
	}
	now := capability.ServerTime
	l := &Lantern{
		client: fake,
		clock:  func() time.Time { return now },
	}
	const ttl = 15 * time.Minute
	if _, err := l.PutVertexWithReceipt(
		context.Background(),
		"stable-ttl",
		"value",
		ttl,
		receiptContext,
	); err != nil {
		t.Fatal(err)
	}
	now = now.Add(24 * time.Hour)
	if _, err := l.PutVertexWithReceipt(
		context.Background(),
		"stable-ttl",
		"value",
		ttl,
		receiptContext,
	); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 2 || !bytes.Equal(requests[0], requests[1]) {
		t.Fatalf("cross-invocation replay changed request:\n%x\n%x", requests[0], requests[1])
	}
	var request pb.PutVerticesRequest
	if err := proto.Unmarshal(requests[0], &request); err != nil {
		t.Fatal(err)
	}
	issuedAt, err := receiptContext.OperationIDs[0].IssuedAt()
	if err != nil {
		t.Fatal(err)
	}
	if got := request.GetVertices()[0].GetExpiration().AsTime(); !got.Equal(issuedAt.Add(ttl)) {
		t.Fatalf("expiration = %v, want %v", got, issuedAt.Add(ttl))
	}
}

func TestVertexReceiptSingularFacadesUsePluralRPCs(t *testing.T) {
	capability := testReceiptCapability(0x82)
	putContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		1,
		0x83,
	)
	deleteContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationDeleteVertex,
		1,
		0x84,
	)
	fake := &vertexReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		putVerticesFn: func(request *pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
			if !request.GetIfAbsent() || len(request.GetVertices()) != 1 {
				t.Fatalf("conditional Put request = %+v", request)
			}
			return &pb.PutVerticesResponse{
				Outcomes: []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET},
			}, nil
		},
		deleteVerticesFn: func(request *pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
			if !reflect.DeepEqual(request.GetKeys(), []string{"missing"}) {
				t.Fatalf("Delete request keys = %v", request.GetKeys())
			}
			return &pb.DeleteVerticesResponse{Existed: []bool{false}}, nil
		},
	}
	l := &Lantern{client: fake}

	putResult, err := l.PutVertexIfAbsentWithReceipt(
		context.Background(),
		"existing",
		"new",
		time.Minute,
		putContext,
	)
	if err != nil || putResult.Outcome != PutOutcomeConditionNotMet ||
		putResult.OperationID != putContext.OperationIDs[0] {
		t.Fatalf("Put result = (%+v, %v)", putResult, err)
	}
	deleteResult, err := l.DeleteVertexWithReceipt(
		context.Background(),
		"missing",
		deleteContext,
	)
	if err != nil || deleteResult.Existed ||
		deleteResult.OperationID != deleteContext.OperationIDs[0] {
		t.Fatalf("Delete result = (%+v, %v)", deleteResult, err)
	}
	if fake.singularPutCalls != 0 || fake.singularDeleteCalls != 0 ||
		fake.putVerticesCalls != 1 || fake.deleteVerticesCalls != 1 {
		t.Fatalf(
			"wire calls plural put/delete=%d/%d singular put/delete=%d/%d",
			fake.putVerticesCalls,
			fake.deleteVerticesCalls,
			fake.singularPutCalls,
			fake.singularDeleteCalls,
		)
	}
}

func TestVertexDeleteWithReceiptPreservesAlignment(t *testing.T) {
	capability := testReceiptCapability(0x85)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationDeleteVertex,
		3,
		0x86,
	)
	keys := []string{"present-a", "absent", "present-b"}
	fake := &vertexReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		deleteVerticesFn: func(*pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
			return &pb.DeleteVerticesResponse{
				Deleted: 2,
				Existed: []bool{true, false, true},
			}, nil
		},
	}
	l := &Lantern{client: fake}

	results, err := l.DeleteVerticesWithReceipt(
		context.Background(),
		keys,
		receiptContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	for i, result := range results {
		if result.Key != keys[i] ||
			result.OperationID != receiptContext.OperationIDs[i] ||
			result.Existed != (i != 1) {
			t.Fatalf("result[%d] = %+v", i, result)
		}
	}
}

func TestVertexReceiptRejectsMalformedInputsBeforeTransport(t *testing.T) {
	capability := testReceiptCapability(0x87)
	putContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		1,
		0x88,
	)
	deleteContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationDeleteVertex,
		1,
		0x89,
	)
	fake := &vertexReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		putVerticesFn: func(*pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
			t.Fatal("unexpected Put transport")
			return nil, nil
		},
		deleteVerticesFn: func(*pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
			t.Fatal("unexpected Delete transport")
			return nil, nil
		},
	}
	l := &Lantern{client: fake}

	cases := []func() error{
		func() error {
			_, err := l.PutVerticesWithReceipt(
				context.Background(),
				nil,
				ReceiptContext{},
			)
			return err
		},
		func() error {
			_, err := l.PutVertexWithReceipt(
				context.Background(),
				"",
				"value",
				time.Minute,
				putContext,
			)
			return err
		},
		func() error {
			_, err := l.PutVertexWithReceipt(
				context.Background(),
				"vertex",
				"value",
				time.Minute,
				deleteContext,
			)
			return err
		},
		func() error {
			_, err := l.DeleteVerticesWithReceipt(
				context.Background(),
				[]string{"a", "b"},
				deleteContext,
			)
			return err
		},
		func() error {
			_, err := l.DeleteVertexWithReceipt(
				context.Background(),
				"\xff",
				deleteContext,
			)
			return err
		},
	}
	for i, run := range cases {
		if err := run(); !errors.Is(err, ErrInvalidReceipt) {
			t.Errorf("case %d error = %v", i, err)
		}
	}
	if fake.capabilityCalls != 0 ||
		fake.putVerticesCalls != 0 ||
		fake.deleteVerticesCalls != 0 {
		t.Fatalf(
			"transport calls capability/put/delete=%d/%d/%d",
			fake.capabilityCalls,
			fake.putVerticesCalls,
			fake.deleteVerticesCalls,
		)
	}
}

func TestVertexReceiptUnsupportedAndIntentConflictFailClosed(t *testing.T) {
	capability := testReceiptCapability(0x8a)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		1,
		0x8b,
	)

	t.Run("unsupported endpoint", func(t *testing.T) {
		unsupported := capability
		unsupported.SupportedMutations = []ReceiptMutationKind{
			ReceiptMutationDeleteVertex,
			ReceiptMutationDeleteEdge,
		}
		fake := &vertexReceiptClient{
			capability: testReceiptCapabilityProto(unsupported),
			putVerticesFn: func(*pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
				t.Fatal("unexpected mutation")
				return nil, nil
			},
		}
		l := &Lantern{client: fake}
		_, err := l.PutVertexWithReceipt(
			context.Background(),
			"vertex",
			"value",
			time.Minute,
			receiptContext,
		)
		if !errors.Is(err, ErrReceiptMutationUnsupported) ||
			!errors.Is(err, ErrReceiptReconciliationRequired) {
			t.Fatalf("error = %v", err)
		}
		if fake.putVerticesCalls != 0 {
			t.Fatalf("mutation calls = %d", fake.putVerticesCalls)
		}
	})

	t.Run("intent conflict is not retried", func(t *testing.T) {
		fake := &vertexReceiptClient{
			capability: testReceiptCapabilityProto(capability),
			putVerticesFn: func(*pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
				return nil, connect.NewError(
					connect.CodeInvalidArgument,
					errors.New("operation ID intent conflict"),
				)
			},
		}
		l := &Lantern{
			client: fake,
			opts:   options{retry: testRetryPolicy(3)},
		}
		_, err := l.PutVertexWithReceipt(
			context.Background(),
			"vertex",
			"value",
			time.Minute,
			receiptContext,
		)
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("error = %v", err)
		}
		if fake.capabilityCalls != 1 || fake.putVerticesCalls != 1 {
			t.Fatalf(
				"capability/mutation calls = %d/%d",
				fake.capabilityCalls,
				fake.putVerticesCalls,
			)
		}
	})
}

func TestVertexReceiptMalformedResponsesFailClosed(t *testing.T) {
	capability := testReceiptCapability(0x8c)
	putContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationPutVertex,
		1,
		0x8d,
	)
	deleteContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationDeleteVertex,
		1,
		0x8e,
	)

	t.Run("Put count drift", func(t *testing.T) {
		fake := &vertexReceiptClient{
			capability: testReceiptCapabilityProto(capability),
			putVerticesFn: func(*pb.PutVerticesRequest) (*pb.PutVerticesResponse, error) {
				return &pb.PutVerticesResponse{}, nil
			},
		}
		l := &Lantern{client: fake}
		if _, err := l.PutVertexWithReceipt(
			context.Background(),
			"vertex",
			"value",
			time.Minute,
			putContext,
		); !errors.Is(err, ErrReceiptProtocol) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("Delete count drift", func(t *testing.T) {
		fake := &vertexReceiptClient{
			capability: testReceiptCapabilityProto(capability),
			deleteVerticesFn: func(*pb.DeleteVerticesRequest) (*pb.DeleteVerticesResponse, error) {
				return &pb.DeleteVerticesResponse{Deleted: 1, Existed: []bool{false}}, nil
			},
		}
		l := &Lantern{client: fake}
		if _, err := l.DeleteVertexWithReceipt(
			context.Background(),
			"vertex",
			deleteContext,
		); !errors.Is(err, ErrReceiptProtocol) {
			t.Fatalf("error = %v", err)
		}
	})
}
