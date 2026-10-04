package client

import (
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"testing"
)

func TestReceiptCreateOriginalOutcomeIsStrictAndDisclosureLimited(t *testing.T) {
	cap := testReceiptCapability(0x11)
	cap.SupportedMutations = append(cap.SupportedMutations, ReceiptMutationCreateEdge)
	context := testReceiptContextForMutation(t, cap, ReceiptMutationCreateEdge, 1, 0x31)
	status := testConfirmedEdgeDeleteStatus(context.OperationIDs[0], context.GroupID, 0, 1, false)
	for _, outcome := range []pb.CreateEdgeOutcome{1, 2, 3, 4, 0, 99} {
		status.Receipt.OriginalResult.Result = &pb.ReceiptResult_CreateEdgeOutcome{CreateEdgeOutcome: outcome}
		got, err := receiptStatusFromProto(context.OperationIDs[0], status)
		if outcome == 0 || outcome == 99 {
			if err == nil {
				t.Fatal("invalid original outcome accepted")
			}
			continue
		}
		if err != nil {
			t.Fatal(err)
		}
		original, ok := got.Receipt.OriginalResult.(ReceiptCreateEdgeResult)
		if !ok || original.Outcome != CreateEdgeOutcome(outcome) || original.MutationKind() != ReceiptMutationCreateEdge {
			t.Fatalf("original = %#v", original)
		}
	}
}
