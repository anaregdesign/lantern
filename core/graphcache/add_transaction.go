package graphcache

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
	"github.com/anaregdesign/lantern/core/search"
)

// IndexedEdgeAdd identifies a receiver-local accepted contribution in request
// order. The original EdgeItem retains the contribution identity and intent.
type IndexedEdgeAdd[S comparable] struct {
	Index int
	Item  EdgeItem[S]
}

// EdgeAddStageResult is detached from the staged graph. Effective is aligned
// with every request item; Accepted contains only contributions applied by
// this receiver.
type EdgeAddStageResult[S comparable] struct {
	Effective []float32
	Accepted  []IndexedEdgeAdd[S]
}

// EdgeAddTransaction keeps Add effects hidden until the matching receipt/WAL
// publication commits. Abort restores the exact touched graph and indexes.
type EdgeAddTransaction[S comparable, T any] struct {
	stage  *stagedEdgeAdd[S, T]
	result EdgeAddStageResult[S]
	closed bool
}

// BeginEdgeAdd stages a locally originated Add batch.
func (c *GraphCache[S, T]) BeginEdgeAdd(
	items []EdgeItem[S],
	ts hlc.Timestamp,
) (*EdgeAddTransaction[S, T], error) {
	return c.beginEdgeAdd(items, ts)
}

// BeginReplicatedEdgeAdd stages an already committed Add batch on a receiver.
func (c *GraphCache[S, T]) BeginReplicatedEdgeAdd(
	items []EdgeItem[S],
	ts hlc.Timestamp,
) (*EdgeAddTransaction[S, T], error) {
	return c.beginEdgeAdd(items, ts)
}

