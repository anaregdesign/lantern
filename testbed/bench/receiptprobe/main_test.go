package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"math"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
)

type fakeReceiptClient struct {
	putRequest          *graphv1.PutVertexRequest
	deleteVertexRequest *graphv1.DeleteVertexRequest
	deleteEdgeRequest   *graphv1.DeleteEdgeRequest
	addRequest          *graphv1.AddEdgeRequest
	statusRequest       *graphv1.GetReceiptStatusRequest
	statusResponse      *graphv1.GetReceiptStatusResponse
	putOutcome          graphv1.PutOutcome
	vertexExisted       bool
	edgeExisted         bool
	effectiveWeight     float32
	nilAdmission        bool
}

func (f *fakeReceiptClient) PutVertex(
	_ context.Context,
	request *connect.Request[graphv1.PutVertexRequest],
) (*connect.Response[graphv1.PutVertexResponse], error) {
	f.putRequest = request.Msg
	if f.nilAdmission {
		return nil, nil
	}
	return connect.NewResponse(&graphv1.PutVertexResponse{Outcome: f.putOutcome}), nil
}

func (f *fakeReceiptClient) DeleteVertex(
	_ context.Context,
	request *connect.Request[graphv1.DeleteVertexRequest],
) (*connect.Response[graphv1.DeleteVertexResponse], error) {
	f.deleteVertexRequest = request.Msg
	if f.nilAdmission {
		return nil, nil
	}
	return connect.NewResponse(&graphv1.DeleteVertexResponse{Existed: f.vertexExisted}), nil
}

func (f *fakeReceiptClient) DeleteEdge(
	_ context.Context,
	request *connect.Request[graphv1.DeleteEdgeRequest],
) (*connect.Response[graphv1.DeleteEdgeResponse], error) {
	f.deleteEdgeRequest = request.Msg
	if f.nilAdmission {
		return nil, nil
	}
	return connect.NewResponse(&graphv1.DeleteEdgeResponse{Existed: f.edgeExisted}), nil
}

func (f *fakeReceiptClient) AddEdge(
	_ context.Context,
	request *connect.Request[graphv1.AddEdgeRequest],
) (*connect.Response[graphv1.AddEdgeResponse], error) {
	f.addRequest = request.Msg
	if f.nilAdmission {
		return nil, nil
	}
	return connect.NewResponse(&graphv1.AddEdgeResponse{EffectiveWeight: f.effectiveWeight}), nil
}

func (f *fakeReceiptClient) GetReceiptStatus(
	_ context.Context,
	request *connect.Request[graphv1.GetReceiptStatusRequest],
) (*connect.Response[graphv1.GetReceiptStatusResponse], error) {
	f.statusRequest = request.Msg
	return connect.NewResponse(f.statusResponse), nil
}

func newFakeReceiptClient(operation receiptOperation) *fakeReceiptClient {
	return &fakeReceiptClient{
		putOutcome:      graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		effectiveWeight: math.Float32frombits(operation.expectedAddWeightBits),
		statusResponse:  confirmedResponse(operation),
	}
}

func TestExecuteOperationReusesIDAndValidatesConfirmedReceipt(t *testing.T) {
	t.Parallel()

	for _, family := range receiptFamilies() {
		t.Run(string(family), func(t *testing.T) {
			operation := testReceiptOperation(t, family)
			fake := newFakeReceiptClient(operation)
			admission, lookup, lookedUp := executeOperation(
				context.Background(), "token", time.Second, fake, operation,
			)
			if admission.status != "OK" || lookup.status != "OK" || !lookedUp {
				t.Fatalf("executeOperation() = admission %+v, lookup %+v, lookedUp %t",
					admission, lookup, lookedUp)
			}
			var receiptContext *graphv1.MutationReceiptContext
			switch family {
			case receiptVertexPut:
				if !fake.putRequest.GetIfAbsent() || fake.putRequest.GetVertex().GetKey() == "" ||
					fake.putRequest.GetVertex().GetString_() != "receipt-bench" {
					t.Fatalf("conditional PutVertex = %+v", fake.putRequest)
				}
				receiptContext = fake.putRequest.GetReceiptContext()
			case receiptVertexDelete:
				if fake.deleteVertexRequest.GetKey() == "" {
					t.Fatalf("absent DeleteVertex = %+v", fake.deleteVertexRequest)
				}
				receiptContext = fake.deleteVertexRequest.GetReceiptContext()
			case receiptEdgeDelete:
				if fake.deleteEdgeRequest.GetTail() == "" || fake.deleteEdgeRequest.GetHead() == "" {
					t.Fatalf("absent DeleteEdge = %+v", fake.deleteEdgeRequest)
				}
				receiptContext = fake.deleteEdgeRequest.GetReceiptContext()
			case receiptEdgeAdd:
				if len(fake.addRequest.GetContribId()) != 24 ||
					math.IsInf(float64(fake.addRequest.GetEdge().GetWeight()), 0) ||
					math.IsNaN(float64(fake.addRequest.GetEdge().GetWeight())) {
					t.Fatalf("non-finite or missing contribution = %+v", fake.addRequest)
				}
				receiptContext = fake.addRequest.GetReceiptContext()
			default:
				t.Fatalf("unexpected family %q", family)
			}
			if !bytes.Equal(receiptContext.GetOperationIds()[0], operation.operationID) ||
				!bytes.Equal(fake.statusRequest.GetOperationId(), operation.operationID) {
				t.Fatal("admission and lookup did not reuse the generated operation ID")
			}
		})
	}
}

