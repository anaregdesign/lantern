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

type receiptDeleteClient struct {
	graphv1connect.LanternServiceClient

	capabilityFn    func() (*pb.GetReceiptCapabilityResponse, error)
	deleteFn        func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error)
	capabilityCalls int
	deleteCalls     int
	requests        []*pb.DeleteEdgesRequest
}

func (c *receiptDeleteClient) GetReceiptCapability(
	context.Context,
	*connect.Request[pb.GetReceiptCapabilityRequest],
) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	c.capabilityCalls++
	response, err := c.capabilityFn()
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *receiptDeleteClient) DeleteEdges(
	_ context.Context,
	request *connect.Request[pb.DeleteEdgesRequest],
) (*connect.Response[pb.DeleteEdgesResponse], error) {
	c.deleteCalls++
	c.requests = append(c.requests, proto.Clone(request.Msg).(*pb.DeleteEdgesRequest))
	response, err := c.deleteFn(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func TestDeleteEdgesWithReceiptRejectsMalformedContextBeforeTransport(t *testing.T) {
	capability := testReceiptCapability(0x11)
	valid := testReceiptContext(t, capability, 2, 0x21)
	other := testReceiptContext(t, testReceiptCapability(0x31), 1, 0x41)
	tests := []struct {
		name    string
		refs    []EdgeRef
		context ReceiptContext
	}{
		{"empty", nil, ReceiptContext{}},
		{"misaligned", []EdgeRef{{Tail: "a", Head: "b"}}, valid},
		{"zero group", []EdgeRef{{Tail: "a", Head: "b"}, {Tail: "c", Head: "d"}}, func() ReceiptContext {
			c := valid.Clone()
			c.GroupID = ReceiptGroupID{}
			return c
		}()},
		{"mixed epochs", []EdgeRef{{Tail: "a", Head: "b"}, {Tail: "c", Head: "d"}}, func() ReceiptContext {
			c := valid.Clone()
			c.OperationIDs[1] = other.OperationIDs[0]
			return c
		}()},
		{"duplicate IDs", []EdgeRef{{Tail: "a", Head: "b"}, {Tail: "c", Head: "d"}}, func() ReceiptContext {
			c := valid.Clone()
			c.OperationIDs[1] = c.OperationIDs[0]
			return c
		}()},
		{"empty edge", []EdgeRef{{Tail: "", Head: "b"}, {Tail: "c", Head: "d"}}, valid},
		{"invalid UTF-8", []EdgeRef{{Tail: string([]byte{0xff}), Head: "b"}, {Tail: "c", Head: "d"}}, valid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &receiptDeleteClient{
				capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
					t.Fatal("capability transport called")
					return nil, nil
				},
				deleteFn: func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
					t.Fatal("delete transport called")
					return nil, nil
				},
			}
			l := &Lantern{client: fake}
			if _, err := l.DeleteEdgesWithReceipt(context.Background(), test.refs, test.context); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("error = %v", err)
			}
			if fake.capabilityCalls != 0 || fake.deleteCalls != 0 {
				t.Fatalf("transport calls = capability %d delete %d", fake.capabilityCalls, fake.deleteCalls)
			}
		})
	}
}

func TestDeleteEdgesWithReceiptResponseLossReusesExactContext(t *testing.T) {
	capability := testReceiptCapability(0x12)
	receiptContext := testReceiptContext(t, capability, 2, 0x22)
	fake := &receiptDeleteClient{}
	fake.capabilityFn = func() (*pb.GetReceiptCapabilityResponse, error) {
		return testReceiptCapabilityProto(capability), nil
	}
	fake.deleteFn = func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
		if fake.deleteCalls == 1 {
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("committed response lost"))
		}
		return &pb.DeleteEdgesResponse{Deleted: 1, Existed: []bool{true, false}}, nil
	}
	l := &Lantern{
		client: fake,
		opts: options{retry: &RetryPolicy{
			MaxAttempts: 2, BaseDelay: 1, MaxDelay: 1,
			sleepFn: noSleep, randFn: fixedRand(0),
		}},
	}
	l.opts.retry = func() *RetryPolicy {
		p := l.opts.retry.normalized()
		return &p
	}()
	refs := []EdgeRef{{Tail: "present", Head: "edge"}, {Tail: "absent", Head: "edge"}}
	results, err := l.DeleteEdgesWithReceipt(context.Background(), refs, receiptContext)
	if err != nil {
		t.Fatal(err)
	}
	if fake.capabilityCalls != 2 || fake.deleteCalls != 2 {
		t.Fatalf("calls = capability %d delete %d", fake.capabilityCalls, fake.deleteCalls)
	}
	if !proto.Equal(fake.requests[0], fake.requests[1]) {
		t.Fatalf("retry changed request:\nfirst=%v\nsecond=%v", fake.requests[0], fake.requests[1])
	}
	want := []EdgeDeleteReceiptResult{
		{Edge: refs[0], OperationID: receiptContext.OperationIDs[0], Existed: true},
		{Edge: refs[1], OperationID: receiptContext.OperationIDs[1], Existed: false},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results = %+v, want %+v", results, want)
	}
}

