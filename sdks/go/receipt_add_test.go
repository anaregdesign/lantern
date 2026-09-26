package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"reflect"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

type edgeAddReceiptClient struct {
	graphv1connect.LanternServiceClient

	capability      *pb.GetReceiptCapabilityResponse
	capabilityCalls int
	addEdgesFn      func(*pb.AddEdgesRequest) (*pb.AddEdgesResponse, error)
	addEdgesCalls   int
	addEdgeCalls    int
	requests        []*pb.AddEdgesRequest
}

func (c *edgeAddReceiptClient) GetReceiptCapability(
	context.Context,
	*connect.Request[pb.GetReceiptCapabilityRequest],
) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	c.capabilityCalls++
	return connect.NewResponse(proto.Clone(c.capability).(*pb.GetReceiptCapabilityResponse)), nil
}

func (c *edgeAddReceiptClient) AddEdges(
	_ context.Context,
	request *connect.Request[pb.AddEdgesRequest],
) (*connect.Response[pb.AddEdgesResponse], error) {
	c.addEdgesCalls++
	c.requests = append(c.requests, proto.Clone(request.Msg).(*pb.AddEdgesRequest))
	response, err := c.addEdgesFn(request.Msg)
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *edgeAddReceiptClient) AddEdge(
	context.Context,
	*connect.Request[pb.AddEdgeRequest],
) (*connect.Response[pb.AddEdgeResponse], error) {
	c.addEdgeCalls++
	return nil, errors.New("singular Edge Add RPC must not be called")
}

func TestContribIDPersistenceAndDeterministicMinting(t *testing.T) {
	raw := bytes.Repeat([]byte{0x4a}, ContribIDSize)
	id, err := ContribIDFromBytes(raw)
	if err != nil {
		t.Fatal(err)
	}
	raw[0] = 0
	if id[0] != 0x4a {
		t.Fatal("ContribID retained caller-owned bytes")
	}
	copied := id.Bytes()
	copied[0] = 0
	if id[0] != 0x4a {
		t.Fatal("ContribID.Bytes returned shared storage")
	}
	parsed, err := ParseContribID(id.String())
	if err != nil {
		t.Fatal(err)
	}
	if parsed != id {
		t.Fatalf("parsed ID = %v, want %v", parsed, id)
	}
	text, err := id.MarshalText()
	if err != nil {
		t.Fatal(err)
	}
	var decoded ContribID
	if err := decoded.UnmarshalText(text); err != nil {
		t.Fatal(err)
	}
	if decoded != id {
		t.Fatalf("decoded ID = %v, want %v", decoded, id)
	}
	encodedJSON, err := json.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	var decodedJSON ContribID
	if err := json.Unmarshal(encodedJSON, &decodedJSON); err != nil {
		t.Fatal(err)
	}
	if decodedJSON != id {
		t.Fatalf("JSON decoded ID = %v, want %v", decodedJSON, id)
	}
	if got := ReceiptMutationAddEdge.String(); got != "ADD_EDGE" {
		t.Fatalf("Add mutation String = %q", got)
	}

	for _, raw := range [][]byte{
		nil,
		make([]byte, ContribIDSize-1),
		make([]byte, ContribIDSize),
	} {
		if _, err := ContribIDFromBytes(raw); !errors.Is(err, ErrInvalidReceipt) {
			t.Errorf("ContribIDFromBytes(%d bytes) error = %v", len(raw), err)
		}
	}
	if _, err := ParseContribID("not-hex"); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("ParseContribID error = %v", err)
	}

	entropy := testContribID(0x31)
	l := &Lantern{receiptIDs: receiptIdentitySource{random: bytes.NewReader(entropy[:])}}
	minted, err := l.NewContribID()
	if err != nil {
		t.Fatal(err)
	}
	if minted != entropy {
		t.Fatalf("minted ID = %v, want %v", minted, entropy)
	}
	l.receiptIDs.random = bytes.NewReader(make([]byte, ContribIDSize))
	if _, err := l.NewContribID(); !errors.Is(err, ErrInvalidReceipt) {
		t.Fatalf("zero entropy error = %v", err)
	}
}

