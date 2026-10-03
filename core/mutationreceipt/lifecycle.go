package mutationreceipt

func validLifecycleReduction(receipt Receipt) bool {
	return !receipt.LifecycleReduction || receipt.Kind == PutVertex || receipt.Kind == PutEdge
}

// SetReservedLifecycleReductions attaches the origin's actual storage effect
// to reserved receipt rows before Stage/WAL. Duplicate replay never recomputes
// it from today's graph. Core knows no authorization meaning for this bit.
func (tx *Tx) SetReservedLifecycleReductions(reduced []bool) error {
	if tx == nil || tx.closed || tx.mode != txReserved || len(reduced) != len(tx.staged) {
		return ErrTransactionState
	}
	for i, value := range reduced {
		candidate := tx.staged[i]
		candidate.LifecycleReduction = value
		if !validLifecycleReduction(candidate) {
			return ErrInvalidBatch
		}
	}
	for i, value := range reduced {
		tx.staged[i].LifecycleReduction = value
	}
	return nil
}
