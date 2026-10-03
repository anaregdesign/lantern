package graphcache

import (
	"context"
	"errors"
	"sort"
)

// KeyRange is a half-open binary interval in the index's projected keyspace.
// Empty Upper means positive infinity. Lower/Upper may be binary seek bounds.
type KeyRange struct {
	Lower string
	Upper string
}

// QueryView restricts vertex candidates and both edge endpoints independently.
// Its private, detached ranges are immutable. It is a storage query primitive,
// not an authentication authority; the Server supplies an admitted view.
type QueryView struct {
	vertices []KeyRange
	edges    []KeyRange
}

func NewQueryView(vertices, edges []KeyRange) (*QueryView, error) {
	valid := func(ranges []KeyRange) bool {
		if len(ranges) > 16384 {
			return false
		}
		bytes := 0
		for i, r := range ranges {
			bytes += len(r.Lower) + len(r.Upper)
			if bytes > 8<<20 || r.Upper != "" && r.Lower >= r.Upper ||
				i > 0 && (ranges[i-1].Upper == "" || r.Lower < ranges[i-1].Upper) {
				return false
			}
		}
		return true
	}
	if !valid(vertices) || !valid(edges) {
		return nil, errors.New("invalid query ranges")
	}
	view := &QueryView{vertices: append([]KeyRange(nil), vertices...), edges: append([]KeyRange(nil), edges...)}
	return view, nil
}

func containsRange(ranges []KeyRange, key string) bool {
	i := sort.Search(len(ranges), func(i int) bool { return ranges[i].Lower > key }) - 1
	return i >= 0 && (ranges[i].Upper == "" || key < ranges[i].Upper)
}
func (v *QueryView) Vertex(projected string) bool {
	return v == nil || containsRange(v.vertices, projected)
}
func (v *QueryView) Edge(tail, head string) bool {
	return v == nil || v.Vertex(tail) && v.Vertex(head) && containsRange(v.edges, tail) && containsRange(v.edges, head)
}

type queryViewKey struct{}

func WithQueryView(ctx context.Context, view *QueryView) context.Context {
	if view == nil {
		panic("nil query view")
	}
	return context.WithValue(ctx, queryViewKey{}, view)
}
func queryViewFromContext(ctx context.Context) *QueryView {
	view, _ := ctx.Value(queryViewKey{}).(*QueryView)
	return view
}

func binaryPrefixEnd(prefix string) string {
	end := []byte(prefix)
	for i := len(end) - 1; i >= 0; i-- {
		if end[i] != 255 {
			end[i]++
			return string(end[:i+1])
		}
	}
	return ""
}
func earlierEnd(a, b string) string {
	if a == "" || b != "" && b < a {
		return b
	}
	return a
}

// walkVisiblePrefix seeks each disjoint visible interval. Denied subtrees are
// skipped before resolving values, collecting limits, or running callbacks.
// A nil view retains the existing single-prefix fast path and allocations.
func walkVisiblePrefix(ctx context.Context, index *radix, prefix, after string, inclusive, desc, edges bool, fn func(string) bool) {
	view := queryViewFromContext(ctx)
	if view == nil {
		if desc {
			if after == "" {
				index.walkPrefixDesc(prefix, fn)
			} else {
				index.walkPrefixBoundDesc(prefix, after, inclusive, fn)
			}
		} else if after == "" {
			index.walkPrefix(prefix, fn)
		} else {
			index.walkPrefixBound(prefix, after, inclusive, fn)
		}
		return
	}
	walkViewRanges(ctx, view, prefix, after, inclusive, desc, edges, index.walkRange, fn)
}

func walkViewRanges(ctx context.Context, view *QueryView, prefix, after string, inclusive, desc, edges bool, walkRange func(string, string, string, bool, bool, func(string) bool), fn func(string) bool) {
	ranges := view.vertices
	if edges {
		ranges = view.edges
	}
	end := binaryPrefixEnd(prefix)
	for step := range ranges {
		i := step
		if desc {
			i = len(ranges) - 1 - step
		}
		r := ranges[i]
		lower, upper := max(prefix, r.Lower), earlierEnd(end, r.Upper)
		if upper != "" && lower >= upper {
			continue
		}
		if !desc && after != "" && (upper != "" && after >= upper) {
			continue
		}
		if desc && after != "" && (after < lower || !inclusive && after == lower) {
			continue
		}
		keepGoing := true
		visit := func(key string) bool {
			if edges && !view.Vertex(key) {
				return true
			}
			keepGoing = fn(key)
			return keepGoing
		}
		walkRange(lower, upper, after, inclusive, desc, visit)
		if !keepGoing || ctx.Err() != nil {
			return
		}
	}
}

// queryProjection uses the same projection as range seeks and never interprets
// OIDC identity, Role membership, or authorization policy.
func (c *GraphCache[S, T]) queryProjection(key S) string {
	if c.prefixExtract != nil {
		return c.prefixExtract(key)
	}
	projected, _ := any(key).(string)
	return projected
}
func (c *GraphCache[S, T]) queryEdgeVisible(view *QueryView, tail, head S) bool {
	return view == nil || view.Edge(c.queryProjection(tail), c.queryProjection(head))
}
