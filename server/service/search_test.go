package service

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
)

func TestSearchVertices_TerminalObservationExactlyOnce(t *testing.T) {
	tests := []struct {
		name        string
		limits      SearchLimits
		request     *pb.SearchVerticesRequest
		context     func() context.Context
		backend     func(*fakeBackend)
		wantOutcome string
		wantReason  string
	}{
		{
			name: "success", limits: SearchLimits{Enabled: true},
			request: &pb.SearchVerticesRequest{Query: "alpha"},
			backend: func(f *fakeBackend) {
				f.searchResults = []search.Result[string]{{ID: "doc", Score: 1}}
			},
			wantOutcome: "ok", wantReason: "none",
		},
		{
			name: "zero hit", limits: SearchLimits{Enabled: true},
			request:     &pb.SearchVerticesRequest{Query: "absent"},
			wantOutcome: "ok", wantReason: "no_hits",
		},
		{
			name: "disabled", request: &pb.SearchVerticesRequest{Query: "alpha"},
			wantOutcome: "failed_precondition", wantReason: "disabled",
		},
		{
			name: "invalid options", limits: SearchLimits{Enabled: true},
			request:     &pb.SearchVerticesRequest{Query: "alpha", Options: &pb.SearchOptions{Fuzziness: 99}},
			wantOutcome: "invalid_argument", wantReason: "invalid_options",
		},
		{
			name: "pre-canceled", limits: SearchLimits{Enabled: true},
			request: &pb.SearchVerticesRequest{Query: "alpha"},
			context: func() context.Context {
				ctx, cancel := context.WithCancel(context.Background())
				cancel()
				return ctx
			},
			wantOutcome: "canceled", wantReason: "canceled",
		},
		{
			name: "query bytes", limits: SearchLimits{Enabled: true, MaxQueryBytes: 1},
			request:     &pb.SearchVerticesRequest{Query: "alpha"},
			wantOutcome: "resource_exhausted", wantReason: "query_bytes",
		},
		{
			name: "positions disabled", limits: SearchLimits{Enabled: true},
			request:     &pb.SearchVerticesRequest{Query: "alpha beta", Options: &pb.SearchOptions{Phrase: true}},
			wantOutcome: "failed_precondition", wantReason: "positions_disabled",
		},
		{
			name: "index incomplete", limits: SearchLimits{Enabled: true},
			request: &pb.SearchVerticesRequest{Query: "alpha"},
			backend: func(f *fakeBackend) {
				f.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
					return nil, search.Stats{}, search.ErrIndexIncomplete
				}
			},
			wantOutcome: "failed_precondition", wantReason: "index_incomplete",
		},
		{
			name: "work budget", limits: SearchLimits{Enabled: true},
			request: &pb.SearchVerticesRequest{Query: "alpha"},
			backend: func(f *fakeBackend) {
				f.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
					return nil, search.Stats{PostingVisits: 2}, &search.BudgetExceededError{Kind: search.WorkPostingVisits, Limit: 1}
				}
			},
			wantOutcome: "resource_exhausted", wantReason: string(search.WorkPostingVisits),
		},
		{
			name: "deadline", limits: SearchLimits{Enabled: true},
			request: &pb.SearchVerticesRequest{Query: "alpha"},
			backend: func(f *fakeBackend) {
				f.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
					return nil, search.Stats{}, context.DeadlineExceeded
				}
			},
			wantOutcome: "deadline_exceeded", wantReason: "deadline",
		},
		{
			name: "internal", limits: SearchLimits{Enabled: true},
			request: &pb.SearchVerticesRequest{Query: "alpha"},
			backend: func(f *fakeBackend) {
				f.searchContextFn = func(context.Context) ([]search.Result[string], search.Stats, error) {
					return nil, search.Stats{}, errors.New("backend failure")
				}
			},
			wantOutcome: "internal", wantReason: "internal",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend()
			if tc.backend != nil {
				tc.backend(fb)
			}
			metrics := &fakeHotPathMetrics{}
			svc := NewLanternService(fb).WithSearchLimits(tc.limits).WithHotPathMetrics(metrics)
			ctx := context.Background()
			if tc.context != nil {
				ctx = tc.context()
			}
			_, _ = svc.SearchVertices(ctx, tc.request)
			if len(metrics.searchExecution) != 1 {
				t.Fatalf("terminal observations = %d, want exactly 1", len(metrics.searchExecution))
			}
			got := metrics.searchExecution[0]
			if got.Outcome != tc.wantOutcome || got.Reason != tc.wantReason {
				t.Fatalf("terminal observation = %+v, want outcome=%s reason=%s", got, tc.wantOutcome, tc.wantReason)
			}
			if tc.wantReason == "query_bytes" && got.Stats.QueryBytes != int64(len(tc.request.GetQuery())) {
				t.Errorf("query-bytes rejection work = %d, want %d", got.Stats.QueryBytes, len(tc.request.GetQuery()))
			}
			if got.TotalDuration <= 0 {
				t.Errorf("terminal duration = %v, want > 0", got.TotalDuration)
			}
		})
	}
}

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
		wantLimit uint32
	}{
		{"zero falls back to default", SearchLimits{Enabled: true, DefaultLimit: 25, MaxLimit: 1000}, 0, "user.", 25},
		{"over max is capped", SearchLimits{Enabled: true, DefaultLimit: 25, MaxLimit: 50}, 9999, "", 50},
		{"in range passes through", SearchLimits{Enabled: true, DefaultLimit: 25, MaxLimit: 50}, 10, "project.", 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fb := newFakeBackend()
			svc := NewLanternService(fb).WithSearchLimits(tc.limits)
			resp, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
				Query:  "q",
				Limit:  tc.reqLimit,
				Prefix: tc.reqPrefix,
			})
			if err != nil {
				t.Fatalf("SearchVertices: %v", err)
			}
			if resp.GetEffectiveLimit() != tc.wantLimit {
				t.Errorf("effective limit = %d, want %d", resp.GetEffectiveLimit(), tc.wantLimit)
			}
			if fb.lastSearchLimit != svc.search.MaxSessionHits+1 {
				t.Errorf("backend snapshot limit = %d, want %d", fb.lastSearchLimit, svc.search.MaxSessionHits+1)
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

func TestSearchVertices_CursorPaginationSnapshot(t *testing.T) {
	fb := newFakeBackend()
	fb.searchResults = []search.Result[string]{
		{ID: "a", Score: 5}, {ID: "b", Score: 4}, {ID: "c", Score: 3},
		{ID: "d", Score: 2}, {ID: "e", Score: 1},
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled: true, DefaultLimit: 2, MaxLimit: 2,
		CursorTTL: time.Minute, MaxSessions: 4, MaxSessionHits: 10, MaxSessionBytes: 1 << 20,
	})
	request := &pb.SearchVerticesRequest{Query: "alpha", Limit: 2}
	first, err := svc.SearchVertices(context.Background(), request)
	if err != nil {
		t.Fatal(err)
	}
	assertSearchPage(t, first, []string{"a", "b"}, true, false, true)

	// The continuation owns the ranked snapshot; later backend mutation cannot
	// insert, remove, or reorder an already-issued page chain.
	fb.searchResults = []search.Result[string]{{ID: "z", Score: 100}}
	second, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
		Query: "alpha", Limit: 2, Cursor: first.GetNextCursor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSearchPage(t, second, []string{"c", "d"}, true, false, true)
	third, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
		Query: "alpha", Limit: 2, Cursor: second.GetNextCursor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSearchPage(t, third, []string{"e"}, false, false, false)
	if fb.searchCalls != 1 {
		t.Fatalf("backend searches = %d, want 1 snapshot build", fb.searchCalls)
	}

	_, err = svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
		Query: "different", Limit: 2, Cursor: first.GetNextCursor(),
	})
	if connect.CodeOf(err) != connect.CodeInvalidArgument || SearchFailureReasonForTest(err) != pb.SearchErrorReason_SEARCH_CURSOR_INVALID {
		t.Fatalf("request mismatch err = %v", err)
	}
}

