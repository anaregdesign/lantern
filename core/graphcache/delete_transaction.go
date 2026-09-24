package graphcache

import (
	"errors"
	"time"

	"github.com/anaregdesign/lantern/core/hlc"
)

// IndexedEdgeDelete identifies a causally admitted request item. An accepted
// absent edge still appears here with Existed=false in the aligned result, and
// duplicate keys retain their separate request indexes in original order.
type IndexedEdgeDelete[S comparable] struct {
	Index int
	Key   EdgeKey[S]
}

// EdgeDeleteStageResult is a detached result of a staged Delete. Existed has
// one entry per input key. Accepted contains every causally admitted input
// position, including no-op and duplicate positions; it is not deduplicated
// to the graph keys that actually need a state transition.
type EdgeDeleteStageResult[S comparable] struct {
	Existed  []bool
	Accepted []IndexedEdgeDelete[S]
}

// EdgeDeleteTransaction owns both GraphCache visibility locks until Commit or
// Abort. It is single-owner and must not be used concurrently. While open, the
// caller must not invoke another GraphCache method on the same cache. Callers
// should defer Abort immediately after Begin succeeds; Abort is safe after
// Commit.
//
// This is only an in-memory graph primitive. The enclosing server must hold
// its receipt/log/Snapshot publication cut across the WAL transaction, and
// must fail closed on an ambiguous WAL result rather than treating Abort as
// evidence that the durable envelope did not commit.
type EdgeDeleteTransaction[S comparable, T any] struct {
	stage  *stagedEdgeDelete[S, T]
	closed bool
}

// BeginEdgeDelete validates, stages, and hides an Edge Delete batch while the
// caller commits the matching envelope elsewhere. Every fallible graph/index
// change occurs before it returns. A definite external abort calls Abort; a
// successful external commit calls Commit. An ordinary NewGraphCache has no
// staging gate and returns an error here.
func (c *GraphCache[S, T]) BeginEdgeDelete(
	keys []EdgeKey[S], ts hlc.Timestamp, expiration time.Time,
) (*EdgeDeleteTransaction[S, T], error) {
	if c.publicationGate == nil {
		return nil, errors.New("graphcache: staged Delete requires a publication gate")
	}
	// A user-supplied projection may call a point reader. Capture the stable
	// init-time extractor, then execute it before taking either exclusive lock.
	c.mu.RLock()
	extract := c.prefixExtract
	c.mu.RUnlock()
	var projected map[EdgeKey[S]]string
	if extract != nil {
		projected = make(map[EdgeKey[S]]string, len(keys))
		for _, key := range keys {
			projected[key] = extract(key.Head)
		}
	}

	c.mu.Lock()
	c.publicationGate.Lock()
	owned := true
	var stage *stagedEdgeDelete[S, T]
	defer func() {
		if !owned {
			return
		}
		defer c.mu.Unlock()
		defer c.publicationGate.Unlock()
		if stage != nil {
			stage.rollbackLocked()
		}
	}()
	var err error
	stage, err = c.prepareStagedEdgeDeleteLocked(keys, ts, expiration, c.applicationTime(), projected)
	if err != nil {
		return nil, err
	}
	stage.applyLocked()
	tx := &EdgeDeleteTransaction[S, T]{stage: stage}
	owned = false
	return tx, nil
}

// Result returns defensive copies of the request-aligned observations and
// accepted indexed transitions. Call it before the external WAL commit when
// building the envelope; changing its slices cannot change the staged graph.
func (tx *EdgeDeleteTransaction[S, T]) Result() EdgeDeleteStageResult[S] {
	return EdgeDeleteStageResult[S]{
		Existed:  append([]bool{}, tx.stage.outcomes...),
		Accepted: append([]IndexedEdgeDelete[S]{}, tx.stage.accepted...),
	}
}

// Commit publishes an already staged Delete by releasing its visibility
// locks. It performs no graph mutation or allocation after the WAL commit.
func (tx *EdgeDeleteTransaction[S, T]) Commit() {
	if tx.closed {
		panic("graphcache: staged Delete committed after close")
	}
	tx.closed = true
	tx.stage.cache.publicationGate.Unlock()
	tx.stage.cache.mu.Unlock()
}

// Abort restores the exact pre-Begin graph/index/causal state, then releases
// both visibility locks. It is idempotent so it can be deferred by the caller.
func (tx *EdgeDeleteTransaction[S, T]) Abort() {
	if tx == nil || tx.closed {
		return
	}
	tx.closed = true
	c := tx.stage.cache
	defer c.mu.Unlock()
	defer c.publicationGate.Unlock()
	tx.stage.rollbackLocked()
}