func TestExecuteOperationRejectsMismatchedConfirmedReceipt(t *testing.T) {
	t.Parallel()

	for _, family := range receiptFamilies() {
		t.Run(string(family), func(t *testing.T) {
			operation := testReceiptOperation(t, family)
			for _, corruption := range []string{
				"status ID", "receipt ID", "call ID", "digest",
				"deadline", "coordinates", "state", "result arm", "result value", "missing result",
			} {
				t.Run(corruption, func(t *testing.T) {
					fake := newFakeReceiptClient(operation)
					switch corruption {
					case "status ID":
						fake.statusResponse.Status.OperationId = append([]byte(nil), operation.operationID...)
						fake.statusResponse.Status.OperationId[0] ^= 0xff
					case "receipt ID":
						fake.statusResponse.Status.Receipt.OperationId = append([]byte(nil), operation.operationID...)
						fake.statusResponse.Status.Receipt.OperationId[0] ^= 0xff
					case "call ID":
						fake.statusResponse.Status.Receipt.LogicalCallId = append([]byte(nil), operation.logicalCallID...)
						fake.statusResponse.Status.Receipt.LogicalCallId[0] ^= 0xff
					case "digest":
						fake.statusResponse.Status.Receipt.IntentSha256[0] ^= 0xff
					case "deadline":
						fake.statusResponse.Status.Receipt.DeadlineUnixMs++
					case "coordinates":
						fake.statusResponse.Status.Receipt.ItemCount++
					case "state":
						fake.statusResponse.Status.State = graphv1.MutationReceiptState_MUTATION_RECEIPT_STATE_NOT_YET_OBSERVED
					case "result arm":
						result := &graphv1.ReceiptResult{
							Result: &graphv1.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: false},
						}
						if family == receiptEdgeDelete {
							result.Result = &graphv1.ReceiptResult_PutVertexOutcome{
								PutVertexOutcome: graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
							}
						}
						fake.statusResponse.Status.Receipt.OriginalResult = result
					case "result value":
						switch result := fake.statusResponse.Status.Receipt.OriginalResult.Result.(type) {
						case *graphv1.ReceiptResult_PutVertexOutcome:
							result.PutVertexOutcome = graphv1.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET
						case *graphv1.ReceiptResult_DeleteVertexExisted:
							result.DeleteVertexExisted = true
						case *graphv1.ReceiptResult_DeleteEdgeExisted:
							result.DeleteEdgeExisted = true
						case *graphv1.ReceiptResult_AddEdgeEffectiveWeight:
							result.AddEdgeEffectiveWeight = 0
						}
					case "missing result":
						fake.statusResponse.Status.Receipt.OriginalResult = nil
					}
					admission, lookup, lookedUp := executeOperation(
						context.Background(), "token", time.Second, fake, operation,
					)
					if admission.status != "OK" || lookup.status != "DataLoss" || !lookedUp {
						t.Fatalf("executeOperation() = admission %+v, lookup %+v, lookedUp %t",
							admission, lookup, lookedUp)
					}
				})
			}
		})
	}
}

