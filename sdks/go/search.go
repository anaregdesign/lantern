package client

import (
	"context"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// SearchHit is one ranked result from Lantern.SearchVertices: the key of a
// matching vertex paired with its full-text relevance Score (higher is more
// relevant). Like EdgeRef it is a flat SDK-native value, not a proto alias —
// it carries only the key and score, so call GetVertex / GetVertices on the
// keys to hydrate the stored value and TTL.
type SearchHit struct {
	Key   string
	Score float64
}

// SearchOption configures Lantern.SearchVertices. Use WithSearchLimit /
// WithSearchPrefix to cap the result count or scope hits to a key prefix.
type SearchOption func(*searchOptions)

type searchOptions struct {
	limit  uint32
	prefix string
}

// WithSearchLimit caps the number of hits the server returns from one
// SearchVertices call. 0 (the default) lets the server apply its configured
// default (see LANTERN_SEARCH_DEFAULT_LIMIT). Values above the server's
// configured maximum (LANTERN_SEARCH_MAX_LIMIT) are clamped down
// server-side, so callers never need to pre-clamp.
func WithSearchLimit(n uint32) SearchOption {
	return func(o *searchOptions) { o.limit = n }
}

// WithSearchPrefix scopes hits to vertices whose key carries the given
// prefix — the content-search analogue of restricting ScanVertices to a
// namespace. An empty prefix (the default) searches every live vertex.
func WithSearchPrefix(p string) SearchOption {
	return func(o *searchOptions) { o.prefix = p }
}

// SearchVertices returns vertices ranked by full-text relevance over their
// indexed content (key + value), most relevant first. It is the content
// counterpart to ScanVertices' lexicographic key walk: search by a
// remembered topic word instead of an exact key prefix.
//
// Empty result vs. error: an empty, unanalysable, or simply non-matching
// query is a zero-hit success — the returned slice is empty (never nil) and
// err is nil, not a sentinel. The server clamps the limit to its configured
// (0, MaxLimit] range (0 = server default) AFTER applying liveness and
// prefix filters, so the returned slice never exceeds the effective limit
// and the SDK does not pre-clamp.
//
// Gating: when the server was started without the search index
// (LANTERN_SEARCH_ENABLED=false) the call returns an error matching
// ErrFailedPrecondition — a configuration state, not a transient failure.
// Detect it with errors.Is(err, ErrFailedPrecondition) and present a calm
// "search is turned off" state rather than retrying.
func (l *Lantern) SearchVertices(ctx context.Context, query string, opts ...SearchOption) ([]SearchHit, error) {
	o := searchOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	resp, err := unary(ctx, &pb.SearchVerticesRequest{
		Query:  query,
		Limit:  o.limit,
		Prefix: o.prefix,
	}, l.client.SearchVertices)
	if err != nil {
		return nil, err
	}
	pbHits := resp.GetHits()
	hits := make([]SearchHit, 0, len(pbHits))
	for _, h := range pbHits {
		hits = append(hits, SearchHit{Key: h.GetKey(), Score: h.GetScore()})
	}
	return hits, nil
}
