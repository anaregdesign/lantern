package service

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

// errSearchDisabled is the FAILED_PRECONDITION payload returned when the
// SearchVertices RPC is invoked but the server-side index was never built
// (LANTERN_SEARCH_ENABLED=false). It is a fixed sentinel so clients and the
// admin UI can present a calm "search is turned off" state rather than
// treating it as a transient failure.
var errSearchDisabled = errors.New("vertex search is disabled on this server; set LANTERN_SEARCH_ENABLED=true to enable it")

var errSearchPositionsDisabled = errors.New("phrase search requires positional postings; set LANTERN_SEARCH_POSITIONS=true or omit phrase=true")

const (
	searchMaxFuzziness      = uint32(2)
	searchAnalyzerVersion   = "script-aware-v1"
	searchProjectionVersion = "vertex-key-value-v1"
)

// SearchVertices returns vertices ranked by full-text relevance over their
// indexed content (key + value) in stable (score DESC, raw key ASC) order. It
// is the content counterpart to ScanVertices' lexicographic key walk: callers
// search by remembered topic words instead of an exact key prefix.
//
// Gating: when the server was started without the search index
// (LANTERN_SEARCH_ENABLED=false) the RPC returns FAILED_PRECONDITION. The
// decision lives here, not in the cache, because the core backend returns an
// empty result for a disabled index, an unanalysable query, and a genuine
// no-match alike — only the composition root knows whether
// EnableSearchIndex was called (#624).
//
// Pagination: the request limit is clamped to (0, MaxLimit]; a zero value
// falls back to DefaultLimit. The hit count is bounded exactly by the
// clamped limit because the cache applies it after its liveness and prefix
// filters. An empty or unanalysable query yields zero hits (not an error).
// Prefix, when non-empty, scopes hits to vertices whose key carries it.
func (s *LanternService) SearchVertices(ctx context.Context, in *pb.SearchVerticesRequest) (*pb.SearchVerticesResponse, error) {
	start := time.Now()
	outcome, reason := "internal", "internal"
	var stats search.Stats
	span := trace.SpanFromContext(ctx)
	defer func() {
		s.metrics.OnSearchExecution(outcome, reason, stats)
		span.SetAttributes(
			attribute.String("lantern.search.outcome", outcome),
			attribute.String("lantern.search.reason", reason),
			attribute.Int64("lantern.search.query_terms", stats.QueryTerms),
			attribute.Int64("lantern.search.dictionary_visits", stats.DictionaryVisits),
			attribute.Int64("lantern.search.posting_visits", stats.PostingVisits),
			attribute.Int64("lantern.search.position_visits", stats.PositionVisits),
		)
	}()
	if err := ctx.Err(); err != nil {
		outcome, reason = searchContextOutcome(err)
		return nil, ctxToConnect(err)
	}
	if err := validateSearchOptions(in.GetOptions()); err != nil {
		outcome, reason = "invalid_argument", "invalid_options"
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if !s.search.Enabled {
		outcome, reason = "failed_precondition", "disabled"
		return nil, newSearchPreconditionError(pb.SearchErrorReason_SEARCH_DISABLED, errSearchDisabled)
	}
	if s.search.MaxQueryBytes > 0 && len(in.GetQuery()) > s.search.MaxQueryBytes {
		outcome, reason = "resource_exhausted", "query_bytes"
		return nil, newSearchResourceError(
			pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED,
			"query_bytes",
			fmt.Errorf("search query exceeds %d bytes", s.search.MaxQueryBytes),
		)
	}
	opts, phrase := s.resolveSearchOptions(in.GetOptions())
	if phrase && !s.search.PositionsEnabled {
		outcome, reason = "failed_precondition", "positions_disabled"
		return nil, newSearchPreconditionError(pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED, errSearchPositionsDisabled)
	}
	if s.search.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, s.search.Timeout)
		defer cancel()
	}
	if s.searchGate != nil {
		if s.searchGate.TryAcquire(1) {
			defer s.searchGate.Release(1)
		} else {
			outcome, reason = "resource_exhausted", "admission"
			return nil, newSearchResourceError(
				pb.SearchErrorReason_SEARCH_ADMISSION_SATURATED,
				"",
				errors.New("search admission capacity is saturated"),
			)
		}
	}
	limit := clampLimit(in.GetLimit(), s.search.DefaultLimit, s.search.MaxLimit)
	ranked, workStats, err := s.cache.SearchVerticesMatchContext(ctx, in.GetQuery(), int(limit), in.GetPrefix(), opts, phrase, s.search.WorkBudget)
	stats = workStats
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome, reason = searchContextOutcome(err)
			return nil, ctxToConnect(err)
		}
		var exhausted *search.BudgetExceededError
		if errors.As(err, &exhausted) {
			outcome, reason = "resource_exhausted", string(exhausted.Kind)
			return nil, newSearchResourceError(
				pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED,
				string(exhausted.Kind),
				err,
			)
		}
		outcome, reason = "internal", "internal"
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	hits := make([]*pb.SearchHit, 0, len(ranked))
	for _, r := range ranked {
		hits = append(hits, &pb.SearchHit{Key: r.ID, Score: r.Score})
	}
	s.metrics.OnSearch(len(hits), time.Since(start))
	outcome, reason = "ok", "none"
	return &pb.SearchVerticesResponse{Hits: hits}, nil
}

