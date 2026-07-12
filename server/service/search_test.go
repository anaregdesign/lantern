package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"connectrpc.com/connect"

	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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
			if got := searchDetail.GetReason(); got != want {
				t.Fatalf("reason = %v, want %v", got, want)
			}
			return
		}
	}
	t.Fatalf("SearchErrorDetail not found in %v", err)
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
