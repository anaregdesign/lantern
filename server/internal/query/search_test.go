package query

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type searchReaderStub struct {
	capture      func(context.Context, Spec) (CaptureResult, error)
	readPage     func(func() error) error
	captureCalls int
	pageCalls    int
	lastSpec     Spec
}

func (r *searchReaderStub) Capture(ctx context.Context, spec Spec) (CaptureResult, error) {
	r.captureCalls++
	r.lastSpec = spec
	if r.capture != nil {
		return r.capture(ctx, spec)
	}
	return CaptureResult{Hits: []*pb.SearchHit{}}, nil
}

func (r *searchReaderStub) ReadPage(capture func() error) error {
	r.pageCalls++
	if r.readPage != nil {
		return r.readPage(capture)
	}
	return capture()
}

func querySearchErrorDetail(t *testing.T, err error) *pb.SearchErrorDetail {
	t.Helper()
	var failure *connect.Error
	if !errors.As(err, &failure) {
		t.Fatalf("error = %v, want Connect error", err)
	}
	for _, detail := range failure.Details() {
		value, decodeErr := detail.Value()
		if decodeErr != nil {
			t.Fatal(decodeErr)
		}
		if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
			return searchDetail
		}
	}
	t.Fatalf("error = %v, want SearchErrorDetail", err)
	return nil
}

