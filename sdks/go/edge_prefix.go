package client

import (
	"context"
	"iter"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// EdgeScanOption configures Lantern.ScanEdges. Use the prefix options to
// narrow the result set on either endpoint dimension; either prefix may
// be omitted to leave it unconstrained.
type EdgeScanOption func(*edgeScanOptions)

type edgeScanOptions struct {
	tailPrefix string
	headPrefix string
	limit      uint32
	cursor     []byte
}

// WithEdgeScanTailPrefix restricts the scan to edges whose tail key
// starts with prefix. Empty (the default) imposes no tail constraint.
func WithEdgeScanTailPrefix(prefix string) EdgeScanOption {
	return func(o *edgeScanOptions) { o.tailPrefix = prefix }
}

// WithEdgeScanHeadPrefix restricts the scan to edges whose head key
// starts with prefix. v1 evaluates this as a post-filter on the tail
// walk, so a head-only scan is no cheaper than a full scan; pair with
// WithEdgeScanTailPrefix when possible.
func WithEdgeScanHeadPrefix(prefix string) EdgeScanOption {
	return func(o *edgeScanOptions) { o.headPrefix = prefix }
}

// WithEdgeScanLimit caps the number of edges the server returns in one
// ScanEdges response. 0 (the default) lets the server apply its
// configured ScanDefaultLimit. Values above ScanMaxLimit are clamped
// down server-side.
func WithEdgeScanLimit(n uint32) EdgeScanOption {
	return func(o *edgeScanOptions) { o.limit = n }
}

// WithEdgeScanCursor resumes a paginated edge scan from the cursor
// returned by a previous ScanEdges call. Treat the cursor as opaque
// bytes — do not inspect or modify it, and do not pass a vertex-scan
// cursor here (the two wire formats are intentionally incompatible).
func WithEdgeScanCursor(c []byte) EdgeScanOption {
	return func(o *edgeScanOptions) { o.cursor = c }
}

// DeleteEdgesByPrefixOption configures Lantern.DeleteEdgesByPrefix.
type DeleteEdgesByPrefixOption func(*deleteEdgesByPrefixOptions)

type deleteEdgesByPrefixOptions struct {
	tailPrefix string
	headPrefix string
	limit      uint32
	dryRun     bool
}

// WithEdgeDeleteTailPrefix restricts the delete to edges whose tail key
// starts with prefix. At least one of the tail / head prefixes MUST be
// non-empty or the server rejects the call with InvalidArgument — a bulk
// edge wipe is always explicitly scoped.
func WithEdgeDeleteTailPrefix(prefix string) DeleteEdgesByPrefixOption {
	return func(o *deleteEdgesByPrefixOptions) { o.tailPrefix = prefix }
}

// WithEdgeDeleteHeadPrefix restricts the delete to edges whose head key
// starts with prefix. Like ScanEdges, v1 evaluates this as a post-filter on
// the tail walk, so a head-only delete may visit many tails to reach the
// limit; pair with WithEdgeDeleteTailPrefix when possible.
func WithEdgeDeleteHeadPrefix(prefix string) DeleteEdgesByPrefixOption {
	return func(o *deleteEdgesByPrefixOptions) { o.headPrefix = prefix }
}

// WithEdgeDeleteLimit caps the number of edges a single DeleteEdgesByPrefix
// call may remove. 0 (the default) lets the server apply its configured
// default (see LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT). Values above the
// server's configured maximum are clamped down server-side.
func WithEdgeDeleteLimit(n uint32) DeleteEdgesByPrefixOption {
	return func(o *deleteEdgesByPrefixOptions) { o.limit = n }
}

// WithEdgeDeleteDryRun asks the server to report the number of edges that
// WOULD be deleted without actually deleting them. The returned count from
// DeleteEdgesByPrefix is the matched count; no mutation occurs.
func WithEdgeDeleteDryRun() DeleteEdgesByPrefixOption {
	return func(o *deleteEdgesByPrefixOptions) { o.dryRun = true }
}

// DeleteEdgesByPrefix removes up to the configured limit of live edges whose
// tail key starts with the tail prefix AND whose head key starts with the
// head prefix, returning the number actually removed (or, with
// WithEdgeDeleteDryRun, the number that WOULD have been removed).
//
// At least one of WithEdgeDeleteTailPrefix / WithEdgeDeleteHeadPrefix must be
// supplied with a non-empty value; an all-empty request is rejected with
// InvalidArgument so a bulk edge wipe is always explicitly scoped.
//
// Operational notes mirror DeleteVerticesByPrefix:
//   - This is a destructive bulk operation. Run with WithEdgeDeleteDryRun
//     first to confirm the matched count before issuing a real delete.
//   - To remove EVERY matching edge when the set exceeds the server's max
//     delete-by-prefix limit, call repeatedly until the returned count is
//     zero — the server applies the limit per call.
func (l *Lantern) DeleteEdgesByPrefix(ctx context.Context, opts ...DeleteEdgesByPrefixOption) (uint64, error) {
	o := deleteEdgesByPrefixOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.DeleteEdgesByPrefixRequest{
		TailPrefix: o.tailPrefix,
		HeadPrefix: o.headPrefix,
		Limit:      o.limit,
		DryRun:     o.dryRun,
	}, l.client.DeleteEdgesByPrefix)
	if err != nil {
		return 0, err
	}
	return resp.GetDeleted(), nil
}