func TestAddEdgesWithReceiptReplaysByteIdenticalRequest(t *testing.T) {
	capability := testReceiptCapability(0xa1)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		3,
		0xa2,
	)
	inputs := []EdgeAddReceiptInput{
		{
			Edge: EdgeInput{
				Tail: "a", Head: "b", Weight: 1.25,
				Expiration: capability.ServerTime.Add(time.Hour),
			},
			ContribID: testContribID(0x11),
		},
		{
			Edge:      EdgeInput{Tail: "a", Head: "b", Weight: 2.5},
			ContribID: testContribID(0x21),
		},
		{
			Edge: EdgeInput{
				Tail: "c", Head: "d", Weight: -0.5,
				Expiration: capability.ServerTime.Add(-time.Hour),
			},
			ContribID: testContribID(0x31),
		},
	}
	fake := &edgeAddReceiptClient{capability: testReceiptCapabilityProto(capability)}
	fake.addEdgesFn = func(*pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
		if fake.addEdgesCalls == 1 {
			inputs[0].Edge.Tail = "mutated-after-send"
			return nil, connect.NewError(
				connect.CodeUnavailable,
				errors.New("committed response lost"),
			)
		}
		return &pb.AddEdgesResponse{
			Written:          3,
			EffectiveWeights: []float32{1.25, 3.75, -0.5},
		}, nil
	}
	l := &Lantern{
		client: fake,
		opts:   options{retry: testRetryPolicy(2)},
	}

	results, err := l.AddEdgesWithReceipt(context.Background(), inputs, receiptContext)
	if err != nil {
		t.Fatal(err)
	}
	if fake.capabilityCalls != 2 || fake.addEdgesCalls != 2 {
		t.Fatalf(
			"capability/mutation calls = %d/%d, want 2/2",
			fake.capabilityCalls,
			fake.addEdgesCalls,
		)
	}
	if len(fake.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(fake.requests))
	}
	firstRequest, err := proto.Marshal(fake.requests[0])
	if err != nil {
		t.Fatal(err)
	}
	secondRequest, err := proto.Marshal(fake.requests[1])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstRequest, secondRequest) {
		t.Fatalf(
			"replayed request bytes differ:\nfirst=%x\nsecond=%x",
			firstRequest,
			secondRequest,
		)
	}
	if got := fake.requests[0].GetEdges()[1].GetExpiration(); got != nil {
		t.Fatalf("permanent receipt Add expiration = %v, want omitted", got)
	}
	want := []EdgeAddReceiptResult{
		{
			Edge:      EdgeRef{Tail: "a", Head: "b"},
			ContribID: inputs[0].ContribID, OperationID: receiptContext.OperationIDs[0],
			EffectiveWeight: 1.25,
		},
		{
			Edge:      EdgeRef{Tail: "a", Head: "b"},
			ContribID: inputs[1].ContribID, OperationID: receiptContext.OperationIDs[1],
			EffectiveWeight: 3.75,
		},
		{
			Edge:      EdgeRef{Tail: "c", Head: "d"},
			ContribID: inputs[2].ContribID, OperationID: receiptContext.OperationIDs[2],
			EffectiveWeight: -0.5,
		},
	}
	if !reflect.DeepEqual(results, want) {
		t.Fatalf("results = %+v, want %+v", results, want)
	}
	if got := fake.requests[0].GetContribIds(); !bytes.Equal(got[0], inputs[0].ContribID.Bytes()) ||
		!bytes.Equal(got[1], inputs[1].ContribID.Bytes()) ||
		!bytes.Equal(got[2], inputs[2].ContribID.Bytes()) {
		t.Fatalf("contribution IDs = %x", got)
	}
}

