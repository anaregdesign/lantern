package dataruntime

import (
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/tarp"
)

const tarpSearchBase byte = 26

type tarpSearchBaseRef struct {
	ID     uint64
	Source search.DiskSource
}

func readTarpSearchBaseRef(ctx context.Context, view *graphstore.ReadView) (tarpSearchBaseRef, error) {
	var ref tarpSearchBaseRef
	value, err := view.Snapshot.GetContext(ctx, tarpServiceKey(view.Generation, tarpSearchBase))
	if err != nil {
		return ref, err
	}
	if len(value) > 8<<10 || json.Unmarshal(value, &ref) != nil || ref.ID == 0 ||
		ref.Source.Watermark.IsZero() || ref.Source.Digest == ([32]byte{}) {
		return tarpSearchBaseRef{}, errTarpSearchCut
	}
	return ref, nil
}

// selectTarpSearchBase publishes an already durable derived base reference
// after verifying its identity and all intervening Graph changes. The graph
// head remains authoritative even if the detached index cannot reopen.
func selectTarpSearchBase(ctx context.Context, graph *graphstore.DB, derived *tarp.DB, ref tarpSearchBaseRef, analyzer search.Analyzer, scorer search.Scorer, maxBytes, maxKeys int, options ...search.IndexOption) error {
	if ref.ID == 0 || ref.Source.Watermark.IsZero() || ref.Source.Digest == ([32]byte{}) {
		return errTarpSearchCut
	}
	node := graph.NodeID()
	if ref.Source.Store != hex.EncodeToString(node[:]) {
		return errTarpSearchCut
	}
	if _, err := search.OpenDiskIndex(ctx, derived, ref.ID, ref.Source, analyzer, scorer, options...); err != nil {
		return err
	}
	encoded, err := json.Marshal(ref)
	if err != nil {
		return err
	}
	if len(encoded) > 8<<10 {
		return errTarpSearchCut
	}
	return graph.Update(func(gen uint64, snapshot *tarp.Snapshot, batch *tarp.WriteBatch) error {
		cut, err := readTarpSearchCut(ctx, snapshot, gen)
		if err != nil {
			return err
		}
		if cut.epoch != ref.Source.Epoch {
			return errTarpSearchCut
		}
		if err := visitTarpSearchChanges(ctx, snapshot, gen, cut, ref.Source.Sequence, ref.Source.Digest, maxBytes, maxKeys, func(uint64, string) error {
			return nil
		}); err != nil {
			return err
		}
		batch.Put(tarpServiceKey(gen, tarpSearchBase), encoded)
		return nil
	})
}

func openSelectedTarpSearchBase(ctx context.Context, graph *graphstore.ReadView, derived *tarp.DB, analyzer search.Analyzer, scorer search.Scorer, options ...search.IndexOption) (*search.DiskIndex, error) {
	ref, err := readTarpSearchBaseRef(ctx, graph)
	if err != nil {
		return nil, errors.Join(search.ErrIndexIncomplete, err)
	}
	node := graph.Database().NodeID()
	if ref.Source.Store != hex.EncodeToString(node[:]) {
		return nil, errors.Join(search.ErrIndexIncomplete, errTarpSearchCut)
	}
	index, err := search.OpenDiskIndex(ctx, derived, ref.ID, ref.Source, analyzer, scorer, options...)
	if err != nil {
		return nil, errors.Join(search.ErrIndexIncomplete, err)
	}
	return index, nil
}

// reclaimTarpSearchChanges removes coverable changes after Graph durably
// selects their replacement base. Older already-pinned Graph snapshots keep
// their keys through Tarp's normal isolation.
func reclaimTarpSearchChanges(ctx context.Context, graph *graphstore.DB, activated tarpSearchBaseRef) error {
	expected, err := json.Marshal(activated)
	if err != nil {
		return err
	}
	for {
		done := false
		err := graph.Update(func(gen uint64, snapshot *tarp.Snapshot, batch *tarp.WriteBatch) error {
			selected, err := snapshot.GetContext(ctx, tarpServiceKey(gen, tarpSearchBase))
			if err != nil {
				return err
			}
			if !bytes.Equal(selected, expected) {
				return errTarpSearchCut
			}
			cut, err := readTarpSearchCut(ctx, snapshot, gen)
			if err != nil {
				return err
			}
			if cut.epoch != activated.Source.Epoch || cut.last < activated.Source.Sequence {
				return errTarpSearchCut
			}
			if cut.first > activated.Source.Sequence {
				done = true
				return nil
			}
			// One original logical commit may contain many chunks. Its
			// first-retained sequence advances with their atomic deletion.
			prefix := tarpSearchSequencePrefix(gen, cut.first)
			lower, upper := tarp.PrefixRange(prefix)
			for key, err := range snapshot.KeysRange(ctx, lower, upper) {
				if err != nil {
					return err
				}
				if batch.Len() >= tarp.MaxWriteBatchEntries-2 {
					// This is the one-record capacity reserved for the
					// Search source row and the Graph manifest.
					return errTarpSearchCut
				}
				batch.Delete(key)
			}
			cut.first++
			batch.Put(tarpServiceKey(gen, tarpSearchSource), cut.encode())
			done = cut.first > activated.Source.Sequence
			return ctx.Err()
		})
		if err != nil || done {
			return err
		}
	}
}
