package client

import (
	"context"
	"errors"
	"testing"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
)

func TestSearchOptions_Defaults(t *testing.T) {
	o := searchOptions{}
	if o.limit != 0 {
		t.Errorf("default limit = %d, want 0", o.limit)
	}
	if o.prefix != "" {
		t.Errorf("default prefix = %q, want empty", o.prefix)
	}
}

func TestSearchOptions_Apply(t *testing.T) {
	o := searchOptions{}
	for _, apply := range []SearchOption{
		WithSearchLimit(42),
		WithSearchPrefix("user."),
	} {
		apply(&o)
	}
	if o.limit != 42 {
		t.Errorf("limit = %d, want 42", o.limit)
	}
	if o.prefix != "user." {
		t.Errorf("prefix = %q, want %q", o.prefix, "user.")
	}
}

func TestValidateSearchOptions(t *testing.T) {
	valid := [][]SearchOption{
		nil,
		{WithFuzziness(1)},
		{WithMatchMode(MatchAny)},
		{WithMatchMode(MatchMinShould)},
		{WithMatchMode(MatchMinShould), WithMinShouldMatch(2)},
		{WithPhrase()},
	}
	for i, opts := range valid {
		if err := ValidateSearchOptions(opts...); err != nil {
			t.Errorf("valid case %d: %v", i, err)
		}
	}

	invalid := [][]SearchOption{
		{WithMatchMode(MatchMode(99))},
		{WithMinShouldMatch(1)},
		{WithMatchMode(MatchAny), WithMinShouldMatch(1)},
		{WithFuzziness(3)},
		{WithPhrase(), WithMatchMode(MatchAll)},
		{WithPhrase(), WithFuzziness(1)},
		{WithPhrase(), WithPrefixTerms()},
	}
	for i, opts := range invalid {
		if err := ValidateSearchOptions(opts...); !errors.Is(err, ErrInvalidArgument) {
			t.Errorf("invalid case %d: err = %v, want ErrInvalidArgument", i, err)
		}
	}
}

// captureSearch is a fake LanternServiceClient that records every
// SearchVerticesRequest it receives and replays a canned response (or error).
// It embeds the interface (left nil) so it satisfies the full surface while
// overriding only SearchVertices.
type captureSearch struct {
	graphv1connect.LanternServiceClient
	reqs []*pb.SearchVerticesRequest
	resp *pb.SearchVerticesResponse
	err  error
}

func (c *captureSearch) SearchVertices(_ context.Context, req *connect.Request[pb.SearchVerticesRequest]) (*connect.Response[pb.SearchVerticesResponse], error) {
	c.reqs = append(c.reqs, req.Msg)
	if c.err != nil {
		return nil, c.err
	}
	resp := c.resp
	if resp == nil {
		resp = &pb.SearchVerticesResponse{}
	}
	return connect.NewResponse(resp), nil
}

