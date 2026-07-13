package client

import (
	"context"
	"errors"
	"fmt"
	"iter"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

// SearchErrorReason is the bounded reason carried by a SearchVertices error.
type SearchErrorReason = pb.SearchErrorReason

const (
	SearchErrorReasonUnspecified       = pb.SearchErrorReason_SEARCH_ERROR_REASON_UNSPECIFIED
	SearchErrorReasonDisabled          = pb.SearchErrorReason_SEARCH_DISABLED
	SearchErrorReasonPositionsDisabled = pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED
	SearchErrorReasonWorkBudget        = pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED
	SearchErrorReasonAdmission         = pb.SearchErrorReason_SEARCH_ADMISSION_SATURATED
	SearchErrorReasonIndexIncomplete   = pb.SearchErrorReason_SEARCH_INDEX_INCOMPLETE
	SearchErrorReasonIndexBudget       = pb.SearchErrorReason_SEARCH_INDEX_BUDGET_EXHAUSTED
	SearchErrorReasonCursorStale       = pb.SearchErrorReason_SEARCH_CURSOR_STALE
	SearchErrorReasonCursorInvalid     = pb.SearchErrorReason_SEARCH_CURSOR_INVALID
	SearchErrorReasonContinuation      = pb.SearchErrorReason_SEARCH_CONTINUATION_LIMITED
)

var (
	// ErrSearchDisabled means the server has no content-search index.
	ErrSearchDisabled = errors.New("search disabled")
	// ErrSearchPositionsDisabled means phrase adjacency cannot be verified.
	ErrSearchPositionsDisabled = errors.New("search positions disabled")
	// ErrSearchWorkBudget means one deterministic per-query work cap was exceeded.
	ErrSearchWorkBudget = errors.New("search work budget exhausted")
	// ErrSearchAdmission means every configured in-flight search slot was occupied.
	ErrSearchAdmission = errors.New("search admission saturated")
	// ErrSearchIndexIncomplete means the graph converged but the local derived
	// index requires a successful bounded rebuild.
	ErrSearchIndexIncomplete = errors.New("search index incomplete")
	// ErrSearchIndexBudget means a local write would exceed an index memory cap.
	ErrSearchIndexBudget = errors.New("search index budget exhausted")
	// ErrSearchCursorStale means the endpoint-sticky session expired or was
	// evicted. Restart explicitly from page one; never reuse the cursor.
	ErrSearchCursorStale = errors.New("search cursor stale")
	// ErrSearchCursorInvalid means the opaque cursor was tampered with or used
	// with a different query, option set, projection, config, or endpoint.
	ErrSearchCursorInvalid = errors.New("search cursor invalid")
	// ErrSearchContinuationLimited means a bounded session exposed more matches
	// than its configured hit/byte cap could retain.
	ErrSearchContinuationLimited = errors.New("search continuation limited")
)

// SearchFailureReason extracts the machine-readable reason from a search
// error detail, including errors wrapped by the SDK.
func SearchFailureReason(err error) SearchErrorReason {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return SearchErrorReasonUnspecified
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
			return searchDetail.GetReason()
		}
	}
	return SearchErrorReasonUnspecified
}

// SearchFailureWorkKind extracts the stable exhausted counter name
// (query_bytes, query_terms, dictionary_visits, posting_visits, or
// position_visits). It is empty for non-budget errors.
func SearchFailureWorkKind(err error) string {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return ""
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			continue
		}
		if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
			return searchDetail.GetWorkKind()
		}
	}
	return ""
}

// SearchHit is one ranked result from Lantern.SearchVertices: the key of a
// matching vertex paired with its full-text relevance Score (higher is more
// relevant). Equal scores are ordered by raw Key ascending. Like EdgeRef it is
// a flat SDK-native value, not a proto alias. Vertex is populated only when
// SearchProjectionFullVertex was requested and ProjectionStatus proves an
// exact selection snapshot.
type SearchHit struct {
	Key              string
	Score            float64
	Vertex           *Vertex
	ProjectionStatus SearchHitProjectionStatus
}

