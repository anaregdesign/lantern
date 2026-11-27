package graphcache

import (
	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

// A value loaded from the committed store has a known starting state. Track
// only changed sources while editing that bucket, including TTL collection.
// New or completely replaced buckets still use the full postimage path.
type sourceChanges map[graphstore.SourceID]sourceChange

type sourceChange struct {
	source  graphstore.Source
	deleted bool
}

func (v weightValue) storedSource() graphstore.Source {
	return graphstore.Source{Weight: v.value, Expiration: v.expiration, HLC: v.hlc, DerivedAggregate: v.derivedAggregate}
}

func (changes sourceChanges) note(v weightValue, deleted bool) {
	if changes == nil || v.contribID.IsZero() && v.ordinal == 0 {
		return
	}
	id := graphstore.SourceID{ContribID: v.contribID, Ordinal: v.ordinal}
	changes[id] = sourceChange{source: v.storedSource(), deleted: deleted}
}

func writeSourceChanges[S comparable, T any](w *weight, rows *graphstore.Rows[S, T], snap *tarp.Snapshot, b graphstore.Writer, gen uint64, key graphstore.EdgeKey[S]) error {
	edge := graphstore.Edge{HLC: w.lastHLC, LastOrdinal: w.lastOrdinal}
	for _, v := range w.values {
		if v.contribID.IsZero() && v.ordinal == 0 {
			base := v.storedSource()
			edge.Base = &base
			break
		}
	}
	for id, change := range w.changes {
		old, found, err := rows.GetSource(snap, gen, key, id)
		if err != nil {
			return err
		}
		var before, after *graphstore.Source
		if found {
			before = &old
		}
		if !change.deleted {
			after = &change.source
		}
		if err := rows.ReplaceSource(b, gen, key, id, before, after); err != nil {
			return err
		}
	}
	return rows.UpdateEdge(b, gen, key, edge)
}