func TestExecuteOperationRejectsInvalidAdmission(t *testing.T) {
	t.Parallel()
	for _, family := range receiptFamilies() {
		t.Run(string(family), func(t *testing.T) {
			operation := testReceiptOperation(t, family)
			for _, missing := range []bool{false, true} {
				fake := newFakeReceiptClient(operation)
				if missing {
					fake.nilAdmission = true
				} else {
					fake.putOutcome = graphv1.PutOutcome_PUT_OUTCOME_CONDITION_NOT_MET
					fake.vertexExisted = true
					fake.edgeExisted = true
					fake.effectiveWeight = 0
				}
				admission, _, lookedUp := executeOperation(
					context.Background(), "token", time.Second, fake, operation,
				)
				if admission.status != "DataLoss" || lookedUp || fake.statusRequest != nil {
					t.Fatalf("invalid admission (missing=%t) = %+v, lookedUp=%t",
						missing, admission, lookedUp)
				}
			}
		})
	}
}

func TestExecuteOperationPreservesNonFiniteAddResultBits(t *testing.T) {
	t.Parallel()
	for _, bits := range []uint32{0x7f800000, 0xff800000, 0x7fc00001} {
		t.Run(hex.EncodeToString([]byte{
			byte(bits >> 24), byte(bits >> 16), byte(bits >> 8), byte(bits),
		}), func(t *testing.T) {
			operation := testReceiptOperation(t, receiptEdgeAdd)
			operation.expectedAddWeightBits = bits
			fake := newFakeReceiptClient(operation)
			admission, lookup, lookedUp := executeOperation(
				context.Background(), "token", time.Second, fake, operation,
			)
			if admission.status != "OK" || lookup.status != "OK" || !lookedUp {
				t.Fatalf("non-finite original bits %08x = %+v, %+v, %t", bits, admission, lookup, lookedUp)
			}
		})
	}
}

func TestReceiptIntentDigestsMatchCanonicalServerEncoding(t *testing.T) {
	t.Parallel()
	contribID := bytes.Repeat([]byte{0xa5}, len(mutationreceipt.ContribID{}))
	for _, tc := range []struct {
		name      string
		canonical string
		digest    func() ([sha256.Size]byte, error)
	}{
		{
			name:      "conditional Vertex Put",
			canonical: "01" + "0000000000000001" + "76" + "11" + "0000000000000001" + "78" + "0001",
			digest: func() ([sha256.Size]byte, error) {
				return vertexPutIntentSHA256(&graphv1.Vertex{
					Key: "v", Value: &graphv1.Vertex_String_{String_: "x"},
				})
			},
		},
		{
			name:      "absent Vertex Delete",
			canonical: "04" + "0000000000000001" + "76",
			digest: func() ([sha256.Size]byte, error) {
				return vertexDeleteIntentSHA256("v"), nil
			},
		},
		{
			name:      "absent Edge Delete",
			canonical: "05" + "0000000000000001" + "74" + "0000000000000001" + "68",
			digest: func() ([sha256.Size]byte, error) {
				return edgeDeleteIntentSHA256("t", "h"), nil
			},
		},
		{
			name: "contribution-keyed Edge Add",
			canonical: "03" + "0000000000000001" + "74" + "0000000000000001" + "68" +
				"3fa00000" + "00" + "0000000000000018" + strings.Repeat("a5", 24),
			digest: func() ([sha256.Size]byte, error) {
				return edgeAddIntentSHA256(&graphv1.Edge{Tail: "t", Head: "h", Weight: 1.25}, contribID)
			},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			canonical, err := hex.DecodeString(tc.canonical)
			if err != nil {
				t.Fatal(err)
			}
			got, err := tc.digest()
			if err != nil {
				t.Fatal(err)
			}
			if want := mutationreceipt.IntentDigest(canonical); got != want {
				t.Fatalf("canonical intent digest = %x, want %x", got, want)
			}
		})
	}
}

func TestReceiptProbeRejectsUnsupportedDigestInputs(t *testing.T) {
	t.Parallel()
	if _, err := vertexPutIntentSHA256(&graphv1.Vertex{
		Key: "v", Value: &graphv1.Vertex_Int32{Int32: 1},
	}); err == nil {
		t.Fatal("Put digest accepted a value outside the selected string workload")
	}
	for _, tc := range []struct {
		name      string
		edge      *graphv1.Edge
		contribID []byte
	}{
		{"nonfinite source", &graphv1.Edge{Tail: "t", Head: "h", Weight: float32(math.Inf(1))}, bytes.Repeat([]byte{1}, 24)},
		{"missing contribution", &graphv1.Edge{Tail: "t", Head: "h", Weight: 1}, nil},
		{"zero contribution", &graphv1.Edge{Tail: "t", Head: "h", Weight: 1}, make([]byte, 24)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := edgeAddIntentSHA256(tc.edge, tc.contribID); err == nil {
				t.Fatal("Add digest accepted an invalid source or contribution")
			}
		})
	}
}

