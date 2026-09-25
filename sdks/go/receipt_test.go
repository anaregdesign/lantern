package client

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"io"
	"math"
	"reflect"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

func TestReceiptIdentityValidation(t *testing.T) {
	epoch := ReceiptEpoch{0x01}
	issuedAt := time.UnixMilli(1_700_000_000_123).UTC()
	var random [ReceiptOperationRandomSize]byte
	for i := range random {
		random[i] = byte(i + 1)
	}
	id, err := NewReceiptOperationID(epoch, issuedAt, random)
	if err != nil {
		t.Fatal(err)
	}
	if id[0] != receiptOperationIDVersion ||
		!bytes.Equal(id[1:17], epoch[:]) ||
		binary.BigEndian.Uint64(id[17:25]) != uint64(issuedAt.UnixMilli()) ||
		!bytes.Equal(id[25:], random[:]) {
		t.Fatalf("operation ID layout = %x", id)
	}
	decoded, err := ReceiptOperationIDFromBytes(id.Bytes())
	if err != nil || decoded != id {
		t.Fatalf("byte round trip = (%x, %v)", decoded, err)
	}
	parsed, err := ParseReceiptOperationID(id.String())
	if err != nil || parsed != id {
		t.Fatalf("hex round trip = (%x, %v)", parsed, err)
	}
	if got, err := id.Epoch(); err != nil || got != epoch {
		t.Fatalf("Epoch = (%x, %v)", got, err)
	}
	if got, err := id.IssuedAt(); err != nil || !got.Equal(issuedAt) {
		t.Fatalf("IssuedAt = (%v, %v)", got, err)
	}

	t.Run("reject malformed operation IDs", func(t *testing.T) {
		cases := [][]byte{
			nil,
			make([]byte, ReceiptOperationIDSize-1),
			make([]byte, ReceiptOperationIDSize+1),
			append([]byte{2}, id.Bytes()[1:]...),
		}
		zeroEpoch := id.Bytes()
		clear(zeroEpoch[1:17])
		cases = append(cases, zeroEpoch)
		zeroRandom := id.Bytes()
		clear(zeroRandom[25:])
		cases = append(cases, zeroRandom)
		badTime := id.Bytes()
		binary.BigEndian.PutUint64(badTime[17:25], uint64(math.MaxInt64)+1)
		cases = append(cases, badTime)
		for i, raw := range cases {
			if _, err := ReceiptOperationIDFromBytes(raw); !errors.Is(err, ErrInvalidReceipt) ||
				!errors.Is(err, ErrInvalidArgument) {
				t.Errorf("case %d error = %v", i, err)
			}
		}
	})

	t.Run("reject malformed fixed identities", func(t *testing.T) {
		if _, err := ReceiptGroupIDFromBytes(make([]byte, ReceiptGroupIDSize)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("zero group error = %v", err)
		}
		if _, err := ReceiptEpochFromBytes([]byte{1}); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("short epoch error = %v", err)
		}
		if _, err := ReceiptNodeIDFromBytes(make([]byte, ReceiptNodeIDSize)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("zero node error = %v", err)
		}
		if _, err := ReceiptGenerationFromBytes(make([]byte, ReceiptGenerationSize+1)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("long generation error = %v", err)
		}
		if _, err := ReceiptPolicyFingerprintFromBytes(make([]byte, ReceiptPolicyFingerprintSize)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("zero fingerprint error = %v", err)
		}
		if _, err := ParseReceiptGroupID(strings.Repeat("zz", ReceiptGroupIDSize)); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("invalid group hex error = %v", err)
		}
	})
}

