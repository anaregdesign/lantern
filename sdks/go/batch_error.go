package client

import "fmt"

// BatchError reports a partial-write failure from a chunked batch helper
// (PutVertices, AddEdges, PutEdges, DeleteVertices, DeleteEdges).
//
// Lantern's batch helpers split large inputs into chunks (see
// WithBatchChunkSize) and send them sequentially. If the Nth chunk fails,
// chunks 0..N-1 have already been committed server-side. BatchError records
// the number of fully-written input entries in Written so callers can:
//
//   - resume by re-sending input[Written:],
//   - surface the partial progress in logs/metrics,
//   - branch on errors.Is for the underlying gRPC sentinel
//     (ErrInvalidArgument / ErrResourceExhausted / ...).
//
// BatchError wraps the underlying error, so errors.Is and errors.Unwrap
// continue to work transparently.
type BatchError struct {
	// Written is the number of input entries successfully committed before
	// the failure. Always 0 <= Written < len(input).
	Written int
	// Err is the underlying error returned by the failing chunk RPC, already
	// passed through wrapStatus so SDK sentinels match via errors.Is.
	Err error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("lantern: batch partially written (%d entries committed): %v", e.Written, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