// TestSearchVertices verifies the forwarder's contracts: it wires
// query/limit/prefix onto the request, maps the ranked *pb.SearchHit slice to
// the SDK-native []SearchHit preserving order, treats a no-match response as a
// zero-hit success (empty, non-nil slice, nil error), and lifts a
// FAILED_PRECONDITION reply into the ErrFailedPrecondition sentinel.
func TestSearchVertices(t *testing.T) {
	newClient := func(t *testing.T) (*Lantern, *captureSearch) {
		t.Helper()
		l := mustLantern(t)
		capt := &captureSearch{}
		l.client = capt
		return l, capt
	}

	t.Run("forwards query, limit and prefix", func(t *testing.T) {
		l, capt := newClient(t)
		if _, err := l.SearchVertices(context.Background(), "calm tone",
			WithSearchLimit(7), WithSearchPrefix("user.")); err != nil {
			t.Fatalf("SearchVertices: %v", err)
		}
		if len(capt.reqs) != 1 {
			t.Fatalf("want 1 request, got %d", len(capt.reqs))
		}
		got := capt.reqs[0]
		if got.GetQuery() != "calm tone" {
			t.Errorf("query = %q, want %q", got.GetQuery(), "calm tone")
		}
		if got.GetLimit() != 7 {
			t.Errorf("limit = %d, want 7", got.GetLimit())
		}
		if got.GetPrefix() != "user." {
			t.Errorf("prefix = %q, want %q", got.GetPrefix(), "user.")
		}
	})

	t.Run("default omits limit and prefix", func(t *testing.T) {
		l, capt := newClient(t)
		if _, err := l.SearchVertices(context.Background(), "q"); err != nil {
			t.Fatalf("SearchVertices: %v", err)
		}
		got := capt.reqs[0]
		if got.GetLimit() != 0 {
			t.Errorf("default limit = %d, want 0 (server default)", got.GetLimit())
		}
		if got.GetPrefix() != "" {
			t.Errorf("default prefix = %q, want empty", got.GetPrefix())
		}
	})

	t.Run("invalid options fail locally without transport", func(t *testing.T) {
		l, capt := newClient(t)
		_, err := l.SearchVertices(context.Background(), "alpha beta", WithPhrase(), WithFuzziness(1))
		if !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("error = %v, want ErrInvalidArgument", err)
		}
		if len(capt.reqs) != 0 {
			t.Fatalf("transport received %d requests, want 0", len(capt.reqs))
		}
	})

	t.Run("maps ranked hits preserving order", func(t *testing.T) {
		l, capt := newClient(t)
		capt.resp = &pb.SearchVerticesResponse{Hits: []*pb.SearchHit{
			{Key: "user.preferences.tone", Score: 2.5},
			{Key: "user.preferences.format", Score: 1.0},
		}}
		hits, err := l.SearchVertices(context.Background(), "calm")
		if err != nil {
			t.Fatalf("SearchVertices: %v", err)
		}
		want := []SearchHit{
			{Key: "user.preferences.tone", Score: 2.5},
			{Key: "user.preferences.format", Score: 1.0},
		}
		if len(hits) != len(want) {
			t.Fatalf("got %d hits, want %d", len(hits), len(want))
		}
		for i := range want {
			if hits[i] != want[i] {
				t.Errorf("hit[%d] = %+v, want %+v", i, hits[i], want[i])
			}
		}
	})

	t.Run("no match yields empty non-nil slice and nil error", func(t *testing.T) {
		l, capt := newClient(t)
		capt.resp = &pb.SearchVerticesResponse{} // zero hits
		hits, err := l.SearchVertices(context.Background(), "nothing")
		if err != nil {
			t.Fatalf("no-match must be a success, got err: %v", err)
		}
		if hits == nil {
			t.Fatalf("no-match must return an empty non-nil slice, got nil")
		}
		if len(hits) != 0 {
			t.Fatalf("want 0 hits, got %d", len(hits))
		}
	})

	t.Run("FAILED_PRECONDITION maps to ErrFailedPrecondition", func(t *testing.T) {
		l, capt := newClient(t)
		connectErr := connect.NewError(connect.CodeFailedPrecondition,
			errors.New("vertex search is disabled on this server"))
		detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{
			Reason: pb.SearchErrorReason_SEARCH_DISABLED,
		})
		if detailErr != nil {
			t.Fatalf("NewErrorDetail: %v", detailErr)
		}
		connectErr.AddDetail(detail)
		capt.err = connectErr
		hits, err := l.SearchVertices(context.Background(), "q")
		if hits != nil {
			t.Errorf("want nil hits on error, got %v", hits)
		}
		if !errors.Is(err, ErrFailedPrecondition) {
			t.Fatalf("want errors.Is(err, ErrFailedPrecondition), got %v", err)
		}
		if !errors.Is(err, ErrSearchDisabled) {
			t.Fatalf("want errors.Is(err, ErrSearchDisabled), got %v", err)
		}
		if got := SearchFailureReason(err); got != SearchErrorReasonDisabled {
			t.Fatalf("SearchFailureReason = %v, want SEARCH_DISABLED", got)
		}
	})

	t.Run("positions reason remains distinct from disabled search", func(t *testing.T) {
		l, capt := newClient(t)
		connectErr := connect.NewError(connect.CodeFailedPrecondition,
			errors.New("phrase search requires positional postings"))
		detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{
			Reason: pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED,
		})
		if detailErr != nil {
			t.Fatalf("NewErrorDetail: %v", detailErr)
		}
		connectErr.AddDetail(detail)
		capt.err = connectErr
		_, err := l.SearchVertices(context.Background(), "alpha beta", WithPhrase())
		if !errors.Is(err, ErrSearchPositionsDisabled) {
			t.Fatalf("want errors.Is(err, ErrSearchPositionsDisabled), got %v", err)
		}
		if errors.Is(err, ErrSearchDisabled) {
			t.Fatalf("positions-disabled error incorrectly matches ErrSearchDisabled: %v", err)
		}
		if got := SearchFailureReason(err); got != SearchErrorReasonPositionsDisabled {
			t.Fatalf("SearchFailureReason = %v, want SEARCH_POSITIONS_DISABLED", got)
		}
	})

	t.Run("work budget carries typed sentinel and kind", func(t *testing.T) {
		l, capt := newClient(t)
		connectErr := connect.NewError(connect.CodeResourceExhausted, errors.New("posting budget"))
		detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{
			Reason:   pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED,
			WorkKind: "posting_visits",
		})
		if detailErr != nil {
			t.Fatalf("NewErrorDetail: %v", detailErr)
		}
		connectErr.AddDetail(detail)
		capt.err = connectErr
		_, err := l.SearchVertices(context.Background(), "alpha")
		if !errors.Is(err, ErrResourceExhausted) || !errors.Is(err, ErrSearchWorkBudget) {
			t.Fatalf("error = %v, want resource + search budget sentinels", err)
		}
		if got := SearchFailureReason(err); got != SearchErrorReasonWorkBudget {
			t.Fatalf("SearchFailureReason = %v, want work budget", got)
		}
		if got := SearchFailureWorkKind(err); got != "posting_visits" {
			t.Fatalf("SearchFailureWorkKind = %q, want posting_visits", got)
		}
	})

	t.Run("admission carries distinct typed sentinel", func(t *testing.T) {
		l, capt := newClient(t)
		connectErr := connect.NewError(connect.CodeResourceExhausted, errors.New("admission"))
		detail, detailErr := connect.NewErrorDetail(&pb.SearchErrorDetail{
			Reason: pb.SearchErrorReason_SEARCH_ADMISSION_SATURATED,
		})
		if detailErr != nil {
			t.Fatalf("NewErrorDetail: %v", detailErr)
		}
		connectErr.AddDetail(detail)
		capt.err = connectErr
		_, err := l.SearchVertices(context.Background(), "alpha")
		if !errors.Is(err, ErrResourceExhausted) || !errors.Is(err, ErrSearchAdmission) {
			t.Fatalf("error = %v, want resource + admission sentinels", err)
		}
		if got := SearchFailureReason(err); got != SearchErrorReasonAdmission {
			t.Fatalf("SearchFailureReason = %v, want admission", got)
		}
	})
}
