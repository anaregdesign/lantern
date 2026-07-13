package service

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"strings"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"
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
	searchAnalyzerVersion   = "script-aware-v2"
	searchProjectionVersion = "vertex-fields-v2"
)

// SearchObservation is the one terminal telemetry record emitted for every
// SearchVertices handler invocation. Request dimensions are bounded enums or
// booleans; query text, prefixes, matched keys, and values never cross this
// observability boundary.
type SearchObservation struct {
	Mode          string
	Phrase        bool
	Fuzziness     uint32
	PrefixTerms   bool
	PrefixPresent bool
	Outcome       string
	Reason        string
	Results       int
	TotalDuration time.Duration
	Stats         search.Stats
}

// SearchVertices returns vertices ranked by full-text relevance over their
// field-separated key and value content in stable (score DESC, raw key ASC) order. It
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
	observation := searchObservationForRequest(in)
	span := trace.SpanFromContext(ctx)
	defer func() {
		observation.TotalDuration = time.Since(start)
		s.metrics.OnSearchExecution(observation)
		span.SetAttributes(
			attribute.String("lantern.search.mode", observation.Mode),
			attribute.Bool("lantern.search.phrase", observation.Phrase),
			attribute.String("lantern.search.fuzziness", searchFuzzinessLabel(observation.Fuzziness)),
			attribute.Bool("lantern.search.prefix_terms", observation.PrefixTerms),
			attribute.Bool("lantern.search.prefix_present", observation.PrefixPresent),
			attribute.String("lantern.search.outcome", observation.Outcome),
			attribute.String("lantern.search.reason", observation.Reason),
			attribute.Int("lantern.search.results", observation.Results),
			attribute.Int64("lantern.search.query_bytes", observation.Stats.QueryBytes),
			attribute.Int64("lantern.search.query_tokens", observation.Stats.QueryTokens),
			attribute.Int64("lantern.search.query_clauses", observation.Stats.QueryClauses),
			attribute.Int64("lantern.search.query_terms", observation.Stats.QueryTerms),
			attribute.Int64("lantern.search.dictionary_visits", observation.Stats.DictionaryVisits),
			attribute.Int64("lantern.search.expansions_retained", observation.Stats.ExpansionRetained),
			attribute.Int64("lantern.search.posting_visits", observation.Stats.PostingVisits),
			attribute.Int64("lantern.search.position_visits", observation.Stats.PositionVisits),
			attribute.Int64("lantern.search.expiration_visits", observation.Stats.ExpirationVisits),
			attribute.Int64("lantern.search.candidate_visits", observation.Stats.CandidateVisits),
			attribute.Int64("lantern.search.candidate_skips", observation.Stats.CandidateSkips),
			attribute.Float64("lantern.search.analysis_duration_seconds", observation.Stats.AnalysisDuration.Seconds()),
			attribute.Float64("lantern.search.expansion_duration_seconds", observation.Stats.ExpansionDuration.Seconds()),
			attribute.Float64("lantern.search.selection_duration_seconds", observation.Stats.SelectionDuration.Seconds()),
			attribute.Float64("lantern.search.total_duration_seconds", observation.TotalDuration.Seconds()),
		)
	}()
	if err := ctx.Err(); err != nil {
		observation.Outcome, observation.Reason = searchContextOutcome(err)
		return nil, ctxToConnect(err)
	}
	if err := validateSearchOptions(in.GetOptions()); err != nil {
		observation.Outcome, observation.Reason = "invalid_argument", "invalid_options"
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	projection, err := normalizeSearchProjection(in.GetProjection())
	if err != nil {
		observation.Outcome, observation.Reason = "invalid_argument", "invalid_projection"
		return nil, connect.NewError(connect.CodeInvalidArgument, err)
	}
	if !s.search.Enabled {
		observation.Outcome, observation.Reason = "failed_precondition", "disabled"
		return nil, newSearchPreconditionError(pb.SearchErrorReason_SEARCH_DISABLED, errSearchDisabled)
	}
	if s.search.MaxQueryBytes > 0 && len(in.GetQuery()) > s.search.MaxQueryBytes {
		observation.Stats.QueryBytes = int64(len(in.GetQuery()))
		observation.Outcome, observation.Reason = "resource_exhausted", "query_bytes"
		return nil, newSearchResourceError(
			pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED,
			"query_bytes",
			fmt.Errorf("search query exceeds %d bytes", s.search.MaxQueryBytes),
		)
	}
	opts, phrase := s.resolveSearchOptions(in.GetOptions())
	if phrase && !s.search.PositionsEnabled {
		observation.Outcome, observation.Reason = "failed_precondition", "positions_disabled"
		return nil, newSearchPreconditionError(pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED, errSearchPositionsDisabled)
	}
	limit := clampLimit(in.GetLimit(), s.search.DefaultLimit, s.search.MaxLimit)
	requestHash, err := searchRequestHash(in, limit, projection)
	if err != nil {
		observation.Outcome, observation.Reason = "internal", "internal"
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	configFingerprint := s.SearchConfigFingerprint()
	if len(in.GetCursor()) > 0 {
		hits, next, truncated, limited, pageErr := s.searchSessions.page(in.GetCursor(), requestHash, configFingerprint, int(limit))
		if pageErr != nil {
			switch {
			case errors.Is(pageErr, errSearchCursorStale):
				observation.Outcome, observation.Reason = "aborted", "cursor_stale"
				return nil, newSearchAbortedError(pb.SearchErrorReason_SEARCH_CURSOR_STALE, pageErr)
			default:
				observation.Outcome, observation.Reason = "invalid_argument", "cursor_invalid"
				return nil, newSearchInvalidCursorError(pb.SearchErrorReason_SEARCH_CURSOR_INVALID, pageErr)
			}
		}
		observation.Results = len(hits)
		observation.Outcome, observation.Reason = "ok", "none"
		return &pb.SearchVerticesResponse{
			Hits:                hits,
			NextCursor:          next,
			EffectiveLimit:      limit,
			Truncated:           truncated,
			ContinuationLimited: limited,
		}, nil
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
			observation.Outcome, observation.Reason = "resource_exhausted", "admission"
			return nil, newSearchResourceError(
				pb.SearchErrorReason_SEARCH_ADMISSION_SATURATED,
				"",
				errors.New("search admission capacity is saturated"),
			)
		}
	}
	retainedLimit := s.searchSessions.maxRetainedHits()
	if retainedLimit < int(limit) {
		retainedLimit = int(limit)
	}
	queryLimit := retainedLimit + 1
	var hits []*pb.SearchHit
	var workStats search.Stats
	if projection == pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX {
		var snapshotErr error
		snapshot, stats, snapshotErr := s.cache.SearchVerticesSnapshotContext(ctx, in.GetQuery(), queryLimit, in.GetPrefix(), opts, phrase, s.search.WorkBudget)
		workStats = stats
		err = snapshotErr
		if err == nil {
			hits = make([]*pb.SearchHit, 0, len(snapshot))
			for _, ranked := range snapshot {
				hit := &pb.SearchHit{Key: ranked.Result.ID, Score: ranked.Result.Score}
				if ranked.Found && ranked.Value != nil {
					hit.Vertex = proto.Clone(ranked.Value).(*pb.Vertex)
					hit.ProjectionStatus = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT
				} else {
					hit.ProjectionStatus = pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_MISSING
				}
				hits = append(hits, hit)
			}
		}
	} else {
		ranked, stats, rankedErr := s.cache.SearchVerticesMatchContext(ctx, in.GetQuery(), queryLimit, in.GetPrefix(), opts, phrase, s.search.WorkBudget)
		workStats = stats
		err = rankedErr
		if err == nil {
			hits = make([]*pb.SearchHit, 0, len(ranked))
			for _, result := range ranked {
				hits = append(hits, &pb.SearchHit{
					Key:              result.ID,
					Score:            result.Score,
					ProjectionStatus: pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_KEY_SCORE,
				})
			}
		}
	}
	observation.Stats = workStats
	if err != nil {
		if errors.Is(err, search.ErrIndexIncomplete) {
			observation.Outcome, observation.Reason = "failed_precondition", "index_incomplete"
			return nil, newSearchPreconditionError(pb.SearchErrorReason_SEARCH_INDEX_INCOMPLETE, err)
		}
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			observation.Outcome, observation.Reason = searchContextOutcome(err)
			return nil, ctxToConnect(err)
		}
		var exhausted *search.BudgetExceededError
		if errors.As(err, &exhausted) {
			observation.Outcome, observation.Reason = "resource_exhausted", string(exhausted.Kind)
			return nil, newSearchResourceError(
				pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED,
				string(exhausted.Kind),
				err,
			)
		}
		observation.Outcome, observation.Reason = "internal", "internal"
		return nil, connect.NewError(connect.CodeInternal, err)
	}
	limited := len(hits) > retainedLimit
	if limited {
		hits = hits[:retainedLimit]
	}
	pageEnd := min(int(limit), len(hits))
	pageHits := cloneSearchHits(hits[:pageEnd])
	truncated := pageEnd < len(hits) || limited
	var next []byte
	continuationLimited := limited
	if pageEnd < len(hits) {
		var admitted bool
		next, admitted, err = s.searchSessions.create(requestHash, configFingerprint, hits, limited, pageEnd)
		if err != nil {
			observation.Outcome, observation.Reason = "internal", "internal"
			return nil, connect.NewError(connect.CodeInternal, err)
		}
		if !admitted {
			continuationLimited = true
		}
	}
	observation.Results = len(pageHits)
	observation.Outcome = "ok"
	if len(pageHits) == 0 {
		observation.Reason = "no_hits"
	} else {
		observation.Reason = "none"
	}
	return &pb.SearchVerticesResponse{
		Hits:                pageHits,
		NextCursor:          next,
		EffectiveLimit:      limit,
		Truncated:           truncated,
		ContinuationLimited: continuationLimited,
	}, nil
}