func (c *GraphCache[S, T]) beginEdgeAdd(
	items []EdgeItem[S],
	ts hlc.Timestamp,
) (*EdgeAddTransaction[S, T], error) {
	if c.publicationGate == nil {
		return nil, errors.New("graphcache: staged Add requires a publication gate")
	}
	endpointKeys := make([]S, 0, 2*len(items))
	for _, item := range items {
		endpointKeys = append(endpointKeys, item.Tail, item.Head)
	}
	endpointKeys = uniqueVertexKeys(endpointKeys)
	endpoints := make([]VertexItem[S, T], len(endpointKeys))
	indexByKey := make(map[S]int, len(endpointKeys))
	for i, key := range endpointKeys {
		endpoints[i].Key = key
		indexByKey[key] = i
	}

	projections, prefixEnabled := c.prepareStagedVertexProjections(endpointKeys)
	preparation := c.newSearchPreparation(len(endpoints))
	if preparation != nil {
		c.prepareSearchDocsBounded(endpoints, allVertexIndexes(endpoints), time.Now(), preparation)
	}
	var oldSearch map[S]stagedVertexSearchBefore[T]
	if preparation != nil {
		oldSearch = make(map[S]stagedVertexSearchBefore[T])
	}

	lockState := 0
	var pending *stagedEdgeAdd[S, T]
	defer func() {
		if lockState == 0 {
			return
		}
		defer c.mu.Unlock()
		if lockState >= 2 {
			defer c.publicationGate.Unlock()
		}
		if lockState >= 3 {
			defer c.searchCommitMu.Unlock()
		}
		if pending != nil {
			pending.rollbackEdgesLocked()
			pending.vertex.rollbackLocked()
		}
	}()
	unlockMu := func() {
		lockState = 0
		c.mu.Unlock()
	}
	unlockAll := func() {
		lockState = 0
		c.unlockStagedVertexBegin()
	}

	for {
		c.mu.Lock()
		lockState = 1
		if (c.prefixIndex != nil) != prefixEnabled {
			unlockMu()
			return nil, errors.New("graphcache: vertex prefix policy changed during staged Add")
		}
		if !c.searchPreparationCurrentLocked(preparation) {
			unlockMu()
			preparation = c.newSearchPreparation(len(endpoints))
			if preparation == nil {
				oldSearch = nil
			} else {
				oldSearch = make(map[S]stagedVertexSearchBefore[T])
				c.prepareSearchDocsBounded(endpoints, allVertexIndexes(endpoints), time.Now(), preparation)
			}
			continue
		}
		if c.searchIndex != nil && c.searchIndex.Health() != search.IndexHealthy {
			unlockMu()
			return nil, search.ErrIndexIncomplete
		}

		_, _, provisionalAccepted, err := c.planStagedEdgeAddLocked(items, ts, time.Now())
		if err != nil {
			unlockMu()
			return nil, err
		}
		provisionalEndpoints := append([]VertexItem[S, T](nil), endpoints...)
		provisionalIndexes := c.stagedEdgeAddEndpointItemsLocked(
			items, provisionalAccepted, provisionalEndpoints, indexByKey, time.Now(),
		)
		oldMissing := c.missingStagedVertexSearchBeforeLocked(
			vertexKeysAt(provisionalEndpoints, provisionalIndexes),
			oldSearch,
		)
		if len(oldMissing) > 0 {
			unlockMu()
			prepareStagedVertexSearchBefore(preparation, oldMissing, oldSearch)
			continue
		}

		c.publicationGate.Lock()
		lockState = 2
		c.searchCommitMu.Lock()
		lockState = 3
		applicationTime := c.applicationTime()
		plans, effective, accepted, err := c.planStagedEdgeAddLocked(items, ts, applicationTime)
		if err != nil {
			unlockAll()
			return nil, err
		}
		finalEndpoints := append([]VertexItem[S, T](nil), endpoints...)
		endpointIndexes := c.stagedEdgeAddEndpointItemsLocked(
			items, accepted, finalEndpoints, indexByKey, applicationTime,
		)
		oldMissing = c.missingStagedVertexSearchBeforeLocked(
			vertexKeysAt(finalEndpoints, endpointIndexes),
			oldSearch,
		)
		if len(oldMissing) > 0 {
			unlockAll()
			prepareStagedVertexSearchBefore(preparation, oldMissing, oldSearch)
			continue
		}
		searchAfter, err := preparedItemsForIndexesAt(
			finalEndpoints, endpointIndexes, preparation, applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		searchBefore, err := stagedVertexSearchBeforeItems(
			vertexKeysAt(finalEndpoints, endpointIndexes),
			oldSearch,
			applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		if c.searchIndex != nil {
			if err := c.searchIndex.ValidateManyPreparedAt(searchAfter, applicationTime); err != nil {
				unlockAll()
				return nil, err
			}
		}

		vertexStage, err := c.prepareStagedVertexMutationLocked(
			vertexKeysAt(finalEndpoints, endpointIndexes),
			projections,
			searchBefore,
			searchAfter,
			applicationTime,
		)
		if err != nil {
			unlockAll()
			return nil, err
		}
		// New edge buckets own dictionary references even when both endpoint
		// vertices were already live, so the rollback journal covers every
		// endpoint rather than only vertices that need revival.
		vertexStage.dict.capture(c.dict, endpointKeys, len(endpointKeys))
		for _, plan := range plans {
			if c.headByTail != nil {
				projected, ok := projections[plan.key.Head]
				if !ok {
					unlockAll()
					return nil, errors.New("graphcache: staged Add head projection missing")
				}
				plan.headProjected = projected
			}
		}
		stage := &stagedEdgeAdd[S, T]{
			cache: c, vertex: vertexStage, plans: plans,
			effective: effective, accepted: accepted,
		}
		pending = stage
		stage.applyLocked(finalEndpoints, endpointIndexes)
		resultAccepted := make([]IndexedEdgeAdd[S], 0, len(items))
		for i, ok := range accepted {
			if ok {
				resultAccepted = append(resultAccepted, IndexedEdgeAdd[S]{Index: i, Item: items[i]})
			}
		}
		lockState = 0
		return &EdgeAddTransaction[S, T]{
			stage: stage,
			result: EdgeAddStageResult[S]{
				Effective: append([]float32(nil), effective...),
				Accepted:  resultAccepted,
			},
		}, nil
	}
}

// Result returns defensive copies of the authoritative effective weights and
// receiver-local accepted projection.
func (tx *EdgeAddTransaction[S, T]) Result() EdgeAddStageResult[S] {
	return EdgeAddStageResult[S]{
		Effective: append([]float32(nil), tx.result.Effective...),
		Accepted:  append([]IndexedEdgeAdd[S](nil), tx.result.Accepted...),
	}
}

// Commit publishes the staged state by releasing its visibility locks.
func (tx *EdgeAddTransaction[S, T]) Commit() {
	if tx == nil || tx.closed || !tx.stage.applied {
		panic("graphcache: staged Add committed in invalid state")
	}
	tx.closed = true
	tx.stage.vertex.release()
}

// Abort restores the exact pre-Add state and releases the visibility locks.
func (tx *EdgeAddTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.closed = true
	tx.stage.rollbackEdgesLocked()
	tx.stage.vertex.abort()
}
