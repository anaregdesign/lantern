package client

import (
	"context"
	"iter"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// ScanOption configures Lantern.ScanVertices. Use WithScanLimit /
// WithScanCursor to page through a large prefix range.
type ScanOption func(*scanOptions)

type scanOptions struct {
	limit  uint32
	cursor []byte
}

// WithScanLimit caps the number of vertices the server returns in one
// ScanVertices response. 0 (the default) lets the server apply its
// configured default (see LANTERN_SCAN_DEFAULT_LIMIT). Values above the
// server's configured maximum are clamped down server-side.
func WithScanLimit(n uint32) ScanOption {
	return func(o *scanOptions) { o.limit = n }
}

// WithScanCursor resumes a paginated scan from the cursor returned by a
// previous ScanVertices call. Treat the cursor as opaque bytes — do not
// inspect or modify it. An empty cursor (the default) starts from the
// beginning of the prefix range.
func WithScanCursor(c []byte) ScanOption {
	return func(o *scanOptions) { o.cursor = c }
}

// DeleteByPrefixOption configures Lantern.DeleteVerticesByPrefix.
type DeleteByPrefixOption func(*deleteByPrefixOptions)

type deleteByPrefixOptions struct {
	limit  uint32
	dryRun bool
}

// WithDeleteByPrefixLimit caps the number of vertices a single
// DeleteVerticesByPrefix call may remove. 0 (the default) lets the server
// apply its configured default (see LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT).
// Values above the server's configured maximum are clamped down server-side.
func WithDeleteByPrefixLimit(n uint32) DeleteByPrefixOption {
	return func(o *deleteByPrefixOptions) { o.limit = n }
}

// WithDryRun asks the server to report the number of vertices that WOULD be
// deleted without actually deleting them. The returned count from
// DeleteVerticesByPrefix is the matched count; no mutation occurs.
func WithDryRun() DeleteByPrefixOption {
	return func(o *deleteByPrefixOptions) { o.dryRun = true }
}

// ScanVertices returns one page of vertices whose key starts with prefix, in
// ascending key order. nextCursor is non-empty when more pages are available;
// pass it via WithScanCursor on the next call to continue. An empty
// nextCursor signals end of stream.
//
// For most callers, ScanVerticesAll is the more ergonomic API — use this
// method only when you need explicit control over a single page (e.g. for
// implementing a UI "next page" button).
func (l *Lantern) ScanVertices(ctx context.Context, prefix string, opts ...ScanOption) (vertices []*Vertex, nextCursor []byte, err error) {
	o := scanOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := l.client.ScanVertices(ctx, &pb.ScanVerticesRequest{
		Prefix: prefix,
		Limit:  o.limit,
		Cursor: o.cursor,
	})
	if err != nil {
		return nil, nil, wrapStatus(err)
	}
	return resp.GetVertices(), resp.GetNextCursor(), nil
}

// CountVerticesByPrefix returns the number of live vertices whose key starts
// with prefix. The implementation is radix-only and does NOT cross-check
// vertex liveness per entry, so under heavy expiration churn the count may
// transiently overshoot the number ScanVertices would actually return. Treat
// the value as a fast estimate, not as a hard invariant for ScanVertices.
func (l *Lantern) CountVerticesByPrefix(ctx context.Context, prefix string) (uint64, error) {
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := l.client.CountVerticesByPrefix(ctx, &pb.CountVerticesByPrefixRequest{Prefix: prefix})
	if err != nil {
		return 0, wrapStatus(err)
	}
	return resp.GetCount(), nil
}

// DeleteVerticesByPrefix removes up to the configured limit of vertices
// whose key starts with prefix, returning the number actually removed (or,
// with WithDryRun, the number that WOULD have been removed).
//
// Operational notes:
//   - This is a destructive bulk operation. Always run with WithDryRun first
//     to confirm the matched count before issuing a real delete.
//   - The call is excluded from the SDK's default retry policy because a
//     partial-success retry could over-delete (server already removed N when
//     UNAVAILABLE was returned; the retry would remove the next N).
//   - To remove EVERY matching vertex when the prefix exceeds the server's
//     max delete-by-prefix limit, call repeatedly until the returned count is
//     zero — the server applies the limit per call.
func (l *Lantern) DeleteVerticesByPrefix(ctx context.Context, prefix string, opts ...DeleteByPrefixOption) (uint64, error) {
	o := deleteByPrefixOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := l.client.DeleteVerticesByPrefix(ctx, &pb.DeleteVerticesByPrefixRequest{
		Prefix: prefix,
		Limit:  o.limit,
		DryRun: o.dryRun,
	})
	if err != nil {
		return 0, wrapStatus(err)
	}
	return resp.GetDeleted(), nil
}

// ScanVerticesAll returns a Go 1.23+ iter.Seq2 that yields successive pages
// of ScanVertices results until the prefix range is exhausted or ctx is
// cancelled. Each yielded value is one server response's vertex slice (never
// nil, but possibly empty on the final page); errors short-circuit
// iteration. batchSize is forwarded to the server as the per-call limit
// (0 = server default).
//
// Typical use:
//
//	for batch, err := range cli.ScanVerticesAll(ctx, "users/", 500) {
//	    if err != nil { return err }
//	    for _, v := range batch { ... }
//	}
//
// Stop conditions: empty next_cursor from the server (clean end of stream),
// any error from the server, or the consumer returning false from yield
// (i.e. `break` out of the for-range).
func (l *Lantern) ScanVerticesAll(ctx context.Context, prefix string, batchSize uint32) iter.Seq2[[]*Vertex, error] {
	return func(yield func([]*Vertex, error) bool) {
		var cursor []byte
		for {
			if ctx.Err() != nil {
				yield(nil, ctx.Err())
				return
			}
			vs, next, err := l.ScanVertices(ctx, prefix, WithScanLimit(batchSize), WithScanCursor(cursor))
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(vs, nil) {
				return
			}
			if len(next) == 0 {
				return
			}
			cursor = next
		}
	}
}