// SearchProjection selects lightweight key+score hits or exact vertex
// snapshots. The unspecified wire value is normalized to KeyScore.
type SearchProjection = pb.SearchProjection

const (
	SearchProjectionKeyScore   = pb.SearchProjection_SEARCH_PROJECTION_KEY_SCORE
	SearchProjectionFullVertex = pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX
)

// SearchHitProjectionStatus states whether a hit is lightweight, an exact
// snapshot, or a fail-closed missing/replaced selection.
type SearchHitProjectionStatus = pb.SearchHitProjectionStatus

const (
	SearchHitProjectionKeyScore = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_KEY_SCORE
	SearchHitProjectionSnapshot = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT
	SearchHitProjectionMissing  = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_MISSING
	SearchHitProjectionReplaced = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_REPLACED
)

// SearchPage is one bounded page plus its explicit continuation contract.
type SearchPage struct {
	Hits                []SearchHit
	NextCursor          []byte
	EffectiveLimit      uint32
	Truncated           bool
	ContinuationLimited bool
}

// MatchMode selects how SearchVertices combines a multi-word query's terms:
// MatchServerDefault, MatchAny (OR), MatchAll (AND), or MatchMinShould (at
// least WithMinShouldMatch terms). It is a thin alias of the wire enum.
type MatchMode = pb.MatchMode

const (
	// MatchServerDefault defers membership semantics to the server.
	MatchServerDefault = pb.MatchMode_MATCH_MODE_UNSPECIFIED
	// MatchAny keeps vertices sharing at least one query term (OR-union).
	MatchAny = pb.MatchMode_MATCH_MODE_ANY
	// MatchAll keeps vertices carrying every query word term (AND).
	MatchAll = pb.MatchMode_MATCH_MODE_ALL
	// MatchMinShould keeps vertices carrying at least WithMinShouldMatch terms.
	MatchMinShould = pb.MatchMode_MATCH_MODE_MIN_SHOULD
)

// SearchOption configures Lantern.SearchVertices: WithSearchLimit /
// WithSearchPrefix cap the result count or scope hits to a key prefix, and
// WithMatchMode / WithMinShouldMatch / WithPhrase / WithFuzziness /
// WithPrefixTerms tune relevance (#892). Leaving all of the relevance options
// unset sends no SearchOptions, so the server applies its configured defaults.
type SearchOption func(*searchOptions)

type searchOptions struct {
	limit          uint32
	prefix         string
	matchMode      pb.MatchMode
	minShouldMatch uint32
	fuzziness      uint32
	prefixTerms    bool
	phrase         bool
	cursor         []byte
	projection     pb.SearchProjection
}

const maxSearchFuzziness = uint32(2)

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

// WithSearchCursor resumes the endpoint-sticky snapshot returned by the prior
// SearchVerticesPage call. Treat cursor as opaque and repeat every other
// option exactly.
func WithSearchCursor(cursor []byte) SearchOption {
	return func(o *searchOptions) { o.cursor = append([]byte(nil), cursor...) }
}

// WithSearchProjection selects KEY_SCORE or FULL_VERTEX result projection.
func WithSearchProjection(projection SearchProjection) SearchOption {
	return func(o *searchOptions) { o.projection = projection }
}

// WithFullVertex requests exact value/TTL snapshots in each ranked hit.
func WithFullVertex() SearchOption {
	return WithSearchProjection(SearchProjectionFullVertex)
}

// WithMatchMode selects how a multi-word query's terms combine: MatchAny (OR),
// MatchAll (AND), or MatchMinShould (see WithMinShouldMatch). The default lets
// the server decide (LANTERN_SEARCH_DEFAULT_MODE, itself MatchAny) (#890).
func WithMatchMode(m MatchMode) SearchOption {
	return func(o *searchOptions) { o.matchMode = m }
}

