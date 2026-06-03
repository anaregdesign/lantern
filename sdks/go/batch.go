package client

import (
	"context"
)

// runBatchWrite splits items into chunks of l.opts.batchChunkSize, invokes
// fn for each chunk with the per-call timeout applied, sums the returned
// per-chunk counts, and wraps any failure as a *BatchError whose Written
// field records the number of inputs that were fully committed before the
// failing chunk.
//
// Used by PutVertices / DeleteVertices / AddEdges / PutEdges / DeleteEdges:
// the four pure writes ignore the returned int (fn returns 0), and the two
// deletes use it to report the server-side "actually existed and removed"
// count.
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
			return total, &BatchError{Written: written, Err: wrapStatus(err)}
		}
		written += len(chunk)
		total += int(n)
	}
	return total, nil
}

// runBatchRead splits items into chunks and invokes fn for each chunk with
// the per-call timeout applied. Read paths abort on the first failure with a
// wrapped gRPC error (no *BatchError) — they have no partial-result contract
// to expose. Callers accumulate into their own variables via the closure.
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
			return wrapStatus(err)
		}
	}
	return nil
}
