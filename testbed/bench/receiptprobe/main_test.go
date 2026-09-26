package main

import (
	"bytes"
	"context"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/mutationreceipt"
	graphv1 "github.com/anaregdesign/lantern/pb/graph/v1"
)

type fakeReceiptClient struct {
	deleteRequest  *graphv1.DeleteEdgeRequest
	statusRequest  *graphv1.GetReceiptStatusRequest
	statusResponse *graphv1.GetReceiptStatusResponse
}

func (f *fakeReceiptClient) DeleteEdge(
	_ context.Context,
	request *connect.Request[graphv1.DeleteEdgeRequest],
) (*connect.Response[graphv1.DeleteEdgeResponse], error) {
	f.deleteRequest = request.Msg
	return connect.NewResponse(&graphv1.DeleteEdgeResponse{Existed: false}), nil
}

func (f *fakeReceiptClient) GetReceiptStatus(
	_ context.Context,
	request *connect.Request[graphv1.GetReceiptStatusRequest],
) (*connect.Response[graphv1.GetReceiptStatusResponse], error) {
	f.statusRequest = request.Msg
	return connect.NewResponse(f.statusResponse), nil
}

func TestExecuteOperationReusesIDAndValidatesConfirmedReceipt(t *testing.T) {
	t.Parallel()

	endpoint := testReceiptEndpoint()
	var nonce [16]byte
	nonce[0] = 1
	operation, err := newReceiptOperation(&endpoint, nonce, 7, "steady")
	if err != nil {
		t.Fatalf("newReceiptOperation() error = %v", err)
	}
	fake := &fakeReceiptClient{statusResponse: confirmedResponse(operation)}

	admission, lookup, lookedUp := executeOperation(
		context.Background(),
		"token",
		time.Second,
		fake,
		operation,
	)
	if admission.status != "OK" || lookup.status != "OK" || !lookedUp {
		t.Fatalf(
			"executeOperation() = admission %q, lookup %q, lookedUp %t",
			admission.status,
			lookup.status,
			lookedUp,
		)
	}
	if !bytes.Equal(fake.deleteRequest.GetReceiptContext().GetOperationIds()[0], operation.operationID) {
		t.Fatal("DeleteEdge operation ID does not match generated operation")
	}
	if !bytes.Equal(fake.statusRequest.GetOperationId(), operation.operationID) {
		t.Fatal("GetReceiptStatus did not reuse DeleteEdge operation ID")
	}
}

func TestExecuteOperationRejectsMismatchedConfirmedReceipt(t *testing.T) {
	t.Parallel()

	endpoint := testReceiptEndpoint()
	var nonce [16]byte
	nonce[0] = 1
	operation, err := newReceiptOperation(&endpoint, nonce, 9, "steady")
	if err != nil {
		t.Fatalf("newReceiptOperation() error = %v", err)
	}
	response := confirmedResponse(operation)
	response.Status.Receipt.IntentSha256[0] ^= 0xff
	fake := &fakeReceiptClient{statusResponse: response}

	admission, lookup, lookedUp := executeOperation(
		context.Background(),
		"token",
		time.Second,
		fake,
		operation,
	)
	if admission.status != "OK" || lookup.status != "DataLoss" || !lookedUp {
		t.Fatalf(
			"executeOperation() = admission %q, lookup %q, lookedUp %t",
			admission.status,
			lookup.status,
			lookedUp,
		)
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
	if err := validateEndpointSet([]receiptEndpoint{first, second}); err == nil {
		t.Fatal("validateEndpointSet() accepted duplicate node IDs")
	}

	second.capability.Endpoint.NodeId[0] ^= 0xff
	second.capability.Policy.Fingerprint[0] ^= 0xff
	if err := validateEndpointSet([]receiptEndpoint{first, second}); err == nil {
		t.Fatal("validateEndpointSet() accepted divergent receipt policy")
	}
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
		},
		issuedAt: time.Now().Add(-time.Second).Truncate(time.Millisecond),
	}
}

func confirmedResponse(operation receiptOperation) *graphv1.GetReceiptStatusResponse {
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
				OriginalResult: &graphv1.ReceiptResult{
					Result: &graphv1.ReceiptResult_DeleteEdgeExisted{
						DeleteEdgeExisted: false,
					},
				},
			},
		},
	}
}