func normalizeSearchProjection(projection pb.SearchProjection) (pb.SearchProjection, error) {
	switch projection {
	case pb.SearchProjection_SEARCH_PROJECTION_UNSPECIFIED,
		pb.SearchProjection_SEARCH_PROJECTION_KEY_SCORE:
		return pb.SearchProjection_SEARCH_PROJECTION_KEY_SCORE, nil
	case pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX:
		return projection, nil
	default:
		return 0, fmt.Errorf("search projection %d is not recognized", projection)
	}
}

func searchRequestHash(in *pb.SearchVerticesRequest, limit uint32, projection pb.SearchProjection) ([32]byte, error) {
	canonical := proto.Clone(in).(*pb.SearchVerticesRequest)
	canonical.Cursor = nil
	canonical.Limit = limit
	canonical.Projection = projection
	raw, err := (proto.MarshalOptions{Deterministic: true}).Marshal(canonical)
	if err != nil {
		return [32]byte{}, fmt.Errorf("marshal search cursor request: %w", err)
	}
	return sha256.Sum256(raw), nil
}

func searchObservationForRequest(in *pb.SearchVerticesRequest) SearchObservation {
	o := in.GetOptions()
	return SearchObservation{
		Mode:          searchModeLabel(o),
		Phrase:        o.GetPhrase(),
		Fuzziness:     o.GetFuzziness(),
		PrefixTerms:   o.GetPrefixTerms(),
		PrefixPresent: in.GetPrefix() != "",
		Outcome:       "internal",
		Reason:        "internal",
	}
}

func searchModeLabel(o *pb.SearchOptions) string {
	if o == nil || o.GetMatchMode() == pb.MatchMode_MATCH_MODE_UNSPECIFIED {
		return "server"
	}
	switch o.GetMatchMode() {
	case pb.MatchMode_MATCH_MODE_ANY:
		return "any"
	case pb.MatchMode_MATCH_MODE_ALL:
		return "all"
	case pb.MatchMode_MATCH_MODE_MIN_SHOULD:
		return "min_should"
	default:
		return "unknown"
	}
}

func searchFuzzinessLabel(value uint32) string {
	if value <= searchMaxFuzziness {
		return fmt.Sprintf("%d", value)
	}
	return "other"
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

func newSearchInvalidCursorError(reason pb.SearchErrorReason, cause error) error {
	err := connect.NewError(connect.CodeInvalidArgument, cause)
	detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{Reason: reason})
	if detailErr != nil {
		return connect.NewError(connect.CodeInternal, fmt.Errorf("marshal SearchErrorDetail: %w", detailErr))
	}
	err.AddDetail(detail)
	return err
}

func newSearchAbortedError(reason pb.SearchErrorReason, cause error) error {
	err := connect.NewError(connect.CodeAborted, cause)
	detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{Reason: reason})
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