func TestDeleteEdgesWithReceiptStopsWhenGenerationChangesAfterResponseLoss(t *testing.T) {
	capability := testReceiptCapability(0x17)
	receiptContext := testReceiptContext(t, capability, 1, 0x27)
	changed := capability
	changed.Continuity.Generation[0]++
	fake := &receiptDeleteClient{}
	fake.capabilityFn = func() (*pb.GetReceiptCapabilityResponse, error) {
		if fake.capabilityCalls == 1 {
			return testReceiptCapabilityProto(capability), nil
		}
		return testReceiptCapabilityProto(changed), nil
	}
	fake.deleteFn = func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("committed response lost"))
	}
	policy := RetryPolicy{
		MaxAttempts: 3, BaseDelay: 1, MaxDelay: 1,
		sleepFn: noSleep, randFn: fixedRand(0),
	}.normalized()
	l := &Lantern{client: fake, opts: options{retry: &policy}}
	_, err := l.DeleteEdgeWithReceipt(context.Background(), "tail", "head", receiptContext)
	var reconciliation *ReceiptReconciliationError
	if !errors.Is(err, ErrReceiptReconciliationRequired) || !errors.As(err, &reconciliation) {
		t.Fatalf("error = %v", err)
	}
	if fake.capabilityCalls != 2 || fake.deleteCalls != 1 {
		t.Fatalf("calls = capability %d delete %d", fake.capabilityCalls, fake.deleteCalls)
	}
	if reconciliation.Observed == nil || reconciliation.Observed.Generation != changed.Continuity.Generation {
		t.Fatalf("observed continuity = %+v", reconciliation.Observed)
	}
}

func TestDeleteEdgeWithReceiptUsesPluralWire(t *testing.T) {
	capability := testReceiptCapability(0x13)
	receiptContext := testReceiptContext(t, capability, 1, 0x23)
	fake := &receiptDeleteClient{
		capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
			return testReceiptCapabilityProto(capability), nil
		},
		deleteFn: func(request *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
			if len(request.GetEdges()) != 1 ||
				request.GetEdges()[0].GetTail() != "tail" ||
				request.GetEdges()[0].GetHead() != "head" {
				t.Fatalf("plural request = %+v", request)
			}
			return &pb.DeleteEdgesResponse{Deleted: 1, Existed: []bool{true}}, nil
		},
	}
	l := &Lantern{client: fake}
	result, err := l.DeleteEdgeWithReceipt(context.Background(), "tail", "head", receiptContext)
	if err != nil || !result.Existed || result.OperationID != receiptContext.OperationIDs[0] {
		t.Fatalf("result = (%+v, %v)", result, err)
	}
	if fake.deleteCalls != 1 {
		t.Fatalf("plural DeleteEdges calls = %d", fake.deleteCalls)
	}
}

