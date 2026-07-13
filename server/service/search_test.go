package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSearchVertices_DisabledReturnsFailedPrecondition(t *testing.T) {
	// A service built with the default limits has search disabled (opt-in
	// at the composition root), so the RPC must refuse before touching the
	// backend.
	fb := newFakeBackend()
	fb.searchResults = []search.Result[string]{{ID: "users/1", Score: 1}}
	svc := NewLanternService(fb)

	_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "alice"})
	if err == nil {
		t.Fatalf("expected error when search disabled")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	assertSearchErrorReason(t, err, pb.SearchErrorReason_SEARCH_DISABLED)
	if fb.searchCalls != 0 {
		t.Errorf("backend SearchVertices called %d times while disabled, want 0", fb.searchCalls)
	}
}

func TestSearchVertices_PhraseWithoutPositionsFailsClosed(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled:          true,
		PositionsEnabled: false,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})

	_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
		Query:   "alpha beta",
		Options: &pb.SearchOptions{Phrase: true},
	})
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	assertSearchErrorReason(t, err, pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED)
	if fb.searchCalls != 0 {
		t.Errorf("backend SearchVertices called %d times, want 0", fb.searchCalls)
	}
}

func TestSearchVertices_RejectsInvalidOptionsBeforeBackend(t *testing.T) {
	tests := []struct {
		name string
		opts *pb.SearchOptions
	}{
		{"unknown match mode", &pb.SearchOptions{MatchMode: pb.MatchMode(99)}},
		{"minimum with unspecified mode", &pb.SearchOptions{MinShouldMatch: 1}},
		{"minimum with any", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY, MinShouldMatch: 1}},
		{"fuzziness above capability", &pb.SearchOptions{Fuzziness: 3}},
		{"phrase with explicit mode", &pb.SearchOptions{Phrase: true, MatchMode: pb.MatchMode_MATCH_MODE_ALL}},
		{"phrase with fuzziness", &pb.SearchOptions{Phrase: true, Fuzziness: 1}},
		{"phrase with prefix terms", &pb.SearchOptions{Phrase: true, PrefixTerms: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend()
			// Keep search disabled to prove malformed requests are rejected at
			// the request boundary before feature gating or backend access.
			svc := NewLanternService(fb)
			_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
				Query:   "alpha beta",
				Options: tc.opts,
			})
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument (err=%v)", got, err)
			}
			if fb.searchCalls != 0 {
				t.Errorf("backend SearchVertices called %d times, want 0", fb.searchCalls)
			}
		})
	}
}

func TestSearchVertices_AcceptsOptionsDecisionTable(t *testing.T) {
	tests := []struct {
		name string
		opts *pb.SearchOptions
	}{
		{"omitted", nil},
		{"all zero", &pb.SearchOptions{}},
		{"explicit any", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY}},
		{"explicit all", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL}},
		{"minimum server threshold", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD}},
		{"minimum explicit threshold", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD, MinShouldMatch: 2}},
		{"fuzzy with server mode", &pb.SearchOptions{Fuzziness: 1}},
		{"prefix terms with server mode", &pb.SearchOptions{PrefixTerms: true}},
		{"phrase alone", &pb.SearchOptions{Phrase: true}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend()
			svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
				Enabled:          true,
				PositionsEnabled: true,
				DefaultLimit:     100,
				MaxLimit:         1000,
			})
			_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "alpha beta", Options: tc.opts})
			if err != nil {
				t.Fatalf("SearchVertices: %v", err)
			}
			if fb.searchCalls != 1 {
				t.Errorf("backend SearchVertices called %d times, want 1", fb.searchCalls)
			}
		})
	}
}

func assertSearchErrorReason(t *testing.T, err error, want pb.SearchErrorReason) {
	t.Helper()
	searchDetail := searchErrorDetail(t, err)
	if got := searchDetail.GetReason(); got != want {
		t.Fatalf("reason = %v, want %v", got, want)
	}
}

func searchErrorDetail(t *testing.T, err error) *pb.SearchErrorDetail {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error %T is not *connect.Error", err)
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatalf("decode detail: %v", valueErr)
		}
		if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
			return searchDetail
		}
	}
	t.Fatalf("SearchErrorDetail not found in %v", err)
	return nil
}

func TestSearchVertices_EnabledMapsRankedHits(t *testing.T) {
	fb := newFakeBackend()
	fb.searchResults = []search.Result[string]{
		{ID: "users/3", Score: 9.5},
		{ID: "users/1", Score: 4.0},
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{Enabled: true, DefaultLimit: 100, MaxLimit: 1000})

	resp, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "alice"})
	if err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	if len(resp.Hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(resp.Hits))
	}
	if resp.Hits[0].Key != "users/3" || resp.Hits[0].Score != 9.5 {
		t.Errorf("hit[0] = %+v, want {users/3 9.5}", resp.Hits[0])
	}
	if resp.Hits[1].Key != "users/1" || resp.Hits[1].Score != 4.0 {
		t.Errorf("hit[1] = %+v, want {users/1 4.0}", resp.Hits[1])
	}
}

