package mcp

import (
	"errors"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// batchWritten interprets the error returned by a chunked SDK batch write
// (PutVertices / AddEdges) into the number of input entries that were fully
// committed and the underlying cause of any failure.
//
// The SDK's runBatchWrite sends the input in chunks and, on the first failing
// chunk, returns a *client.BatchError whose Written field counts the inputs
// committed before that chunk. So:
//   - err == nil               → every input committed (written == total).
//   - err is *client.BatchError → written = be.Written, cause = be.Err.
//   - any other error          → treated defensively as nothing committed
//     (written == 0); this covers pre-flight failures such as a value that
//     fails to encode before the first RPC is even attempted.
//
// Callers use written to partition their per-item result slice into
// stored (index < written) and not-stored (index >= written), which is what
// lets the batch tools report partial failures per item. For ADDITIVE writes
// (AddEdges) this is also the resume point: re-sending an already-committed
// entry double-counts, so a caller must resume from written, never from 0.
func batchWritten(total int, err error) (written int, cause error) {
	if err == nil {
		return total, nil
	}
	var be *client.BatchError
	if errors.As(err, &be) {
		return be.Written, be.Err
	}
	return 0, err
}