func TestAddEdgesWithReceiptPreservesExactResultBits(t *testing.T) {
	capability := testReceiptCapability(0xa3)
	weightBits := []uint32{
		0,
		0x80000000,
		0x7f800000,
		0xff800000,
		0x7fc00001,
	}
	inputs := make([]EdgeAddReceiptInput, len(weightBits))
	for i := range inputs {
		inputs[i] = EdgeAddReceiptInput{
			Edge: EdgeInput{
				Tail: fmt.Sprintf("tail-%d", i),
				Head: fmt.Sprintf("head-%d", i),
			},
			ContribID: testContribID(byte(0x41 + i)),
		}
	}
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		len(inputs),
		0xa4,
	)
	fake := &edgeAddReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		addEdgesFn: func(*pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
			weights := make([]float32, len(weightBits))
			for i, bits := range weightBits {
				weights[i] = math.Float32frombits(bits)
			}
			return &pb.AddEdgesResponse{
				Written:          int32(len(weights)),
				EffectiveWeights: weights,
			}, nil
		},
	}
	results, err := (&Lantern{client: fake}).AddEdgesWithReceipt(
		context.Background(),
		inputs,
		receiptContext,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != len(weightBits) {
		t.Fatalf("result count = %d, want %d", len(results), len(weightBits))
	}
	for i, result := range results {
		if got := math.Float32bits(result.EffectiveWeight); got != weightBits[i] {
			t.Fatalf("result[%d] effective weight bits = %08x, want %08x", i, got, weightBits[i])
		}
	}
}

func TestAddEdgeWithReceiptRelativeTTLIsStableAcrossInvocations(t *testing.T) {
	capability := testReceiptCapability(0xa3)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		1,
		0xa4,
	)
	fake := &edgeAddReceiptClient{capability: testReceiptCapabilityProto(capability)}
	fake.addEdgesFn = func(*pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
		return &pb.AddEdgesResponse{Written: 1, EffectiveWeights: []float32{2}}, nil
	}
	l := &Lantern{client: fake}
	contribID := testContribID(0x41)
	const ttl = 15 * time.Minute
	for range 2 {
		if _, err := l.AddEdgeWithReceipt(
			context.Background(),
			"stable",
			"ttl",
			2,
			ttl,
			contribID,
			receiptContext,
		); err != nil {
			t.Fatal(err)
		}
	}
	if len(fake.requests) != 2 {
		t.Fatalf("request count = %d, want 2", len(fake.requests))
	}
	firstRequest, err := proto.Marshal(fake.requests[0])
	if err != nil {
		t.Fatal(err)
	}
	secondRequest, err := proto.Marshal(fake.requests[1])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(firstRequest, secondRequest) {
		t.Fatalf(
			"cross-invocation replay changed request bytes:\nfirst=%x\nsecond=%x",
			firstRequest,
			secondRequest,
		)
	}
	issuedAt, err := receiptContext.OperationIDs[0].IssuedAt()
	if err != nil {
		t.Fatal(err)
	}
	if got := fake.requests[0].GetEdges()[0].GetExpiration().AsTime(); !got.Equal(issuedAt.Add(ttl)) {
		t.Fatalf("expiration = %v, want %v", got, issuedAt.Add(ttl))
	}
	if fake.addEdgeCalls != 0 {
		t.Fatalf("singular AddEdge RPC calls = %d", fake.addEdgeCalls)
	}
}

func TestAddEdgeWithReceiptPersistsAcrossClientRestart(t *testing.T) {
	capability := testReceiptCapability(0xad)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		1,
		0xae,
	)
	call := struct {
		Inputs  []EdgeAddReceiptInput
		Context ReceiptContext
	}{
		Inputs: []EdgeAddReceiptInput{{
			Edge: EdgeInput{
				Tail: "restart", Head: "edge", Weight: 0,
				Expiration: capability.ServerTime.Add(time.Hour),
			},
			ContribID: testContribID(0xb1),
		}},
		Context: receiptContext,
	}
	persisted, err := json.Marshal(call)
	if err != nil {
		t.Fatal(err)
	}

	var firstRequest []byte
	firstClient := &edgeAddReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		addEdgesFn: func(request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
			encoded, marshalErr := proto.Marshal(request)
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			firstRequest = encoded
			return nil, connect.NewError(
				connect.CodeUnavailable,
				errors.New("committed response lost before app restart"),
			)
		},
	}
	if _, err := (&Lantern{client: firstClient}).AddEdgesWithReceipt(
		context.Background(),
		call.Inputs,
		call.Context,
	); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("first client error = %v", err)
	}

	var restored struct {
		Inputs  []EdgeAddReceiptInput
		Context ReceiptContext
	}
	if err := json.Unmarshal(persisted, &restored); err != nil {
		t.Fatal(err)
	}
	secondClient := &edgeAddReceiptClient{
		capability: testReceiptCapabilityProto(capability),
		addEdgesFn: func(request *pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
			replayed, err := proto.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(replayed, firstRequest) {
				t.Fatalf(
					"restored request bytes differ:\nfirst=%x\nrestored=%x",
					firstRequest,
					replayed,
				)
			}
			return &pb.AddEdgesResponse{
				Written:          1,
				EffectiveWeights: []float32{0},
			}, nil
		},
	}
	results, err := (&Lantern{client: secondClient}).AddEdgesWithReceipt(
		context.Background(),
		restored.Inputs,
		restored.Context,
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 ||
		results[0].ContribID != call.Inputs[0].ContribID ||
		results[0].OperationID != receiptContext.OperationIDs[0] ||
		math.Float32bits(results[0].EffectiveWeight) != 0 {
		t.Fatalf("restored result = %+v", results)
	}
}