func TestSearchVerticesTerminalObservation(t *testing.T) {
	for _, tc := range []struct {
		name        string
		limits      Limits
		request     *pb.SearchVerticesRequest
		preCanceled bool
		result      CaptureResult
		cutError    error
		wantCode    connect.Code
		wantOutcome string
		wantReason  string
		wantDetail  pb.SearchErrorReason
		wantCapture bool
	}{
		{name: "success", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{Hits: []*pb.SearchHit{{Key: "a", Score: 2}}}, wantOutcome: "ok", wantReason: "none", wantCapture: true},
		{name: "zero hits", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "absent"}, result: CaptureResult{Hits: []*pb.SearchHit{}}, wantOutcome: "ok", wantReason: "no_hits", wantCapture: true},
		{name: "invalid options", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Options: &pb.SearchOptions{Fuzziness: 99}}, wantCode: connect.CodeInvalidArgument, wantOutcome: "invalid_argument", wantReason: "invalid_options"},
		{name: "invalid projection", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Projection: pb.SearchProjection(99)}, wantCode: connect.CodeInvalidArgument, wantOutcome: "invalid_argument", wantReason: "invalid_projection"},
		{name: "query bytes", limits: Limits{Enabled: true, MaxQueryBytes: 1}, request: &pb.SearchVerticesRequest{Query: "alpha"}, wantCode: connect.CodeResourceExhausted, wantOutcome: "resource_exhausted", wantReason: "query_bytes", wantDetail: pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED},
		{name: "disabled", request: &pb.SearchVerticesRequest{Query: "alpha"}, wantCode: connect.CodeFailedPrecondition, wantOutcome: "failed_precondition", wantReason: "disabled", wantDetail: pb.SearchErrorReason_SEARCH_DISABLED},
		{name: "positions disabled", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Options: &pb.SearchOptions{Phrase: true}}, wantCode: connect.CodeFailedPrecondition, wantOutcome: "failed_precondition", wantReason: "positions_disabled", wantDetail: pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED},
		{name: "pre canceled", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, preCanceled: true, wantCode: connect.CodeCanceled, wantOutcome: "canceled", wantReason: "canceled"},
		{name: "index incomplete", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{BackendError: search.ErrIndexIncomplete}, wantCode: connect.CodeFailedPrecondition, wantOutcome: "failed_precondition", wantReason: "index_incomplete", wantDetail: pb.SearchErrorReason_SEARCH_INDEX_INCOMPLETE, wantCapture: true},
		{name: "budget", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{Stats: search.Stats{PostingVisits: 3}, BackendError: &search.BudgetExceededError{Kind: search.WorkPostingVisits, Limit: 2}}, wantCode: connect.CodeResourceExhausted, wantOutcome: "resource_exhausted", wantReason: string(search.WorkPostingVisits), wantDetail: pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED, wantCapture: true},
		{name: "backend canceled", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{BackendError: fmt.Errorf("backend: %w", context.Canceled)}, wantCode: connect.CodeCanceled, wantOutcome: "canceled", wantReason: "canceled", wantCapture: true},
		{name: "backend deadline", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{BackendError: context.DeadlineExceeded}, wantCode: connect.CodeDeadlineExceeded, wantOutcome: "deadline_exceeded", wantReason: "deadline", wantCapture: true},
		{name: "backend internal", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{BackendError: errors.New("backend failed")}, wantCode: connect.CodeInternal, wantOutcome: "internal", wantReason: "internal", wantCapture: true},
		{name: "read cut overrides backend", limits: Limits{Enabled: true}, request: &pb.SearchVerticesRequest{Query: "alpha"}, result: CaptureResult{BackendError: search.ErrIndexIncomplete}, cutError: connect.NewError(connect.CodeUnavailable, errors.New("publication changed")), wantCode: connect.CodeUnavailable, wantOutcome: "failed_precondition", wantReason: "publication_gap", wantCapture: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &searchReaderStub{capture: func(context.Context, Spec) (CaptureResult, error) { return tc.result, tc.cutError }}
			ctx := t.Context()
			if tc.preCanceled {
				var cancel context.CancelFunc
				ctx, cancel = context.WithCancel(ctx)
				cancel()
			}
			var observations []Observation
			response, err := New(tc.limits).SearchVertices(ctx, tc.request, reader, func(o Observation) { observations = append(observations, o) })
			if tc.wantOutcome == "ok" {
				if err != nil || response == nil || response.Hits == nil {
					t.Fatalf("response = (%+v, %v), want owned nonnil hit slice", response, err)
				}
			} else if response != nil || connect.CodeOf(err) != tc.wantCode {
				t.Fatalf("response = (%+v, %v), want code %v", response, err, tc.wantCode)
			}
			if len(observations) != 1 {
				t.Fatalf("terminal observations = %d, want 1", len(observations))
			}
			got := observations[0]
			if got.Outcome != tc.wantOutcome || got.Reason != tc.wantReason || got.TotalDuration <= 0 {
				t.Fatalf("observation = %+v, want %s/%s", got, tc.wantOutcome, tc.wantReason)
			}
			if (reader.captureCalls == 1) != tc.wantCapture || reader.pageCalls != 0 {
				t.Fatalf("reader calls = capture:%d page:%d, want capture:%t", reader.captureCalls, reader.pageCalls, tc.wantCapture)
			}
			if tc.wantDetail != pb.SearchErrorReason_SEARCH_ERROR_REASON_UNSPECIFIED {
				if detail := querySearchErrorDetail(t, err); detail.GetReason() != tc.wantDetail {
					t.Fatalf("detail = %+v, want reason %v", detail, tc.wantDetail)
				}
			}
			if tc.wantReason == "query_bytes" && got.Stats.QueryBytes != int64(len(tc.request.Query)) {
				t.Fatalf("query bytes = %d, want %d", got.Stats.QueryBytes, len(tc.request.Query))
			}
			if tc.wantReason == string(search.WorkPostingVisits) && got.Stats.PostingVisits != 3 {
				t.Fatalf("posting work = %d, want 3", got.Stats.PostingVisits)
			}
		})
	}
}