func TestSearchVertices_FullVertexSnapshotAndContinuationLimit(t *testing.T) {
	fb := newFakeBackend()
	for i, key := range []string{"a", "b", "c", "d", "e"} {
		fb.searchResults = append(fb.searchResults, search.Result[string]{ID: key, Score: float64(5 - i)})
		fb.vertices[key] = &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "original-" + key}}
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled: true, DefaultLimit: 2, MaxLimit: 2,
		CursorTTL: time.Minute, MaxSessions: 4, MaxSessionHits: 4, MaxSessionBytes: 1 << 20,
	})
	first, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
		Query: "alpha", Limit: 2, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX,
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSearchPage(t, first, []string{"a", "b"}, true, true, true)
	for _, hit := range first.GetHits() {
		if hit.GetProjectionStatus() != pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT || hit.GetVertex().GetString_() != "original-"+hit.GetKey() {
			t.Fatalf("FULL_VERTEX hit = %+v", hit)
		}
	}
	fb.vertices["a"].Value = &pb.Vertex_String_{String_: "replacement"}
	if got := first.GetHits()[0].GetVertex().GetString_(); got != "original-a" {
		t.Fatalf("response vertex = %q after backend mutation, want original-a", got)
	}
	fb.vertices["c"].Value = &pb.Vertex_String_{String_: "replacement"}
	second, err := svc.SearchVertices(context.Background(), &pb.SearchVerticesRequest{
		Query: "alpha", Limit: 2, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX,
		Cursor: first.GetNextCursor(),
	})
	if err != nil {
		t.Fatal(err)
	}
	assertSearchPage(t, second, []string{"c", "d"}, true, true, false)
	if got := second.GetHits()[0].GetVertex().GetString_(); got != "original-c" {
		t.Fatalf("snapshot vertex = %q, want original-c", got)
	}
}

