package graphcache

import (
	"time"

	"github.com/anaregdesign/lantern/core/search"
)

func (m *stagedGraphMutation[S, T]) reserveScratch(bytes uint64) error {
	if bytes > GraphMutationMaxScratchBytes-m.scratchBytes {
		return ErrGraphMutationBudget
	}
	m.scratchBytes += bytes
	return nil
}

// prepareSearch owns only the mixed accounting adapter. Domain analysis and
// prepared representation remain in search.Prepare. The ordinary Put route
// retains its preparation helpers, with no mixed ledger or timer allocation.
// Charges are retained across bounded repreparation attempts, conservatively
// covering old and replacement documents until the complete call finishes.
func (m *stagedGraphMutation[S, T]) prepareSearch(items []VertexItem[S, T], indexes []int, now time.Time, preparation *searchPreparation[S, T], missing []stagedVertexSearchCapture[S, T], before map[S]stagedVertexSearchBefore[T]) error {
	m.lockedAt = time.Time{}
	if preparation == nil {
		return m.checkBudget()
	}
	prepare := func(doc search.Document) (search.PreparedDocument, error) {
		if err := m.checkBudget(); err != nil {
			return search.PreparedDocument{}, err
		}
		prepared, charge, err := preparation.index.PrepareWithScratchLimit(doc, GraphMutationMaxScratchBytes-m.scratchBytes)
		if reserveErr := m.reserveScratch(charge); reserveErr != nil {
			return search.PreparedDocument{}, reserveErr
		}
		return prepared, err
	}
	for _, i := range indexes {
		if preparation.ready[i] || !vertexItemLiveAt(items[i], now) {
			continue
		}
		prepared, err := prepare(preparation.extract(items[i].Key, items[i].Value))
		if err != nil {
			return err
		}
		preparation.documents[i], preparation.ready[i] = prepared, true
	}
	for _, capture := range missing {
		old := stagedVertexSearchBefore[T]{slot: capture.slot}
		if capture.slot.present {
			var err error
			old.prepared, err = prepare(preparation.extract(capture.key, capture.slot.value))
			if err != nil {
				return err
			}
		}
		before[capture.key] = old
	}
	return m.checkBudget()
}