func TestSearchVerticesOptionsAndLimits(t *testing.T) {
	for _, tc := range []struct {
		name    string
		options *pb.SearchOptions
		want    search.MatchOptions
		phrase  bool
	}{
		{"omitted uses defaults", nil, search.MatchOptions{Mode: search.MatchMinShould, MinShouldMatch: 5}, false},
		{"explicit zeros preserve mode defaults", &pb.SearchOptions{}, search.MatchOptions{Mode: search.MatchMinShould, MinShouldMatch: 5}, false},
		{"explicit any", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY, Fuzziness: 2, PrefixTerms: true}, search.MatchOptions{Mode: search.MatchAny, Fuzziness: 2, PrefixTerms: true}, false},
		{"explicit minimum", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD, MinShouldMatch: 2}, search.MatchOptions{Mode: search.MatchMinShould, MinShouldMatch: 2}, false},
		{"phrase", &pb.SearchOptions{Phrase: true}, search.MatchOptions{Mode: search.MatchMinShould, MinShouldMatch: 5}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			budget := search.Budget{MaxPostingVisits: 17}
			engine := New(Limits{Enabled: true, PositionsEnabled: true, DefaultMode: search.MatchMinShould, DefaultMinShould: 5, WorkBudget: budget, MaxSessionHits: 23})
			reader := &searchReaderStub{}
			_, err := engine.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "alpha", Prefix: "visible:", Options: tc.options, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}, reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			spec := reader.lastSpec
			if spec.Query != "alpha" || spec.Prefix != "visible:" || spec.Options != tc.want || spec.Phrase != tc.phrase || spec.Budget != budget || spec.Projection != pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX {
				t.Fatalf("spec = %+v, want options %+v phrase %t", spec, tc.want, tc.phrase)
			}
		})
	}
	for _, tc := range []struct {
		name                string
		requested, def, max uint32
		want                uint32
	}{
		{"default", 0, 2, 10, 2},
		{"explicit", 4, 2, 10, 4},
		{"explicit capped", 20, 2, 10, 10},
		{"default capped", 0, 20, 10, 10},
	} {
		t.Run(tc.name, func(t *testing.T) {
			reader := &searchReaderStub{}
			response, err := New(Limits{Enabled: true, DefaultLimit: tc.def, MaxLimit: tc.max, MaxSessionHits: 3}).SearchVertices(t.Context(), &pb.SearchVerticesRequest{Limit: tc.requested}, reader, nil)
			if err != nil || response.GetEffectiveLimit() != tc.want || reader.lastSpec.Limit != max(3, int(tc.want))+1 {
				t.Fatalf("limit = (%+v, %v), retained capture limit %d, want page %d", response, err, reader.lastSpec.Limit, tc.want)
			}
		})
	}
}

func TestSearchVerticesRetainedPaging(t *testing.T) {
	limits := Limits{Enabled: true, DefaultLimit: 2, MaxLimit: 2, CursorTTL: time.Minute, MaxSessionHits: 4}
	engine := New(limits)
	owned := []*pb.SearchHit{}
	for _, key := range []string{"a", "b", "c", "d", "e"} {
		owned = append(owned, &pb.SearchHit{Key: key, Vertex: &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "original-" + key}}, ProjectionStatus: pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT})
	}
	reader := &searchReaderStub{capture: func(context.Context, Spec) (CaptureResult, error) { return CaptureResult{Hits: owned}, nil }}
	request := &pb.SearchVerticesRequest{Query: "alpha", Limit: 2, Projection: pb.SearchProjection_SEARCH_PROJECTION_FULL_VERTEX}
	first, err := engine.SearchVertices(t.Context(), request, reader, nil)
	if err != nil || len(first.GetHits()) != 2 || len(first.GetNextCursor()) == 0 || !first.GetTruncated() || !first.GetContinuationLimited() {
		t.Fatalf("first page = (%+v, %v)", first, err)
	}
	owned[2].Vertex.Value = &pb.Vertex_String_{String_: "changed"}
	request.Cursor = first.NextCursor
	second, err := engine.SearchVertices(t.Context(), request, reader, nil)
	if err != nil || len(second.GetHits()) != 2 || second.Hits[0].Key != "c" || second.Hits[1].Key != "d" || second.Hits[0].Vertex.GetString_() != "original-c" || len(second.NextCursor) != 0 || !second.Truncated || !second.ContinuationLimited {
		t.Fatalf("second page = (%+v, %v)", second, err)
	}
	second.Hits[0].Vertex.Value = &pb.Vertex_String_{String_: "caller mutation"}
	replayed, err := engine.SearchVertices(t.Context(), request, reader, nil)
	if err != nil || replayed.Hits[0].Vertex.GetString_() != "original-c" || reader.captureCalls != 1 || reader.pageCalls != 2 {
		t.Fatalf("replay = (%+v, %v), capture:%d page:%d", replayed, err, reader.captureCalls, reader.pageCalls)
	}
	_, err = New(limits).SearchVertices(t.Context(), request, reader, nil)
	if connect.CodeOf(err) != connect.CodeInvalidArgument || querySearchErrorDetail(t, err).Reason != pb.SearchErrorReason_SEARCH_CURSOR_INVALID || reader.captureCalls != 1 {
		t.Fatalf("other endpoint accepted cursor or requeried: %v", err)
	}
}

