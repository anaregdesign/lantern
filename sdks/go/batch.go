package client

import (
	"context"
)

// runBatchWrite splits items into chunks of l.opts.batchChunkSize, invokes
// fn for each chunk with the per-call timeout applied, sums the returned
// per-chunk counts, and wraps any failure as a *BatchError whose Written
// field records the input-prefix length whose responses were fully observed
// and validated before the failing chunk.
//
// Used by PutVertices / DeleteVertices / AddEdges / PutEdges / DeleteEdges.
// Put callbacks return len(chunk), so a successfully validated outcome vector
// advances BatchError.Written by the exact observed prefix; Add callbacks
// return the server count/effective cardinality they expose, and deletes return
// the server-side "actually existed and removed" count. A failure while
// validating the current response leaves that entire chunk outside Written:
// its original per-item outcomes are ambiguous and conditional Put must not be
// blindly replayed to reconstruct them.
func runBatchWrite[T any](
	ctx context.Context,
	l *Lantern,
	items []T,
	fn func(ctx context.Context, chunk []T) (int32, error),
) (int, error) {
	if len(items) == 0 {
		return 0, nil
	}
	written, total := 0, 0
	for _, chunk := range chunkSlice(items, l.opts.batchChunkSize) {
		cctx, cancel := l.applyTimeout(ctx)
		n, err := fn(cctx, chunk)
		cancel()
		if err != nil {
			return total, &BatchError{Written: written, Err: err}
		}
		written += len(chunk)
		total += int(n)
	}
	return total, nil
}

// runBatchRead splits items into chunks and invokes fn for each chunk with
// the per-call timeout applied. Read paths abort on the first failure with
// the underlying (already-wrapped) error — they have no partial-result
// contract to expose. Callers accumulate into their own variables via the
// closure.
func runBatchRead[T any](
	ctx context.Context,
	l *Lantern,
	items []T,
	fn func(ctx context.Context, chunk []T) error,
) error {
	if len(items) == 0 {
		return nil
	}
	for _, chunk := range chunkSlice(items, l.opts.batchChunkSize) {
		cctx, cancel := l.applyTimeout(ctx)
		err := fn(cctx, chunk)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