func TestDeleteEdgesWithReceiptContinuityAndCapabilityFailures(t *testing.T) {
	capability := testReceiptCapability(0x14)
	receiptContext := testReceiptContext(t, capability, 1, 0x24)
	tests := []struct {
		name       string
		capability *pb.GetReceiptCapabilityResponse
		err        error
		reconcile  bool
	}{
		{"disabled", &pb.GetReceiptCapabilityResponse{}, nil, true},
		{"unavailable", nil, connect.NewError(connect.CodeUnavailable, errors.New("down")), false},
		{"changed epoch", testReceiptCapabilityProto(testReceiptCapability(0x41)), nil, true},
		{"changed node", func() *pb.GetReceiptCapabilityResponse {
			changed := testReceiptCapabilityProto(capability)
			changed.Endpoint.NodeId[0]++
			return changed
		}(), nil, true},
		{"changed generation", func() *pb.GetReceiptCapabilityResponse {
			changed := testReceiptCapabilityProto(capability)
			changed.Endpoint.Generation[0]++
			return changed
		}(), nil, true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &receiptDeleteClient{
				capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
					return test.capability, test.err
				},
				deleteFn: func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
					t.Fatal("delete transport called")
					return nil, nil
				},
			}
			l := &Lantern{client: fake}
			_, err := l.DeleteEdgeWithReceipt(context.Background(), "tail", "head", receiptContext)
			if test.reconcile {
				var typed *ReceiptReconciliationError
				if !errors.Is(err, ErrReceiptReconciliationRequired) || !errors.As(err, &typed) {
					t.Fatalf("error = %v", err)
				}
			} else if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error = %v, want ErrUnavailable", err)
			}
			if fake.deleteCalls != 0 {
				t.Fatalf("delete calls = %d", fake.deleteCalls)
			}
		})
	}
}

func TestDeleteEdgesWithReceiptPropagatesConflictAndRejectsBadResponse(t *testing.T) {
	capability := testReceiptCapability(0x15)
	receiptContext := testReceiptContext(t, capability, 2, 0x25)
	refs := []EdgeRef{{Tail: "a", Head: "b"}, {Tail: "c", Head: "d"}}
	t.Run("intent conflict", func(t *testing.T) {
		fake := &receiptDeleteClient{
			capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
				return testReceiptCapabilityProto(capability), nil
			},
			deleteFn: func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
				return nil, connect.NewError(connect.CodeInvalidArgument, errors.New("receipt intent conflict"))
			},
		}
		l := &Lantern{client: fake}
		if _, err := l.DeleteEdgesWithReceipt(context.Background(), refs, receiptContext); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("error = %v", err)
		}
		if fake.deleteCalls != 1 {
			t.Fatalf("delete calls = %d", fake.deleteCalls)
		}
	})

	t.Run("response alignment", func(t *testing.T) {
		responses := []*pb.DeleteEdgesResponse{
			{Deleted: 1, Existed: []bool{true}},
			{Deleted: 2, Existed: []bool{true, false}},
		}
		for i, response := range responses {
			fake := &receiptDeleteClient{
				capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
					return testReceiptCapabilityProto(capability), nil
				},
				deleteFn: func(*pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
					return response, nil
				},
			}
			l := &Lantern{client: fake}
			if _, err := l.DeleteEdgesWithReceipt(context.Background(), refs, receiptContext); !errors.Is(err, ErrReceiptProtocol) {
				t.Errorf("case %d error = %v", i, err)
			}
		}
	})
}

func TestDeleteEdgesWithReceiptContextSurvivesClientCredentialRotation(t *testing.T) {
	capability := testReceiptCapability(0x16)
	receiptContext := testReceiptContext(t, capability, 1, 0x26)
	var committed *pb.DeleteEdgesRequest
	oldClient := &receiptDeleteClient{
		capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
			return testReceiptCapabilityProto(capability), nil
		},
		deleteFn: func(request *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
			committed = proto.Clone(request).(*pb.DeleteEdgesRequest)
			return nil, connect.NewError(connect.CodeUnavailable, errors.New("response lost"))
		},
	}
	if _, err := (&Lantern{client: oldClient}).DeleteEdgeWithReceipt(
		context.Background(), "tail", "head", receiptContext,
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("old credential call error = %v", err)
	}

	newClient := &receiptDeleteClient{
		capabilityFn: func() (*pb.GetReceiptCapabilityResponse, error) {
			return testReceiptCapabilityProto(capability), nil
		},
		deleteFn: func(request *pb.DeleteEdgesRequest) (*pb.DeleteEdgesResponse, error) {
			if !proto.Equal(request, committed) {
				t.Fatalf("credential rotation changed receipt identity: got %v want %v", request, committed)
			}
			return &pb.DeleteEdgesResponse{Deleted: 1, Existed: []bool{true}}, nil
		},
	}
	result, err := (&Lantern{client: newClient}).DeleteEdgeWithReceipt(
		context.Background(), "tail", "head", receiptContext,
	)
	if err != nil || !result.Existed {
		t.Fatalf("new credential replay = (%+v, %v)", result, err)
	}
}