func BenchmarkSearchVertices_FullVertexSinglePage(b *testing.B) {
	const hitCount = 100
	fb := newFakeBackend()
	value := strings.Repeat("search projection payload ", 128)
	for i := range hitCount {
		key := "vertex-" + strconv.Itoa(i)
		fb.searchResults = append(fb.searchResults, search.Result[string]{
			ID:    key,
			Score: float64(hitCount - i),
		})
		fb.vertices[key] = &pb.Vertex{
			Key:   key,
			Value: &pb.Vertex_String_{String_: value},
		}
	}
	svc := NewLanternService(fb).WithSearchLimits(SearchLimits{
		Enabled:         true,
		DefaultLimit:    hitCount,
		MaxLimit:        hitCount,
		CursorTTL:       time.Minute,
		MaxSessions:     4,
		MaxSessionHits:  hitCount,
		MaxSessionBytes: 1 << 30,
	})
	request := &pb.SearchVerticesRequest{
		Query:      "payload",
		Limit:      hitCount,
		Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX,
	}

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		response, err := svc.SearchVertices(context.Background(), request)
		if err != nil {
			b.Fatal(err)
		}
		if len(response.GetHits()) != hitCount {
			b.Fatalf("hits = %d, want %d", len(response.GetHits()), hitCount)
		}
	}
}

func assertSearchPage(t *testing.T, page *pb.SearchVerticesResponse, wantKeys []string, truncated, limited, hasNext bool) {
	t.Helper()
	got := make([]string, 0, len(page.GetHits()))
	for _, hit := range page.GetHits() {
		got = append(got, hit.GetKey())
	}
	if strings.Join(got, ",") != strings.Join(wantKeys, ",") {
		t.Fatalf("keys = %v, want %v", got, wantKeys)
	}
	if page.GetTruncated() != truncated || page.GetContinuationLimited() != limited || (len(page.GetNextCursor()) > 0) != hasNext {
		t.Fatalf("page flags = truncated:%t limited:%t next:%t", page.GetTruncated(), page.GetContinuationLimited(), len(page.GetNextCursor()) > 0)
	}
}

func SearchFailureReasonForTest(err error) pb.SearchErrorReason {
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		return pb.SearchErrorReason_SEARCH_ERROR_REASON_UNSPECIFIED
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr == nil {
			if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
				return searchDetail.GetReason()
			}
		}
	}
	return pb.SearchErrorReason_SEARCH_ERROR_REASON_UNSPECIFIED
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
	if len(fm.searchExecution) != 1 || fm.searchExecution[0].Outcome != "resource_exhausted" ||
		fm.searchExecution[0].Reason != string(search.WorkPostingVisits) || fm.searchExecution[0].Stats.PostingVisits != 3 {
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
	fm := &fakeHotPathMetrics{}
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
	}).WithHotPathMetrics(fm)

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
	if len(fm.searchExecution) != 1 || fm.searchExecution[0].Outcome != "resource_exhausted" || fm.searchExecution[0].Reason != "admission" {
		t.Fatalf("observations before release = %+v, want exactly one admission rejection", fm.searchExecution)
	}
	close(release)
	if err := <-firstDone; err != nil {
		t.Fatalf("first request: %v", err)
	}
	if len(fm.searchExecution) != 2 {
		t.Fatalf("terminal observations = %+v, want one per request", fm.searchExecution)
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
		return nil, search.Stats{QueryTerms: 2, DictionaryVisits: 3, PostingVisits: 5, PositionVisits: 7, CandidateVisits: 11, CandidateSkips: 4}, nil
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
		"lantern.search.reason":            "no_hits",
		"lantern.search.query_terms":       int64(2),
		"lantern.search.dictionary_visits": int64(3),
		"lantern.search.posting_visits":    int64(5),
		"lantern.search.position_visits":   int64(7),
		"lantern.search.candidate_visits":  int64(11),
		"lantern.search.candidate_skips":   int64(4),
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
