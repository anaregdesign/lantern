package client

import (
	"context"
)

// runBatchWrite splits items into chunks of l.opts.batchChunkSize, invokes
// fn for each chunk with the per-call timeout applied, sums the returned
// per-chunk counts, and wraps any failure as a *BatchError whose Written
// field records the input-prefix length whose responses were fully handled
// before the failing chunk. A blind acceptance does not confirm an effect.
//
// Used by PutVertices / DeleteVertices / AddEdges / PutEdges / DeleteEdges.
// Put callbacks return len(chunk), so a successfully validated outcome vector
// advances BatchError.Written by the exact observed prefix; Add callbacks
// expose effective weights separately, and Delete callbacks return the
// server-side "actually existed and removed" count. A failure while
// validating the current response leaves that entire chunk outside Written:
// its original outcomes are ambiguous, so conditional Put, plain Add, and
// exact Delete must not be blindly replayed to reconstruct them.
// A dedicated acceptance advances the input prefix and continues with each
// new chunk once; a later failure remains a BatchError, never full acceptance.
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
	undisclosed := false
	for _, chunk := range chunkSlice(items, l.opts.batchChunkSize) {
		cctx, cancel := l.applyTimeout(ctx)
		n, err := fn(cctx, chunk)
		cancel()
		if _, accepted := err.(*MutationAcceptance); accepted {
			// Advance only the public input prefix. No effect count was
			// observed, and this chunk must never be resent automatically.
			undisclosed = true
			err = nil
			n = 0
		}
		if err != nil {
			if undisclosed {
				total = 0
			}
			return total, &BatchError{Written: written, Err: err}
		}
		written += len(chunk)
		total += int(n)
	}
	if undisclosed {
		return 0, &MutationAcceptance{}
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
	return runBatchReadWithChunkSize(ctx, l, items, l.opts.batchChunkSize, fn)
}

func runBatchReadWithChunkSize[T any](
	ctx context.Context,
	l *Lantern,
	items []T,
	chunkSize int,
	fn func(ctx context.Context, chunk []T) error,
) error {
	if len(items) == 0 {
		return nil
	}
	for _, chunk := range chunkSlice(items, chunkSize) {
		cctx, cancel := l.applyTimeout(ctx)
		err := fn(cctx, chunk)
		cancel()
		if err != nil {
			return err
		}
	}
	return nil
}