func searchContextOutcome(err error) (outcome, reason string) {
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded", "deadline"
	}
	return "canceled", "canceled"
}

// validateSearchOptions is the authoritative request-boundary decision table
// for SearchOptions. It rejects values that the backend would otherwise ignore
// or reinterpret, so every accepted field has one observable meaning (#1055).
func validateSearchOptions(o *pb.SearchOptions) error {
	if o == nil {
		return nil
	}
	switch o.GetMatchMode() {
	case pb.MatchMode_MATCH_MODE_UNSPECIFIED,
		pb.MatchMode_MATCH_MODE_ANY,
		pb.MatchMode_MATCH_MODE_ALL,
		pb.MatchMode_MATCH_MODE_MIN_SHOULD:
	default:
		return fmt.Errorf("search match_mode %d is not recognized", o.GetMatchMode())
	}
	if o.GetMinShouldMatch() != 0 && o.GetMatchMode() != pb.MatchMode_MATCH_MODE_MIN_SHOULD {
		return errors.New("search min_should_match requires match_mode MATCH_MODE_MIN_SHOULD")
	}
	if o.GetFuzziness() > searchMaxFuzziness {
		return fmt.Errorf("search fuzziness must be at most %d", searchMaxFuzziness)
	}
	if !o.GetPhrase() {
		return nil
	}
	if o.GetMatchMode() != pb.MatchMode_MATCH_MODE_UNSPECIFIED {
		return errors.New("search phrase cannot be combined with an explicit match_mode")
	}
	if o.GetMinShouldMatch() != 0 {
		return errors.New("search phrase cannot be combined with min_should_match")
	}
	if o.GetFuzziness() != 0 {
		return errors.New("search phrase cannot be combined with fuzziness")
	}
	if o.GetPrefixTerms() {
		return errors.New("search phrase cannot be combined with prefix_terms")
	}
	return nil
}

func newSearchPreconditionError(reason pb.SearchErrorReason, cause error) error {
	err := connect.NewError(connect.CodeFailedPrecondition, cause)
	detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{Reason: reason})
	if detailErr != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("marshal SearchErrorDetail: %w", detailErr))
	}
	err.AddDetail(detail)
	return err
}

func newSearchResourceError(reason pb.SearchErrorReason, workKind string, cause error) error {
	err := connect.NewError(connect.CodeResourceExhausted, cause)
	detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{Reason: reason, WorkKind: workKind})
	if detailErr != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("marshal SearchErrorDetail: %w", detailErr))
	}
	err.AddDetail(detail)
	return err
}