func TestReceiptEdgeAddPairsUseOneEdgeAndDistinctContributions(t *testing.T) {
	t.Parallel()
	endpoint := testReceiptEndpoint()
	nonce := [16]byte{1}
	first, err := newReceiptOperation(&endpoint, receiptEdgeAdd, nonce, 7, "steady")
	if err != nil {
		t.Fatal(err)
	}
	second, err := newReceiptOperation(&endpoint, receiptEdgeAdd, nonce, 8, "steady")
	if err != nil {
		t.Fatal(err)
	}
	if first.addEdgeRequest.GetEdge().GetTail() != second.addEdgeRequest.GetEdge().GetTail() ||
		!bytes.Equal(first.addEdgeRequest.GetContribId(), first.operationID[25:]) ||
		bytes.Equal(first.addEdgeRequest.GetContribId(), second.addEdgeRequest.GetContribId()) ||
		first.intentSHA256 == second.intentSHA256 {
		t.Fatal("Add pair did not share an edge with distinct 24-byte contribution intents")
	}
}

func TestSummarizeProducesGhzCompatiblePercentiles(t *testing.T) {
	t.Parallel()

	result := summarize([]rpcSample{
		{latency: time.Millisecond, status: "OK"},
		{latency: 2 * time.Millisecond, status: "OK"},
		{latency: 3 * time.Millisecond, status: "Unavailable"},
		{latency: 4 * time.Millisecond, status: "OK"},
	}, time.Second)

	if result.Count != 4 || result.RPS != 4 {
		t.Fatalf("summarize() count/rps = %d/%v, want 4/4", result.Count, result.RPS)
	}
	if result.Fastest != time.Millisecond || result.Slowest != 4*time.Millisecond {
		t.Fatalf(
			"summarize() fastest/slowest = %v/%v",
			result.Fastest,
			result.Slowest,
		)
	}
	if got := result.StatusCodeDistribution["Unavailable"]; got != 1 {
		t.Fatalf("Unavailable count = %d, want 1", got)
	}
	if len(result.LatencyDistribution) != 5 {
		t.Fatalf("latency distribution length = %d, want 5", len(result.LatencyDistribution))
	}
	if got := result.LatencyDistribution[len(result.LatencyDistribution)-1]; got.Percentage != 99 || got.Latency != 4*time.Millisecond {
		t.Fatalf("p99 = %+v, want 4ms", got)
	}
}

func TestConnectStatusNameUnknownFailsClosed(t *testing.T) {
	t.Parallel()

	if got := connectStatusName(connect.Code(255)); got != "Unknown" {
		t.Fatalf("unrecognized error code = %q, want Unknown", got)
	}
}

func TestValidateEndpointSetRejectsDuplicateIdentityAndPolicyDrift(t *testing.T) {
	t.Parallel()

	first := testReceiptEndpoint()
	first.address = "http://first"
	second := testReceiptEndpoint()
	second.address = "http://second"
	if err := validateEndpointSet([]receiptEndpoint{first, second}, receiptEdgeDelete); err == nil {
		t.Fatal("validateEndpointSet() accepted duplicate node IDs")
	}

	second.capability.Endpoint.NodeId[0] ^= 0xff
	second.capability.Policy.Fingerprint[0] ^= 0xff
	if err := validateEndpointSet([]receiptEndpoint{first, second}, receiptEdgeDelete); err == nil {
		t.Fatal("validateEndpointSet() accepted divergent receipt policy")
	}
}