func TestAddEdgesWithReceiptRejectsMalformedInputsBeforeTransport(t *testing.T) {
	capability := testReceiptCapability(0xa5)
	one := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		1,
		0xa6,
	)
	two := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		2,
		0xa7,
	)
	valid := EdgeAddReceiptInput{
		Edge:      EdgeInput{Tail: "a", Head: "b", Weight: 1},
		ContribID: testContribID(0x51),
	}
	tests := []struct {
		name    string
		inputs  []EdgeAddReceiptInput
		context ReceiptContext
	}{
		{name: "empty batch", context: ReceiptContext{}},
		{name: "misaligned context", inputs: []EdgeAddReceiptInput{valid}, context: two},
		{
			name:   "wrong mutation",
			inputs: []EdgeAddReceiptInput{valid},
			context: testReceiptContextForMutation(
				t,
				capability,
				ReceiptMutationDeleteEdge,
				1,
				0xa8,
			),
		},
		{
			name:    "zero contribution ID",
			inputs:  []EdgeAddReceiptInput{{Edge: valid.Edge}},
			context: one,
		},
		{
			name: "duplicate contribution IDs",
			inputs: []EdgeAddReceiptInput{
				valid,
				{Edge: EdgeInput{Tail: "c", Head: "d", Weight: 2}, ContribID: valid.ContribID},
			},
			context: two,
		},
		{
			name: "empty edge",
			inputs: []EdgeAddReceiptInput{{
				Edge: EdgeInput{Head: "b", Weight: 1}, ContribID: valid.ContribID,
			}},
			context: one,
		},
		{
			name: "invalid UTF-8",
			inputs: []EdgeAddReceiptInput{{
				Edge: EdgeInput{
					Tail: string([]byte{0xff}), Head: "b", Weight: 1,
				},
				ContribID: valid.ContribID,
			}},
			context: one,
		},
		{
			name: "NaN weight",
			inputs: []EdgeAddReceiptInput{{
				Edge: EdgeInput{
					Tail: "a", Head: "b", Weight: float32(math.NaN()),
				},
				ContribID: valid.ContribID,
			}},
			context: one,
		},
		{
			name: "positive infinite weight",
			inputs: []EdgeAddReceiptInput{{
				Edge: EdgeInput{
					Tail: "a", Head: "b", Weight: float32(math.Inf(1)),
				},
				ContribID: valid.ContribID,
			}},
			context: one,
		},
		{
			name: "negative infinite weight",
			inputs: []EdgeAddReceiptInput{{
				Edge: EdgeInput{
					Tail: "a", Head: "b", Weight: float32(math.Inf(-1)),
				},
				ContribID: valid.ContribID,
			}},
			context: one,
		},
		{
			name: "invalid expiration",
			inputs: []EdgeAddReceiptInput{{
				Edge: EdgeInput{
					Tail: "a", Head: "b", Weight: 1,
					Expiration: time.Date(10_000, 1, 1, 0, 0, 0, 0, time.UTC),
				},
				ContribID: valid.ContribID,
			}},
			context: one,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fake := &edgeAddReceiptClient{}
			l := &Lantern{client: fake}
			if _, err := l.AddEdgesWithReceipt(
				context.Background(),
				test.inputs,
				test.context,
			); !errors.Is(err, ErrInvalidReceipt) {
				t.Fatalf("error = %v", err)
			}
			if fake.capabilityCalls != 0 || fake.addEdgesCalls != 0 {
				t.Fatalf(
					"transport calls = capability %d Add %d",
					fake.capabilityCalls,
					fake.addEdgesCalls,
				)
			}
		})
	}
}