func TestSearchVerticesCursorGuardPrecedence(t *testing.T) {
	for _, validCursor := range []bool{false, true} {
		t.Run(fmt.Sprintf("valid cursor %t", validCursor), func(t *testing.T) {
			engine := New(Limits{Enabled: true, DefaultLimit: 1, MaxLimit: 1})
			reader := &searchReaderStub{capture: func(context.Context, Spec) (CaptureResult, error) {
				return CaptureResult{Hits: []*pb.SearchHit{{Key: "a"}, {Key: "b"}}}, nil
			}}
			first, err := engine.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "alpha"}, reader, nil)
			if err != nil {
				t.Fatal(err)
			}
			cursor := []byte("invalid")
			if validCursor {
				cursor = first.NextCursor
			}
			guardError := connect.NewError(connect.CodeUnavailable, errors.New("publication changed"))
			callbacks := 0
			reader.readPage = func(capture func() error) error {
				callbacks++
				if err := capture(); err != nil {
					t.Fatalf("page callback returned its internal error: %v", err)
				}
				return guardError
			}
			var observations []Observation
			response, err := engine.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "alpha", Cursor: cursor}, reader, func(o Observation) { observations = append(observations, o) })
			if response != nil || err != guardError || callbacks != 1 || reader.pageCalls != 1 || reader.captureCalls != 1 {
				t.Fatalf("guard response = (%+v, %v), callbacks:%d page:%d capture:%d", response, err, callbacks, reader.pageCalls, reader.captureCalls)
			}
			if len(observations) != 1 || observations[0].Reason != "publication_gap" {
				t.Fatalf("observations = %+v", observations)
			}
		})
	}
}

func TestSearchVerticesExpiredCursorHasTypedStaleError(t *testing.T) {
	engine := New(Limits{Enabled: true, DefaultLimit: 1, MaxLimit: 1, CursorTTL: time.Minute})
	now := time.Unix(10000, 0)
	engine.sessions.now = func() time.Time { return now }
	reader := &searchReaderStub{capture: func(context.Context, Spec) (CaptureResult, error) {
		return CaptureResult{Hits: []*pb.SearchHit{{Key: "a"}, {Key: "b"}}}, nil
	}}
	first, err := engine.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "alpha"}, reader, nil)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	_, err = engine.SearchVertices(t.Context(), &pb.SearchVerticesRequest{Query: "alpha", Cursor: first.NextCursor}, reader, nil)
	if connect.CodeOf(err) != connect.CodeAborted || querySearchErrorDetail(t, err).Reason != pb.SearchErrorReason_SEARCH_CURSOR_STALE || reader.captureCalls != 1 {
		t.Fatalf("expired cursor = %v, capture calls:%d", err, reader.captureCalls)
	}
}