// WithMinShouldMatch requires a hit to carry at least n distinct query word
// terms; it takes effect with WithMatchMode(MatchMinShould). 0 leaves the
// server default (#890).
func WithMinShouldMatch(n uint32) SearchOption {
	return func(o *searchOptions) { o.minShouldMatch = n }
}

// WithPhrase requires the query's word terms to occur adjacently, in order.
// Phrase currently cannot compose with an explicit match mode, fuzziness, or
// prefix terms; ValidateSearchOptions rejects those combinations (#889).
func WithPhrase() SearchOption {
	return func(o *searchOptions) { o.phrase = true }
}

// WithFuzziness also matches dictionary terms within edits edit distance (0, 1,
// or 2) of a query word, so a typo still finds the term. 0 (the default)
// disables fuzzy matching (#891).
func WithFuzziness(edits uint32) SearchOption {
	return func(o *searchOptions) { o.fuzziness = edits }
}

// WithPrefixTerms also matches dictionary terms that extend a query word, so
// "lan" finds "lantern" (#891).
func WithPrefixTerms() SearchOption {
	return func(o *searchOptions) { o.prefixTerms = true }
}

// ValidateSearchOptions checks SearchOption values without dialing a server.
// Invalid combinations wrap ErrInvalidArgument, matching an INVALID_ARGUMENT
// response from the server. SearchVertices calls it automatically; command
// surfaces may call it before opening a transport.
func ValidateSearchOptions(opts ...SearchOption) error {
	o := searchOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	return validateSearchOptions(o)
}

func validateSearchOptions(o searchOptions) error {
	switch o.projection {
	case pb.SearchProjection_SEARCH_PROJECTION_UNSPECIFIED,
		pb.SearchProjection_SEARCH_PROJECTION_KEY_SCORE,
		pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX:
	default:
		return fmt.Errorf("%w: search projection %d is not recognized", ErrInvalidArgument, o.projection)
	}
	switch o.matchMode {
	case pb.MatchMode_MATCH_MODE_UNSPECIFIED,
		pb.MatchMode_MATCH_MODE_ANY,
		pb.MatchMode_MATCH_MODE_ALL,
		pb.MatchMode_MATCH_MODE_MIN_SHOULD:
	default:
		return fmt.Errorf("%w: search match mode %d is not recognized", ErrInvalidArgument, o.matchMode)
	}
	if o.minShouldMatch != 0 && o.matchMode != pb.MatchMode_MATCH_MODE_MIN_SHOULD {
		return fmt.Errorf("%w: WithMinShouldMatch requires WithMatchMode(MatchMinShould)", ErrInvalidArgument)
	}
	if o.fuzziness > maxSearchFuzziness {
		return fmt.Errorf("%w: WithFuzziness must be 0, 1, or 2", ErrInvalidArgument)
	}
	if !o.phrase {
		return nil
	}
	if o.matchMode != pb.MatchMode_MATCH_MODE_UNSPECIFIED {
		return fmt.Errorf("%w: WithPhrase cannot be combined with an explicit match mode", ErrInvalidArgument)
	}
	if o.minShouldMatch != 0 {
		return fmt.Errorf("%w: WithPhrase cannot be combined with WithMinShouldMatch", ErrInvalidArgument)
	}
	if o.fuzziness != 0 {
		return fmt.Errorf("%w: WithPhrase cannot be combined with WithFuzziness", ErrInvalidArgument)
	}
	if o.prefixTerms {
		return fmt.Errorf("%w: WithPhrase cannot be combined with WithPrefixTerms", ErrInvalidArgument)
	}
	return nil
}