// resolveSearchOptions maps the request's SearchOptions to the core query
// options plus a phrase flag (#892). When the request omits options entirely the
// server defaults apply; when it sends them, the request values are taken
// literally — an unspecified match_mode still falls back to the default, but a
// zero fuzziness or false prefix_terms means off, so a client can turn any
// option off. validateSearchOptions has already rejected combinations the core
// backend cannot compose.
func (s *LanternService) resolveSearchOptions(o *pb.SearchOptions) (search.MatchOptions, bool) {
	if o == nil {
		return search.MatchOptions{Mode: s.search.DefaultMode, MinShouldMatch: int(s.search.DefaultMinShould)}, false
	}
	opts := search.MatchOptions{
		Mode:           matchModeFromPB(o.GetMatchMode(), s.search.DefaultMode),
		MinShouldMatch: int(o.GetMinShouldMatch()),
		Fuzziness:      int(o.GetFuzziness()),
		PrefixTerms:    o.GetPrefixTerms(),
	}
	if opts.Mode == search.MatchMinShould && opts.MinShouldMatch == 0 {
		opts.MinShouldMatch = int(s.search.DefaultMinShould)
	}
	return opts, o.GetPhrase()
}

// matchModeFromPB maps the wire enum to the core match mode, using fallback when
// the field is unspecified.
func matchModeFromPB(m pb.MatchMode, fallback search.MatchMode) search.MatchMode {
	switch m {
	case pb.MatchMode_MATCH_MODE_ANY:
		return search.MatchAny
	case pb.MatchMode_MATCH_MODE_ALL:
		return search.MatchAll
	case pb.MatchMode_MATCH_MODE_MIN_SHOULD:
		return search.MatchMinShould
	default:
		return fallback
	}
}

func matchModeToPB(m search.MatchMode) pb.MatchMode {
	switch m {
	case search.MatchAll:
		return pb.MatchMode_MATCH_MODE_ALL
	case search.MatchMinShould:
		return pb.MatchMode_MATCH_MODE_MIN_SHOULD
	default:
		return pb.MatchMode_MATCH_MODE_ANY
	}
}

// parseMatchMode maps a LANTERN_SEARCH_DEFAULT_MODE value to a core match mode
// and reports whether the spelling was recognised. The empty string is
// recognised as the default (MatchAny), so a bare `LANTERN_SEARCH_DEFAULT_MODE=`
// still means "use the default"; any other unrecognised value reports ok ==
// false so the caller can reject it (ValidateMatchMode) instead of laundering a
// typo into MatchAny. It is the single source of truth for the accepted
// spellings, shared by ParseMatchMode and ValidateMatchMode.
func parseMatchMode(s string) (search.MatchMode, bool) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "any":
		return search.MatchAny, true
	case "all":
		return search.MatchAll, true
	case "min-should", "minshould", "min_should":
		return search.MatchMinShould, true
	default:
		return search.MatchAny, false
	}
}

// ParseMatchMode maps a LANTERN_SEARCH_DEFAULT_MODE value to a core match mode.
// It is deliberately tolerant — the empty string and any unrecognised value
// resolve to MatchAny — because the provider validates the value at startup
// (ValidateMatchMode) before this wire seam converts provider config to service
// config, so an unrecognised value can no longer reach it in a booted server.
// Exported for that seam in package main.
func ParseMatchMode(s string) search.MatchMode {
	mode, _ := parseMatchMode(s)
	return mode
}

// ValidateMatchMode reports an error when s is not an accepted
// LANTERN_SEARCH_DEFAULT_MODE spelling — the canonical any|all|min-should, their
// documented aliases (minshould, min_should), or the empty string for "use the
// default". The provider calls it at startup so a typo (min_shold, AND, or)
// fails boot with the allowed values listed, rather than silently defaulting to
// "any" and changing server-wide ranking semantics (#911).
func ValidateMatchMode(s string) error {
	if _, ok := parseMatchMode(s); !ok {
		return fmt.Errorf("LANTERN_SEARCH_DEFAULT_MODE=%q: must be one of any|all|min-should", s)
	}
	return nil
}
