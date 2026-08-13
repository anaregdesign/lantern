package client

import "fmt"

// BatchError reports a partial-write failure from a chunked batch helper
// (PutVertices, AddEdges, PutEdges, DeleteVertices, DeleteEdges).
//
// Lantern's batch helpers split large inputs into chunks (see
// WithBatchChunkSize) and send them sequentially. If the Nth chunk fails,
// chunks 0..N-1 have returned responses that the SDK fully validated.
// BatchError records that input-prefix length in Written so callers can:
//
//   - resume by re-sending input[Written:],
//   - surface the partial progress in logs/metrics,
//   - branch on errors.Is for the underlying SDK sentinel
//     (ErrInvalidArgument / ErrResourceExhausted / ...).
//
// Written is not a count of APPLIED_AND_LIVE outcomes: a completed Put chunk
// can contain EXPIRED, CONDITION_NOT_MET, or SUPERSEDED. For
// PutVerticesIfAbsent, the failed current chunk may have committed without a
// usable response, and replay performs a new condition evaluation rather than
// recovering its original outcomes. Reconcile current state before deciding
// to retry it.
//
// BatchError wraps the underlying error, so errors.Is and errors.Unwrap
// continue to work transparently.
type BatchError struct {
	// Written is the input-prefix length whose chunk responses were fully
	// observed and validated before the failure. Always
	// 0 <= Written < len(input).
	Written int
	// Err is the underlying error returned by the failing chunk RPC,
	// already passed through wrapConnectErr so SDK sentinels match via
	// errors.Is.
	Err error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("lantern: batch stopped after %d completed entries: %v", e.Written, e.Err)
}

func (e *BatchError) Unwrap() error { return e.Err }