func TestSearchVertices_EmptyResultIsEmptyNotError(t *testing.T) {
	fb := newFakeBackend()
	fb.searchResults = nil // core returns nil for no-match / empty query alike
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{Enabled: true, DefaultLimit: 100, MaxLimit: 1000})

	resp, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: ""})
	if err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	if resp.Hits == nil {
		t.Fatalf("hits should be non-nil empty slice, got nil")
	}
	if len(resp.Hits) != 0 {
		t.Fatalf("hits = %d, want 0", len(resp.Hits))
	}
}

func TestSearchVertices_ClampsLimitAndForwardsPrefix(t *testing.T) {
	tests := []struct {
		name      string
		limits    SearchLimits
		reqLimit  uint32
		reqPrefix string
		wantLimit int
	}{
		{"zero falls back to default", SearchLimits{Enabled: true, DefaultLimit: 25, MaxLimit: 1000}, 0, "user.", 25},
		{"over max is capped", SearchLimits{Enabled: true, DefaultLimit: 25, MaxLimit: 50}, 9999, "", 50},
		{"in range passes through", SearchLimits{Enabled: true, DefaultLimit: 25, MaxLimit: 50}, 10, "project.", 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend()
			svc := NewLanternService(fb).WithSearchLimits(tc.limits)
			_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
				Query:  "q",
				Limit:  tc.reqLimit,
				Prefix: tc.reqPrefix,
			})
			if err != nil {
				t.Fatalf("SearchVertices: %v", err)
			}
			if fb.lastSearchLimit != tc.wantLimit {
				t.Errorf("backend limit = %d, want %d", fb.lastSearchLimit, tc.wantLimit)
			}
			if fb.lastSearchPrefix != tc.reqPrefix {
				t.Errorf("backend prefix = %q, want %q", fb.lastSearchPrefix, tc.reqPrefix)
			}
			if fb.lastSearchQuery != "q" {
				t.Errorf("backend query = %q, want %q", fb.lastSearchQuery, "q")
			}
		})
	}
}

func TestSearchVertices_CanceledContext(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{Enabled: true, DefaultLimit: 100, MaxLimit: 1000})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := svc.SearchVertices(ctx, &pb.SearchVerticesRequest{Query: "alice"})
	if err == nil {
		t.Fatalf("expected error on canceled context")
	}
	if got := connect.CodeOf(err); got != connect.CodeCanceled {
		t.Fatalf("code = %v, want Canceled", got)
	}
	if fb.searchCalls != 0 {
		t.Errorf("backend called despite canceled context")
	}
}

func TestSearchVertices_RejectsOversizeQuery(t *testing.T) {
	fb := newFakeBackend()
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled:       true,
		DefaultLimit:  10,
		MaxLimit:      100,
		MaxQueryBytes: 4,
	})

	_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "12345"})
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", got)
	}
	detail := searchErrorDetail(t, err)
	if detail.GetReason() != pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED || detail.GetWorkKind() != "query_bytes" {
		t.Fatalf("detail = %+v, want work-budget/query_bytes", detail)
	}
	if fb.searchCalls != 0 {
		t.Fatalf("backend calls = %d, want 0", fb.searchCalls)
	}
}

func TestSearchVertices_MapsWorkBudgetExhaustion(t *testing.T) {
	fb := newFakeBackend()
	fm := &fakeHotPathMetrics{}
	fb.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
		return nil, search.Stats{PostingVisits: 3}, &search.BudgetExceededError{Kind: search.WorkPostingVisits, Limit: 2}
	}
	wantBudget := search.Budget{MaxQueryTerms: 1, MaxDictionaryVisits: 2, MaxPostingVisits: 3, MaxPositionVisits: 4, MaxExpirationVisits: 5}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled:      true,
		DefaultLimit: 10,
		MaxLimit:     100,
		WorkBudget:   wantBudget,
	}).WithHotPathMetrics(fm)

	resp, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "alpha"})
	if resp != nil {
		t.Fatalf("response = %+v, want nil on exhaustion", resp)
	}
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", got)
	}
	detail := searchErrorDetail(t, err)
	if detail.GetReason() != pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED || detail.GetWorkKind() != string(search.WorkPostingVisits) {
		t.Fatalf("detail = %+v, want posting budget exhaustion", detail)
	}
	if fb.lastSearchBudget != wantBudget {
		t.Fatalf("backend budget = %+v, want %+v", fb.lastSearchBudget, wantBudget)
	}
	if len(fm.searchExecution) != 1 || fm.searchExecution[0].outcome != "resource_exhausted" ||
		fm.searchExecution[0].reason != string(search.WorkPostingVisits) || fm.searchExecution[0].stats.PostingVisits != 3 {
		t.Fatalf("execution observations = %+v, want one bounded posting exhaustion", fm.searchExecution)
	}
}

