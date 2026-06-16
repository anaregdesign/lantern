package service

import (
	"context"
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
	if fb.searchCalls != 0 {
		t.Errorf("backend SearchVertices called %d times while disabled, want 0", fb.searchCalls)
	}
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