func TestValidateEndpointSetRequiresSelectedMutationOnEveryReplica(t *testing.T) {
	t.Parallel()
	first := testReceiptEndpoint()
	first.address = "http://first"
	second := testReceiptEndpoint()
	second.address = "http://second"
	second.capability.Endpoint.NodeId[0] ^= 0xff
	for _, family := range receiptFamilies() {
		if err := validateEndpointSet([]receiptEndpoint{first, second}, family); err != nil {
			t.Fatalf("%s supported by both replicas: %v", family, err)
		}
		kind, err := family.mutationKind()
		if err != nil {
			t.Fatal(err)
		}
		second.capability.SupportedMutations = []graphv1.ReceiptMutationKind{kind}
		if err := validateEndpointSet([]receiptEndpoint{first, second}, family); err != nil {
			t.Fatalf("%s explicitly supported by second replica: %v", family, err)
		}
		second.capability.SupportedMutations = nil
		if err := validateEndpointSet([]receiptEndpoint{first, second}, family); err == nil {
			t.Fatalf("%s without second-replica capability was accepted", family)
		}
		second.capability.SupportedMutations = first.capability.GetSupportedMutations()
		first.capability.SupportedMutations = nil
		if err := validateEndpointSet([]receiptEndpoint{first, second}, family); err == nil {
			t.Fatalf("%s without first-replica capability was accepted", family)
		}
		first.capability.SupportedMutations = testReceiptEndpoint().capability.GetSupportedMutations()
	}
	if _, err := receiptFamily("receipt_unknown").mutationKind(); err == nil {
		t.Fatal("unknown receipt family was accepted")
	}
}

func receiptFamilies() []receiptFamily {
	return []receiptFamily{
		receiptVertexPut, receiptVertexDelete, receiptEdgeDelete, receiptEdgeAdd,
	}
}

func testReceiptOperation(t *testing.T, family receiptFamily) receiptOperation {
	t.Helper()
	endpoint := testReceiptEndpoint()
	nonce := [16]byte{1}
	operation, err := newReceiptOperation(&endpoint, family, nonce, 7, "steady")
	if err != nil {
		t.Fatalf("newReceiptOperation(%s): %v", family, err)
	}
	return operation
}

func testReceiptEndpoint() receiptEndpoint {
	epoch := bytes.Repeat([]byte{0x42}, len(mutationreceipt.Epoch{}))
	nodeID := bytes.Repeat([]byte{0x24}, len(mutationreceipt.Epoch{}))
	return receiptEndpoint{
		capability: &graphv1.GetReceiptCapabilityResponse{
			Enabled: true,
			Policy: &graphv1.ReceiptPolicy{
				DeploymentEpoch: epoch,
				Fingerprint:     bytes.Repeat([]byte{0x52}, 32),
				RetentionMs:     uint64(time.Hour / time.Millisecond),
				MaxEntries:      100,
				MaxBytes:        1 << 20,
			},
			Endpoint: &graphv1.ReceiptEndpoint{
				NodeId:     nodeID,
				Generation: []byte{3},
			},
			ServerNowUnixMs: uint64(time.Now().UnixMilli()),
			SupportedMutations: []graphv1.ReceiptMutationKind{
				graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_PUT_VERTEX,
				graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_VERTEX,
				graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_DELETE_EDGE,
				graphv1.ReceiptMutationKind_RECEIPT_MUTATION_KIND_ADD_EDGE,
			},
		},
		issuedAt: time.Now().Add(-time.Second).Truncate(time.Millisecond),
	}
}

func confirmedResponse(operation receiptOperation) *graphv1.GetReceiptStatusResponse {
	result := &graphv1.ReceiptResult{}
	switch operation.family {
	case receiptVertexPut:
		result.Result = &graphv1.ReceiptResult_PutVertexOutcome{
			PutVertexOutcome: graphv1.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE,
		}
	case receiptVertexDelete:
		result.Result = &graphv1.ReceiptResult_DeleteVertexExisted{DeleteVertexExisted: false}
	case receiptEdgeDelete:
		result.Result = &graphv1.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: false}
	case receiptEdgeAdd:
		result.Result = &graphv1.ReceiptResult_AddEdgeEffectiveWeight{
			AddEdgeEffectiveWeight: math.Float32frombits(operation.expectedAddWeightBits),
		}
	}
	return &graphv1.GetReceiptStatusResponse{
		Status: &graphv1.ReceiptStatus{
			OperationId: operation.operationID,
			State:       graphv1.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED,
			Receipt: &graphv1.MutationReceipt{
				OperationId:    operation.operationID,
				LogicalCallId:  operation.logicalCallID,
				IntentSha256:   operation.intentSHA256[:],
				DeadlineUnixMs: operation.deadlineUnixMS,
				ItemIndex:      operation.expectedItem,
				ItemCount:      operation.expectedItemCount,
				OriginalResult: result,
			},
		},
	}
}
