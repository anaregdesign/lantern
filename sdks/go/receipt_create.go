package client

// ReceiptCreateEdgeResult preserves the original disclosure-limited decision.
type ReceiptCreateEdgeResult struct{ Outcome CreateEdgeOutcome }

func (ReceiptCreateEdgeResult) MutationKind() ReceiptMutationKind { return ReceiptMutationCreateEdge }
func (ReceiptCreateEdgeResult) isReceiptOriginalResult()          {}