func TestSearchVertices_AppliesServerTimeout(t *testing.T) {
	fb := newFakeBackend()
	fb.searchContextFn = func(ctx context.Context) ([]search.Result[string], search.Stats, error) {
		if _, ok := ctx.Deadline(); !ok {
			t.Error("backend context has no server deadline")
		}
		<-ctx.Done()
		return nil, search.Stats{}, ctx.Err()
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled:      true,
		DefaultLimit: 10,
		MaxLimit:     100,
		Timeout:      10 * time.Millisecond,
	})

	_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "alpha"})
	if got := connect.CodeOf(err); got != connect.CodeDeadlineExceeded {
		t.Fatalf("code = %v, want DeadlineExceeded", got)
	}
}

func TestSearchVertices_AdmissionSaturation(t *testing.T) {
	fb := newFakeBackend()
	started := make(chan struct{})
	release := make(chan struct{})
	fb.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
		close(started)
		<-release
		return nil, search.Stats{}, nil
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled:      true,
		DefaultLimit: 10,
		MaxLimit:     100,
		MaxInFlight:  1,
	})

	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "first"})
		firstDone <- err
	}()
	<-started

	_, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{Query: "second"})
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted", got)
	}
	detail := searchErrorDetail(t, err)
	if detail.GetReason() != pb.SearchErrorReason_SEARCH_ADMISSION_SATURATED {
		t.Fatalf("reason = %v, want admission saturated", detail.GetReason())
	}
	if fb.searchCalls != 1 {
		t.Fatalf("backend calls = %d, want only admitted request", fb.searchCalls)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request: %v", err)
	}
}

func TestSearchVertices_CancelsInFlightBackend(t *testing.T) {
	fb := newFakeBackend()
	started := make(chan struct{})
	fb.searchContextFn = func(ctx context.Context) ([]search.Result[string], search.Stats, error) {
		close(started)
		<-ctx.Done()
		return nil, search.Stats{}, ctx.Err()
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{Enabled: true, DefaultLimit: 10, MaxLimit: 100})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := svc.SearchVertices(ctx, &pb.SearchVerticesRequest{Query: "alpha"})
		done <- err
	}()
	<-started
	cancel()
	if err := <-done; connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("error = %v, want Canceled", err)
	}
}

func TestSearchVertices_RecordsBoundedTraceWork(t *testing.T) {
	recorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
	defer func() { _ = provider.Shutdown(context.Background()) }()
	ctx, span := provider.Tracer("test").Start(context.Background(), "search")
	fb := newFakeBackend()
	fb.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
		return nil, search.Stats{QueryTerms: 2, DictionaryVisits: 3, PostingVisits: 5, PositionVisits: 7}, nil
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{Enabled: true, DefaultLimit: 10, MaxLimit: 100})
	if _, err := svc.SearchVertices(ctx, &pb.SearchVerticesRequest{Query: "alpha beta"}); err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	span.End()

	ended := recorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("ended spans = %d, want 1", len(ended))
	}
	attrs := map[string]any{}
	for _, attr := range ended[0].Attributes() {
		attrs[string(attr.Key)] = attr.Value.AsInterface()
	}
	for key, want := range map[string]any{
		"lantern.search.outcome":           "ok",
		"lantern.search.reason":            "none",
		"lantern.search.query_terms":       int64(2),
		"lantern.search.dictionary_visits": int64(3),
		"lantern.search.posting_visits":    int64(5),
		"lantern.search.position_visits":   int64(7),
	} {
		if got := attrs[key]; got != want {
			t.Errorf("attribute %s = %#v, want %#v", key, got, want)
		}
	}
}

func TestParseMatchMode(t *testing.T) {
	tests := []struct {
		in   string
		want search.MatchMode
	}{
		{"", search.MatchAny},
		{"any", search.MatchAny},
		{"ANY", search.MatchAny},
		{"  all  ", search.MatchAll},
		{"min-should", search.MatchMinShould},
		{"minshould", search.MatchMinShould},
		{"min_should", search.MatchMinShould},
		// Unrecognised launders to MatchAny — a dead path in a booted server
		// because the provider rejects the value at startup, but kept tolerant
		// so the wire seam never has to handle an error.
		{"min_shold", search.MatchAny},
		{"or", search.MatchAny},
	}
	for _, tc := range tests {
		if got := ParseMatchMode(tc.in); got != tc.want {
			t.Errorf("ParseMatchMode(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
}

func TestValidateMatchMode(t *testing.T) {
	t.Run("accepts canonical, aliases, and empty", func(t *testing.T) {
		for _, in := range []string{"", "any", "all", "min-should", "minshould", "min_should", "  ALL  "} {
			if err := ValidateMatchMode(in); err != nil {
				t.Errorf("ValidateMatchMode(%q) = %v, want nil", in, err)
			}
		}
	})
	t.Run("rejects a typo naming the value and allowed set", func(t *testing.T) {
		err := ValidateMatchMode("min_shold")
		if err == nil {
			t.Fatal("ValidateMatchMode accepted a typo")
		}
		for _, want := range []string{"min_shold", "any|all|min-should"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err, want)
			}
		}
	})
}
