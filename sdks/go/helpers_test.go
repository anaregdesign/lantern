package client

import (
	"bytes"
	"testing"
	"time"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func testReceiptCapability(seed byte) ReceiptCapability {
	var (
		epoch       ReceiptEpoch
		nodeID      ReceiptNodeID
		generation  ReceiptGeneration
		fingerprint ReceiptPolicyFingerprint
	)
	epoch[0] = seed
	nodeID[0] = seed + 1
	generation[0] = seed + 2
	fingerprint[0] = seed + 3
	return ReceiptCapability{
		Enabled: true,
		Continuity: ReceiptContinuity{
			Epoch: epoch, NodeID: nodeID, Generation: generation,
		},
		PolicyFingerprint: fingerprint,
		Retention:         time.Hour,
		MaxEntries:        100,
		MaxBytes:          1 << 20,
		ServerTime:        time.UnixMilli(1_700_000_000_000).UTC(),
	}
}

func testReceiptCapabilityProto(capability ReceiptCapability) *pb.GetReceiptCapabilityResponse {
	if !capability.Enabled {
		return &pb.GetReceiptCapabilityResponse{}
	}
	return &pb.GetReceiptCapabilityResponse{
		Enabled: true,
		Policy: &pb.ReceiptPolicy{
			DeploymentEpoch: capability.Continuity.Epoch.Bytes(),
			Fingerprint:     capability.PolicyFingerprint.Bytes(),
			RetentionMs:     uint64(capability.Retention / time.Millisecond),
			MaxEntries:      capability.MaxEntries,
			MaxBytes:        capability.MaxBytes,
		},
		Endpoint: &pb.ReceiptEndpoint{
			NodeId:     capability.Continuity.NodeID.Bytes(),
			Generation: capability.Continuity.Generation.Bytes(),
		},
		ServerNowUnixMs: uint64(capability.ServerTime.UnixMilli()),
	}
}

func testReceiptContext(
	t *testing.T,
	capability ReceiptCapability,
	count int,
	seed byte,
) ReceiptContext {
	t.Helper()
	entropy := make([]byte, ReceiptGroupIDSize+count*ReceiptOperationRandomSize)
	for i := range entropy {
		entropy[i] = seed + byte(i)
		if entropy[i] == 0 {
			entropy[i] = 1
		}
	}
	context, err := mintReceiptContext(capability, count, receiptIdentitySource{
		random: bytes.NewReader(entropy),
		now:    func() time.Time { return capability.ServerTime.Add(time.Second) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return context
}

func testConfirmedEdgeDeleteStatus(
	id ReceiptOperationID,
	group ReceiptGroupID,
	index, count uint32,
	existed bool,
) *pb.ReceiptStatus {
	intent := make([]byte, ReceiptIntentSHA256Size)
	intent[0] = byte(index + 1)
	return &pb.ReceiptStatus{
		OperationId: id.Bytes(),
		State:       pb.MutationReceiptState_MUTATION_RECEIPT_STATE_CONFIRMED,
		Receipt: &pb.MutationReceipt{
			OperationId:    id.Bytes(),
			LogicalCallId:  group.Bytes(),
			ItemIndex:      index,
			ItemCount:      count,
			IntentSha256:   intent,
			DeadlineUnixMs: uint64(time.UnixMilli(1_700_003_600_000).UnixMilli()),
			OriginalResult: &pb.ReceiptResult{
				Result: &pb.ReceiptResult_DeleteEdgeExisted{DeleteEdgeExisted: existed},
			},
		},
	}
}