func TestReceiptContextMintingAndValidation(t *testing.T) {
	capability := testReceiptCapability(0x10)
	now := time.UnixMilli(1_700_000_100_456).UTC()
	entropy := make([]byte, ReceiptGroupIDSize+2*ReceiptOperationRandomSize)
	for i := range entropy {
		entropy[i] = byte(i + 1)
	}
	l := &Lantern{receiptIDs: receiptIdentitySource{
		random: bytes.NewReader(entropy),
		now:    func() time.Time { return now },
	}}
	context, err := l.NewReceiptContext(capability, 2)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(context.GroupID[:], entropy[:ReceiptGroupIDSize]) {
		t.Fatalf("group = %x", context.GroupID)
	}
	for i, id := range context.OperationIDs {
		if id[0] != receiptOperationIDVersion ||
			!bytes.Equal(id[1:17], capability.Continuity.Epoch[:]) ||
			binary.BigEndian.Uint64(id[17:25]) != uint64(now.UnixMilli()) {
			t.Fatalf("operation %d layout = %x", i, id)
		}
		offset := ReceiptGroupIDSize + i*ReceiptOperationRandomSize
		if !bytes.Equal(id[25:], entropy[offset:offset+ReceiptOperationRandomSize]) {
			t.Fatalf("operation %d randomness = %x", i, id[25:])
		}
	}
	if err := context.Validate(2); err != nil {
		t.Fatal(err)
	}

	t.Run("JSON persistence preserves exact context", func(t *testing.T) {
		encoded, err := json.Marshal(context)
		if err != nil {
			t.Fatal(err)
		}
		var decoded ReceiptContext
		if err := json.Unmarshal(encoded, &decoded); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(decoded, context) {
			t.Fatalf("round trip = %+v, want %+v", decoded, context)
		}
	})

	t.Run("clone detaches operation slice", func(t *testing.T) {
		clone := context.Clone()
		clone.OperationIDs[0][0] = 0xff
		if context.OperationIDs[0][0] == 0xff {
			t.Fatal("Clone shared operation ID storage")
		}
	})

	t.Run("disabled capability", func(t *testing.T) {
		if _, err := l.NewReceiptContext(ReceiptCapability{}, 1); !errors.Is(err, ErrReceiptsDisabled) ||
			!errors.Is(err, ErrFailedPrecondition) {
			t.Fatalf("error = %v", err)
		}
		malformed := ReceiptCapability{Continuity: capability.Continuity}
		if _, err := l.NewReceiptContext(malformed, 1); !errors.Is(err, ErrInvalidReceipt) ||
			!errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("malformed disabled capability error = %v", err)
		}
	})

	t.Run("entropy failure", func(t *testing.T) {
		failing := &Lantern{receiptIDs: receiptIdentitySource{
			random: io.LimitReader(bytes.NewReader([]byte{1}), 1),
			now:    func() time.Time { return now },
		}}
		if _, err := failing.NewReceiptContext(capability, 1); err == nil {
			t.Fatal("expected entropy failure")
		}
	})

	t.Run("zero entropy", func(t *testing.T) {
		zero := &Lantern{receiptIDs: receiptIdentitySource{
			random: bytes.NewReader(make([]byte, ReceiptGroupIDSize+ReceiptOperationRandomSize)),
			now:    func() time.Time { return now },
		}}
		if _, err := zero.NewReceiptContext(capability, 1); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("error = %v", err)
		}
	})

	t.Run("mixed and duplicate context", func(t *testing.T) {
		mixed := context.Clone()
		other := testReceiptContext(t, testReceiptCapability(0x40), 1, 0x50)
		mixed.OperationIDs[1] = other.OperationIDs[0]
		if err := mixed.Validate(2); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("mixed error = %v", err)
		}
		duplicate := context.Clone()
		duplicate.OperationIDs[1] = duplicate.OperationIDs[0]
		if err := duplicate.Validate(2); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("duplicate error = %v", err)
		}
		if err := context.Validate(1); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("misaligned error = %v", err)
		}
	})
}

type receiptReadClient struct {
	graphv1connect.LanternServiceClient

	capability      *pb.GetReceiptCapabilityResponse
	capabilityErr   error
	capabilityCalls int
	statusesFn      func([][]byte) (*pb.GetReceiptStatusesResponse, error)
	statusCalls     int
	singularCalls   int
}

func (c *receiptReadClient) GetReceiptCapability(
	_ context.Context,
	_ *connect.Request[pb.GetReceiptCapabilityRequest],
) (*connect.Response[pb.GetReceiptCapabilityResponse], error) {
	c.capabilityCalls++
	if c.capabilityErr != nil {
		return nil, c.capabilityErr
	}
	return connect.NewResponse(c.capability), nil
}

func (c *receiptReadClient) GetReceiptStatuses(
	_ context.Context,
	request *connect.Request[pb.GetReceiptStatusesRequest],
) (*connect.Response[pb.GetReceiptStatusesResponse], error) {
	c.statusCalls++
	response, err := c.statusesFn(request.Msg.GetOperationIds())
	if err != nil {
		return nil, err
	}
	return connect.NewResponse(response), nil
}

func (c *receiptReadClient) GetReceiptStatus(
	context.Context,
	*connect.Request[pb.GetReceiptStatusRequest],
) (*connect.Response[pb.GetReceiptStatusResponse], error) {
	c.singularCalls++
	return nil, errors.New("singular wire RPC must not be called")
}