// ScanEdges returns one page of edges in ascending (tail, head) order,
// filtered by the configured tail and head prefixes. nextCursor is
// non-empty when more pages are available; pass it via
// WithEdgeScanCursor on the next call to continue. An empty nextCursor
// signals end of stream.
//
// For most callers, ScanEdgesAll is the more ergonomic API — use this
// method only when you need explicit single-page control.
func (l *Lantern) ScanEdges(ctx context.Context, opts ...EdgeScanOption) (edges []*Edge, nextCursor []byte, err error) {
	o := edgeScanOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, l, &pb.ScanEdgesRequest{
		TailPrefix: o.tailPrefix,
		HeadPrefix: o.headPrefix,
		Limit:      o.limit,
		Cursor:     o.cursor,
	}, l.client.ScanEdges)
	if err != nil {
		return nil, nil, err
	}
	return resp.GetEdges(), resp.GetNextCursor(), nil
}

// ScanEdgesAll returns a Go 1.23+ iter.Seq2 that yields successive pages
// of ScanEdges results until the matching range is exhausted or ctx is
// cancelled. batchSize is forwarded to the server as the per-call limit
// (0 = server default). Stop conditions mirror ScanVerticesAll.
//
// Typical use:
//
//	for batch, err := range cli.ScanEdgesAll(ctx, 500,
//	    client.WithEdgeScanTailPrefix("user:"),
//	    client.WithEdgeScanHeadPrefix("post:"),
//	) {
//	    if err != nil { return err }
//	    for _, e := range batch { ... }
//	}
func (l *Lantern) ScanEdgesAll(ctx context.Context, batchSize uint32, opts ...EdgeScanOption) iter.Seq2[[]*Edge, error] {
	return func(yield func([]*Edge, error) bool) {
		var cursor []byte
		for {
			if ctx.Err() != nil {
				yield(nil, ctx.Err())
				return
			}
			pageOpts := append([]EdgeScanOption{}, opts...)
			pageOpts = append(pageOpts, WithEdgeScanLimit(batchSize), WithEdgeScanCursor(cursor))
			es, next, err := l.ScanEdges(ctx, pageOpts...)
			if err != nil {
				yield(nil, err)
				return
			}
			if !yield(es, nil) {
				return
			}
			if len(next) == 0 {
				return
			}
			cursor = next
		}
	}
}