func TestAddEdgesWithReceiptPropagatesConflictAndRejectsBadResponse(t *testing.T) {
	capability := testReceiptCapability(0xa9)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		2,
		0xaa,
	)
	inputs := []EdgeAddReceiptInput{
		{
			Edge:      EdgeInput{Tail: "a", Head: "b", Weight: 1},
			ContribID: testContribID(0x61),
		},
		{
			Edge:      EdgeInput{Tail: "c", Head: "d", Weight: 2},
			ContribID: testContribID(0x71),
		},
	}
	t.Run("intent conflict", func(t *testing.T) {
		fake := &edgeAddReceiptClient{
			capability: testReceiptCapabilityProto(capability),
			addEdgesFn: func(*pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
				return nil, connect.NewError(
					connect.CodeInvalidArgument,
					errors.New("operation ID intent conflict"),
				)
			},
		}
		l := &Lantern{client: fake, opts: options{retry: testRetryPolicy(3)}}
		if _, err := l.AddEdgesWithReceipt(
			context.Background(),
			inputs,
			receiptContext,
		); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("error = %v", err)
		}
		if fake.addEdgesCalls != 1 {
			t.Fatalf("mutation calls = %d, want 1", fake.addEdgesCalls)
		}
	})

	for _, test := range []struct {
		name     string
		response *pb.AddEdgesResponse
	}{
		{
			name: "written count drift",
			response: &pb.AddEdgesResponse{
				Written: 1, EffectiveWeights: []float32{1, 2},
			},
		},
		{
			name: "effective weight drift",
			response: &pb.AddEdgesResponse{
				Written: 2, EffectiveWeights: []float32{1},
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake := &edgeAddReceiptClient{
				capability: testReceiptCapabilityProto(capability),
				addEdgesFn: func(*pb.AddEdgesRequest) (*pb.AddEdgesResponse, error) {
					return test.response, nil
				},
			}
			l := &Lantern{client: fake}
			if _, err := l.AddEdgesWithReceipt(
				context.Background(),
				inputs,
				receiptContext,
			); !errors.Is(err, ErrReceiptProtocol) {
				t.Fatalf("error = %v", err)
			}
		})
	}
}

func TestReceiptStatusDecodesExactEdgeAddResult(t *testing.T) {
	capability := testReceiptCapability(0xab)
	receiptContext := testReceiptContextForMutation(
		t,
		capability,
		ReceiptMutationAddEdge,
		1,
		0xac,
	)
	for name, weightBits := range map[string]uint32{
		"zero":              0,
		"negative zero":     0x80000000,
		"positive infinity": 0x7f800000,
		"negative infinity": 0xff800000,
		"NaN payload":       0x7fc00001,
	} {
		t.Run(name, func(t *testing.T) {
			status, err := receiptStatusFromProto(
				receiptContext.OperationIDs[0],
				&pb.ReceiptStatus{
					OperationId: receiptContext.OperationIDs[0].Bytes(),
					State:       pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED,
					Receipt: &pb.MutationReceipt{
						OperationId:    receiptContext.OperationIDs[0].Bytes(),
						LogicalCallId:  receiptContext.GroupID.Bytes(),
						ItemIndex:      0,
						ItemCount:      1,
						IntentSha256:   bytes.Repeat([]byte{1}, ReceiptIntentSHA256Size),
						DeadlineUnixMs: uint64(capability.ServerTime.Add(time.Hour).UnixMilli()),
						OriginalResult: &pb.ReceiptResult{
							Result: &pb.ReceiptResult_AddEdgeEffectiveWeight{
								AddEdgeEffectiveWeight: math.Float32frombits(weightBits),
							},
						},
					},
				},
			)
			if err != nil {
				t.Fatal(err)
			}
			result, ok := status.Receipt.OriginalResult.(ReceiptAddEdgeResult)
			if !ok {
				t.Fatalf("original result type = %T", status.Receipt.OriginalResult)
			}
			if result.MutationKind() != ReceiptMutationAddEdge {
				t.Fatalf("mutation kind = %s", result.MutationKind())
			}
			if got := math.Float32bits(result.EffectiveWeight); got != weightBits {
				t.Fatalf("effective weight bits = %08x, want %08x", got, weightBits)
			}
		})
	}
}