func TestReceiptCapabilityAndStatus(t *testing.T) {
	capability := testReceiptCapability(0x20)
	t.Run("enabled and disabled capability", func(t *testing.T) {
		fake := &receiptReadClient{capability: testReceiptCapabilityProto(capability)}
		l := &Lantern{client: fake}
		got, err := l.GetReceiptCapability(context.Background())
		if err != nil || got != capability {
			t.Fatalf("enabled capability = (%+v, %v), want %+v", got, err, capability)
		}
		fake.capability = &pb.GetReceiptCapabilityResponse{}
		got, err = l.GetReceiptCapability(context.Background())
		if err != nil || got != (ReceiptCapability{}) {
			t.Fatalf("disabled capability = (%+v, %v)", got, err)
		}
	})

	t.Run("malformed and unavailable capability", func(t *testing.T) {
		bad := testReceiptCapabilityProto(capability)
		bad.Endpoint.Generation = []byte{1}
		fake := &receiptReadClient{capability: bad}
		l := &Lantern{client: fake}
		if _, err := l.GetReceiptCapability(context.Background()); !errors.Is(err, ErrReceiptProtocol) {
			t.Fatalf("malformed error = %v", err)
		}
		fake.capabilityErr = connect.NewError(connect.CodeUnavailable, errors.New("down"))
		if _, err := l.GetReceiptCapability(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("unavailable error = %v", err)
		}
	})

	contextValue := testReceiptContext(t, capability, 4, 0x30)
	ids := contextValue.OperationIDs
	t.Run("three states preserve alignment and false result presence", func(t *testing.T) {
		fake := &receiptReadClient{}
		fake.statusesFn = func(rawIDs [][]byte) (*pb.GetReceiptStatusesResponse, error) {
			statuses := make([]*pb.ReceiptStatus, len(rawIDs))
			for i, raw := range rawIDs {
				id, err := ReceiptOperationIDFromBytes(raw)
				if err != nil {
					t.Fatal(err)
				}
				switch id {
				case ids[0]:
					statuses[i] = testConfirmedEdgeDeleteStatus(id, contextValue.GroupID, 0, 2, true)
				case ids[1]:
					statuses[i] = testConfirmedEdgeDeleteStatus(id, contextValue.GroupID, 1, 2, false)
				case ids[2]:
					statuses[i] = &pb.ReceiptStatus{
						OperationId: id.Bytes(),
						State:       pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED,
					}
				case ids[3]:
					statuses[i] = &pb.ReceiptStatus{
						OperationId: id.Bytes(),
						State:       pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NO_LONGER_PROVABLE,
					}
				}
			}
			return &pb.GetReceiptStatusesResponse{Statuses: statuses}, nil
		}
		l := &Lantern{client: fake, opts: options{batchChunkSize: 2}}
		got, err := l.GetReceiptStatuses(context.Background(), []ReceiptOperationID{
			ids[0], ids[1], ids[2], ids[3], ids[0],
		})
		if err != nil {
			t.Fatal(err)
		}
		if fake.statusCalls != 3 || len(got) != 5 {
			t.Fatalf("calls/results = %d/%d", fake.statusCalls, len(got))
		}
		if got[0].State != ReceiptConfirmed || got[0].Receipt == nil || !got[0].Receipt.Existed ||
			got[1].State != ReceiptConfirmed || got[1].Receipt == nil || got[1].Receipt.Existed ||
			got[2].State != ReceiptNotYetObserved || got[2].Receipt != nil ||
			got[3].State != ReceiptNoLongerProvable || got[3].Receipt != nil ||
			got[4].OperationID != ids[0] {
			t.Fatalf("statuses = %+v", got)
		}
		one, err := l.GetReceiptStatus(context.Background(), ids[1])
		if err != nil || one.Receipt == nil || one.Receipt.Existed || fake.singularCalls != 0 {
			t.Fatalf("singular = (%+v, %v), wire singular calls=%d", one, err, fake.singularCalls)
		}
	})

	t.Run("malformed input is rejected before transport", func(t *testing.T) {
		fake := &receiptReadClient{statusesFn: func([][]byte) (*pb.GetReceiptStatusesResponse, error) {
			return &pb.GetReceiptStatusesResponse{}, nil
		}}
		l := &Lantern{client: fake}
		if _, err := l.GetReceiptStatuses(context.Background(), nil); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("empty error = %v", err)
		}
		if _, err := l.GetReceiptStatus(context.Background(), ReceiptOperationID{}); !errors.Is(err, ErrInvalidReceipt) {
			t.Fatalf("zero error = %v", err)
		}
		if fake.statusCalls != 0 {
			t.Fatalf("transport calls = %d", fake.statusCalls)
		}
	})

	t.Run("malformed response fails closed", func(t *testing.T) {
		cases := []func(ReceiptOperationID) *pb.ReceiptStatus{
			func(id ReceiptOperationID) *pb.ReceiptStatus {
				return &pb.ReceiptStatus{OperationId: ids[1].Bytes(), State: pb.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED}
			},
			func(id ReceiptOperationID) *pb.ReceiptStatus {
				return &pb.ReceiptStatus{OperationId: id.Bytes(), State: pb.MutationReceiptState_MUTATION_RECEIPT_STATE_UNSPECIFIED}
			},
			func(id ReceiptOperationID) *pb.ReceiptStatus {
				return &pb.ReceiptStatus{OperationId: id.Bytes(), State: pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED}
			},
			func(id ReceiptOperationID) *pb.ReceiptStatus {
				status := testConfirmedEdgeDeleteStatus(id, contextValue.GroupID, 0, 1, true)
				status.Receipt.OriginalResult = &pb.ReceiptResult{
					Result: &pb.ReceiptResult_PutVertexOutcome{PutVertexOutcome: pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE},
				}
				return status
			},
		}
		for i, makeStatus := range cases {
			fake := &receiptReadClient{statusesFn: func([][]byte) (*pb.GetReceiptStatusesResponse, error) {
				return &pb.GetReceiptStatusesResponse{Statuses: []*pb.ReceiptStatus{makeStatus(ids[0])}}, nil
			}}
			l := &Lantern{client: fake}
			if _, err := l.GetReceiptStatus(context.Background(), ids[0]); !errors.Is(err, ErrReceiptProtocol) {
				t.Errorf("case %d error = %v", i, err)
			}
		}
	})
}
