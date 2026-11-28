package graphcache

import (
	"context"
	"time"

	"github.com/anaregdesign/lantern/core/graphstore"
	"github.com/anaregdesign/tarp"
)

// Each graph transaction owns the write lock until Commit or Abort. Capture
// from its applied state without relocking or making it visible prematurely.
func (tx *VertexPutTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed {
		return errStorageStageClosed
	}
	return tx.stage.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (tx *VertexDeleteTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed {
		return errStorageStageClosed
	}
	return tx.stage.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (tx *EdgeAddTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed {
		return errStorageStageClosed
	}
	return tx.stage.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (tx *EdgeCreateTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed {
		return errStorageStageClosed
	}
	return tx.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (tx *EdgeDeleteTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed || !tx.stage.applied {
		return errStorageStageClosed
	}
	return tx.stage.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (tx *EdgeContributionDeleteTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed || !tx.stage.base.applied {
		return errStorageStageClosed
	}
	return tx.stage.base.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

func (tx *GraphMutationTransaction[S, T]) StoreRows(ctx context.Context, rows *graphstore.Rows[S, T], snapshot *tarp.Snapshot, batch graphstore.Writer, generation uint64, keys StorageKeys[S]) error {
	if tx == nil || tx.closed {
		return errStorageStageClosed
	}
	return tx.vertex.stage.cache.storeRowsLocked(ctx, rows, snapshot, batch, generation, keys)
}

// StorageApplicationTime returns the sample used to materialize this staged
// publication's accepted Vertex effects.
func (tx *VertexPutTransaction[S, T]) StorageApplicationTime() time.Time {
	return tx.stage.applicationTime
}

func (tx *VertexDeleteTransaction[S, T]) StorageApplicationTime() time.Time {
	return tx.stage.applicationTime
}

func (tx *EdgeAddTransaction[S, T]) StorageApplicationTime() time.Time {
	return tx.stage.vertex.applicationTime
}

func (tx *GraphMutationTransaction[S, T]) StorageApplicationTime() time.Time {
	return tx.vertex.stage.applicationTime
}