// SearchVertices returns vertices ranked by full-text relevance over their
// indexed key and value fields in stable (score DESC, raw key ASC) order. Field
// boundaries are preserved for phrase/proximity and key evidence is weighted
// independently by the server. It
// is the content counterpart to ScanVertices' lexicographic key walk: search
// by a remembered topic word instead of an exact key prefix.
//
// Empty result vs. error: an empty, unanalysable, or simply non-matching
// query is a zero-hit success — the returned slice is empty (never nil) and
// err is nil, not a sentinel. The server clamps the limit to its configured
// (0, MaxLimit] range (0 = server default) AFTER applying liveness and
// prefix filters, so the returned slice never exceeds the effective limit
// and the SDK does not pre-clamp.
//
// Gating: when the server was started without the search index
// (LANTERN_SEARCH_ENABLED=false) the call returns an error matching both
// ErrFailedPrecondition and ErrSearchDisabled. A phrase request against a
// server without positional postings matches ErrSearchPositionsDisabled.
func (l *Lantern) SearchVertices(ctx context.Context, query string, opts ...SearchOption) ([]SearchHit, error) {
	page, err := l.SearchVerticesPage(ctx, query, opts...)
	return page.Hits, err
}

// SearchVerticesPage returns one bounded page. Truncated is truthful even when
// the caller stops; NextCursor continues the same endpoint-sticky immutable
// snapshot. A zero-hit success returns a non-nil empty Hits slice.
func (l *Lantern) SearchVerticesPage(ctx context.Context, query string, opts ...SearchOption) (SearchPage, error) {
	o := searchOptions{}
	for _, apply := range opts {
		apply(&o)
	}
	if err := validateSearchOptions(o); err != nil {
		return SearchPage{}, err
	}
	ctx, cancel := l.applyTimeout(ctx)
	defer cancel()
	req := &pb.SearchVerticesRequest{
		Query:      query,
		Limit:      o.limit,
		Prefix:     o.prefix,
		Cursor:     o.cursor,
		Projection: o.projection,
	}
	if o.matchMode != pb.MatchMode_MATCH_MODE_UNSPECIFIED || o.minShouldMatch != 0 || o.fuzziness != 0 || o.prefixTerms || o.phrase {
		req.Options = &pb.SearchOptions{
			MatchMode:      o.matchMode,
			MinShouldMatch: o.minShouldMatch,
			Phrase:         o.phrase,
			Fuzziness:      o.fuzziness,
			PrefixTerms:    o.prefixTerms,
		}
	}
	resp, err := unary(ctx, l, req, l.client.SearchVertices)
	if err != nil {
		return SearchPage{}, err
	}
	pbHits := resp.GetHits()
	hits := make([]SearchHit, 0, len(pbHits))
	for _, h := range pbHits {
		hits = append(hits, SearchHit{
			Key:              h.GetKey(),
			Score:            h.GetScore(),
			Vertex:           h.GetVertex(),
			ProjectionStatus: h.GetProjectionStatus(),
		})
	}
	return SearchPage{
		Hits:                hits,
		NextCursor:          append([]byte(nil), resp.GetNextCursor()...),
		EffectiveLimit:      resp.GetEffectiveLimit(),
		Truncated:           resp.GetTruncated(),
		ContinuationLimited: resp.GetContinuationLimited(),
	}, nil
}

// SearchVerticesIter lazily yields every retained snapshot hit page by page.
// It never materializes an unbounded collection. When the server's bounded
// session cap prevented exhaustive retention it yields
// ErrSearchContinuationLimited after the final retained hit.
func (l *Lantern) SearchVerticesIter(ctx context.Context, query string, opts ...SearchOption) iter.Seq2[SearchHit, error] {
	return func(yield func(SearchHit, error) bool) {
		initial := searchOptions{}
		for _, apply := range opts {
			apply(&initial)
		}
		cursor := append([]byte(nil), initial.cursor...)
		for {
			pageOpts := append(append([]SearchOption(nil), opts...), WithSearchCursor(cursor))
			page, err := l.SearchVerticesPage(ctx, query, pageOpts...)
			if err != nil {
				var zero SearchHit
				yield(zero, err)
				return
			}
			for _, hit := range page.Hits {
				if !yield(hit, nil) {
					return
				}
			}
			if len(page.NextCursor) == 0 {
				if page.ContinuationLimited {
					var zero SearchHit
					yield(zero, ErrSearchContinuationLimited)
				}
				return
			}
			cursor = page.NextCursor
		}
	}
}
