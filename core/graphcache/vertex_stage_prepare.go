package graphcache

import (
	"errors"
	"reflect"
	"time"

	"github.com/anaregdesign/lantern/core/cache"
	"github.com/anaregdesign/lantern/core/search"
)

type stagedVertexSlot[T any] struct {
	value      T
	expiration time.Time
	present    bool
}

type stagedVertexSearchBefore[T any] struct {
	slot     stagedVertexSlot[T]
	prepared search.PreparedDocument
	err      error
}

type stagedVertexSearchCapture[S comparable, T any] struct {
	key  S
	slot stagedVertexSlot[T]
}

func (c *GraphCache[S, T]) prepareStagedVertexProjections(keys []S) (map[S]string, bool) {
	c.mu.RLock()
	enabled, extract := c.prefixIndex != nil, c.prefixExtract
	c.mu.RUnlock()
	if !enabled {
		return nil, false
	}
	projected := make(map[S]string, len(keys))
	for _, key := range keys {
		if _, ok := projected[key]; !ok {
			projected[key] = extract(key)
		}
	}
	return projected, true
}

func vertexItemKeys[S comparable, T any](items []VertexItem[S, T]) []S {
	keys := make([]S, len(items))
	for i := range items {
		keys[i] = items[i].Key
	}
	return keys
}

func vertexKeysAt[S comparable, T any](items []VertexItem[S, T], indexes []int) []S {
	keys := make([]S, len(indexes))
	for i, index := range indexes {
		keys[i] = items[index].Key
	}
	return keys
}

func vertexKeysAtIndexes[S comparable](keys []S, indexes []int) []S {
	selected := make([]S, len(indexes))
	for i, index := range indexes {
		selected[i] = keys[index]
	}
	return selected
}

func uniqueVertexKeys[S comparable](keys []S) []S {
	unique := make([]S, 0, len(keys))
	seen := make(map[S]struct{}, len(keys))
	for _, key := range keys {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, key)
	}
	return unique
}

func (c *GraphCache[S, T]) missingStagedVertexSearchBeforeLocked(
	keys []S, prepared map[S]stagedVertexSearchBefore[T],
) []stagedVertexSearchCapture[S, T] {
	if c.searchIndex == nil {
		return nil
	}
	var missing []stagedVertexSearchCapture[S, T]
	for _, key := range uniqueVertexKeys(keys) {
		value, expiration, present := c.vertices.PeekWithExpiration(key)
		slot := stagedVertexSlot[T]{value: value, expiration: expiration, present: present}
		old, ok := prepared[key]
		if ok && stagedVertexSlotsEqual(old.slot, slot) {
			continue
		}
		missing = append(missing, stagedVertexSearchCapture[S, T]{key: key, slot: slot})
	}
	return missing
}

func stagedVertexSlotsEqual[T any](left, right stagedVertexSlot[T]) bool {
	return left.present == right.present &&
		left.expiration.Equal(right.expiration) &&
		reflect.DeepEqual(left.value, right.value)
}

func prepareStagedVertexSearchBefore[S comparable, T any](
	preparation *searchPreparation[S, T],
	missing []stagedVertexSearchCapture[S, T],
	prepared map[S]stagedVertexSearchBefore[T],
) {
	if preparation == nil {
		return
	}
	for _, capture := range missing {
		before := stagedVertexSearchBefore[T]{slot: capture.slot}
		if capture.slot.present {
			before.prepared, _, before.err = preparation.index.Prepare(
				preparation.extract(capture.key, capture.slot.value),
			)
		}
		prepared[capture.key] = before
	}
}

func stagedVertexSearchBeforeItems[S comparable, T any](
	keys []S, prepared map[S]stagedVertexSearchBefore[T], now time.Time,
) ([]search.PreparedItem[S], error) {
	if prepared == nil {
		return nil, nil
	}
	unique := uniqueVertexKeys(keys)
	items := make([]search.PreparedItem[S], 0, len(unique))
	for _, key := range unique {
		before, ok := prepared[key]
		if !ok {
			return nil, errors.New("graphcache: staged Vertex search undo missing")
		}
		item := search.PreparedItem[S]{ID: key}
		if before.slot.present && cache.IsLiveAt(before.slot.expiration, now) {
			if before.err != nil {
				return nil, before.err
			}
			item.Prepared = before.prepared
			item.Expiration = before.slot.expiration
		}
		items = append(items, item)
	}
	return items, nil
}

func stagedVertexSearchDeletes[S comparable](
	keys []S, enabled bool,
) []search.PreparedItem[S] {
	if !enabled {
		return nil
	}
	unique := uniqueVertexKeys(keys)
	items := make([]search.PreparedItem[S], len(unique))
	for i, key := range unique {
		items[i] = search.PreparedItem[S]{ID: key}
	}
	return items
}

func (c *GraphCache[S, T]) prepareStagedVertexMutationLocked(
	keys []S,
	projections map[S]string,
	searchBefore, searchAfter []search.PreparedItem[S],
	applicationTime time.Time,
) (*stagedVertexMutation[S, T], error) {
	if c.dict == nil || c.edges == nil {
		return nil, errors.New("graphcache: staged vertex mutation requires graph indexes")
	}
	unique := uniqueVertexKeys(keys)
	stage := &stagedVertexMutation[S, T]{
		cache:           c,
		slots:           make(map[S]stagedVertexSlot[T], len(unique)),
		keys:            unique,
		projections:     projections,
		prefixBefore:    make(map[string]bool, len(unique)),
		searchIndex:     c.searchIndex,
		searchBefore:    searchBefore,
		searchAfter:     searchAfter,
		applicationTime: applicationTime,
	}
	for _, key := range unique {
		value, expiration, present := c.vertices.PeekWithExpiration(key)
		stage.slots[key] = stagedVertexSlot[T]{
			value: value, expiration: expiration, present: present,
		}
		if c.prefixIndex != nil {
			projected, ok := projections[key]
			if !ok {
				return nil, errors.New("graphcache: staged vertex projection missing")
			}
			if _, seen := stage.prefixBefore[projected]; !seen {
				stage.prefixBefore[projected] = c.prefixIndex.contains(projected)
			}
		}
	}
	stage.dict.capture(c.dict, unique, len(keys))
	captureStagedVertexCausalUndo(&stage.causal, c, unique)
	return stage, nil
}
