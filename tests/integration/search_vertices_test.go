package integration_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"math"
	"math/rand"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/anaregdesign/lantern/core/graphcache"
	"github.com/anaregdesign/lantern/core/search"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/service"
)

// searchVertexDocument mirrors the production provider projection
// (server/provider/search.go) closely enough for the wire test: it folds
// the key together with the string value so a query matches either. The
// integration suite cannot import the unexported provider helper, and the
// exact value-type rendering is covered by the provider unit test — here we
// only need a live index behind the RPC.
func searchVertexDocument(v *pb.Vertex) search.Document {
	var b strings.Builder
	b.WriteString(v.GetKey())
	if s := v.GetString_(); s != "" {
		b.WriteByte(' ')
		b.WriteString(s)
	}
	return search.Text(b.String())
}

func wireSearchErrorReason(t *testing.T, err error) pb.SearchErrorReason {
	t.Helper()
	return wireSearchErrorDetail(t, err).GetReason()
}

func wireSearchErrorDetail(t *testing.T, err error) *pb.SearchErrorDetail {
	t.Helper()
	var connectErr *connect.Error
	if !errors.As(err, &connectErr) {
		t.Fatalf("error %T is not *connect.Error", err)
	}
	for _, detail := range connectErr.Details() {
		value, valueErr := detail.Value()
		if valueErr != nil {
			t.Fatalf("decode error detail: %v", valueErr)
		}
		if searchDetail, ok := value.(*pb.SearchErrorDetail); ok {
			return searchDetail
		}
	}
	t.Fatalf("SearchErrorDetail missing from wire error: %v", err)
	return nil
}

// newSearchRawClient stands up a Connect server whose GraphCache has the
// search index enabled (or not) and whose service gates SearchVertices on
// the matching SearchLimits.Enabled flag — the same two-sided agreement the
// production composition root makes.
func newSearchRawClient(t *testing.T, enabled bool) graphv1connect.LanternServiceClient {
	t.Helper()
	return newSearchRawClientWithLimits(t, service.SearchLimits{
		Enabled:          enabled,
		PositionsEnabled: enabled,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})
}

func newSearchRawClientWithLimits(t *testing.T, limits service.SearchLimits) graphv1connect.LanternServiceClient {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	if limits.Enabled {
		cache.EnableSearchIndex(searchVertexDocument, strings.Compare, graphcache.WithSearchAnalysisLimits(limits.AnalysisLimits))
	}
	svc := service.NewLanternService(cache).WithSearchLimits(limits)
	srv := newConnectTestServer(t, svc, nil)
	return graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
}

func TestSearchIndexWriteBudgetsOverWire(t *testing.T) {
	limits := service.SearchLimits{
		Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100,
		AnalysisLimits: search.SearchAnalysisLimits{
			MaxDocumentBytes: 8, MaxDocumentTokens: 20, MaxDocumentTerms: 20,
			MaxLiveTerms: 20, MaxLivePostings: 20, MaxPositionEntries: 20,
			CompactionRatio: 2, CompactionMinRetired: 1,
		},
	}
	c := newSearchRawClientWithLimits(t, limits)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exp := timestamppb.New(time.Now().Add(time.Hour))
	_, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "a", Value: &pb.Vertex_String_{String_: "ok"}, Expiration: exp},
		{Key: "b", Value: &pb.Vertex_String_{String_: "oversized"}, Expiration: exp},
	}}))
	if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
		t.Fatalf("code = %v, want ResourceExhausted (err=%v)", got, err)
	}
	detail := wireSearchErrorDetail(t, err)
	if detail.GetReason() != pb.SearchErrorReason_SEARCH_INDEX_BUDGET_EXHAUSTED || detail.GetWorkKind() != string(search.LimitDocumentBytes) {
		t.Fatalf("detail = %+v", detail)
	}
	for _, key := range []string{"a", "b"} {
		_, getErr := c.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: key}))
		if connect.CodeOf(getErr) != connect.CodeNotFound {
			t.Fatalf("GetVertex(%q) err = %v, batch was not atomic", key, getErr)
		}
	}
	if _, err := c.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{Key: "c", Value: &pb.Vertex_String_{String_: "ok"}, Expiration: exp}})); err != nil {
		t.Fatalf("small PutVertex: %v", err)
	}
}

func TestSearchIndexRestoreConvergenceOverWire(t *testing.T) {
	analysis := search.SearchAnalysisLimits{
		MaxDocumentBytes: 8, MaxDocumentTokens: 20, MaxDocumentTerms: 20,
		MaxLiveTerms: 20, MaxLivePostings: 20, MaxPositionEntries: 20,
		CompactionRatio: 2, CompactionMinRetired: 1,
	}
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(key string) string { return key })
	cache.EnableSearchIndex(searchVertexDocument, strings.Compare, graphcache.WithSearchAnalysisLimits(analysis))
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100, AnalysisLimits: analysis})
	exp := timestamppb.New(time.Now().Add(time.Hour))
	if _, err := svc.RestoreVertices(context.Background(), &pb.PutVerticesRequest{Vertices: []*pb.Vertex{{Key: "legacy", Value: &pb.Vertex_String_{String_: "oversized"}, Expiration: exp}}}); err != nil {
		t.Fatal(err)
	}

	srv := newConnectTestServer(t, svc, nil)
	c := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if got, err := c.GetVertex(ctx, connect.NewRequest(&pb.GetVertexRequest{Key: "legacy"})); err != nil || got.Msg.GetVertex().GetString_() != "oversized" {
		t.Fatalf("restored graph value = %+v err=%v", got, err)
	}
	_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "legacy"}))
	if connect.CodeOf(err) != connect.CodeFailedPrecondition || wireSearchErrorReason(t, err) != pb.SearchErrorReason_SEARCH_INDEX_INCOMPLETE {
		t.Fatalf("incomplete search err = %v", err)
	}
	if _, err := c.DeleteVertex(ctx, connect.NewRequest(&pb.DeleteVertexRequest{Key: "legacy"})); err != nil {
		t.Fatal(err)
	}
	if _, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "legacy"})); err != nil {
		t.Fatalf("search after automatic bounded rebuild: %v", err)
	}
}

type wireSearchExecution struct {
	outcome string
	reason  string
	stats   search.Stats
}

type wireSearchMetrics struct {
	executions chan wireSearchExecution
}

func (m *wireSearchMetrics) OnIlluminate(string, string, string, string, int, int, time.Duration, time.Duration) {
}
func (m *wireSearchMetrics) OnIlluminateResult(string, string, string, string, string, string) {
}
func (m *wireSearchMetrics) OnScan(string, int, time.Duration) {}
func (m *wireSearchMetrics) OnSearch(int, time.Duration)       {}
func (m *wireSearchMetrics) OnSearchExecution(outcome, reason string, stats search.Stats) {
	m.executions <- wireSearchExecution{outcome: outcome, reason: reason, stats: stats}
}
func (m *wireSearchMetrics) OnBatch(string, int)      {}
func (m *wireSearchMetrics) OnGetVertices(int, int)   {}
func (m *wireSearchMetrics) OnGetEdges(int, int)      {}
func (m *wireSearchMetrics) OnEdgeContribDeduped(int) {}

// TestSearchVertices_OptionsContract proves the #1055 decision table over the
// real Connect/h2c path. In particular, adding fuzziness must not turn an
// omitted match mode into ANY when this server is configured for ALL.
func TestSearchVertices_OptionsContract(t *testing.T) {
	c := newSearchRawClientWithLimits(t, service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: true,
		DefaultLimit:     100,
		MaxLimit:         1000,
		DefaultMode:      search.MatchAll,
		DefaultMinShould: 2,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	exp := timestamppb.New(time.Now().Add(time.Hour))
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: []*pb.Vertex{
		{Key: "doc.both", Value: &pb.Vertex_String_{String_: "alpha beta"}, Expiration: exp},
		{Key: "doc.alpha", Value: &pb.Vertex_String_{String_: "alpha"}, Expiration: exp},
		{Key: "doc.beta", Value: &pb.Vertex_String_{String_: "beta"}, Expiration: exp},
	}})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	keys := func(query string, options *pb.SearchOptions) []string {
		t.Helper()
		resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: query, Options: options}))
		if err != nil {
			t.Fatalf("SearchVertices(%q, %+v): %v", query, options, err)
		}
		got := make([]string, len(resp.Msg.GetHits()))
		for i, hit := range resp.Msg.GetHits() {
			got[i] = hit.GetKey()
		}
		sort.Strings(got)
		return got
	}
	assertKeys := func(name string, got, want []string) {
		t.Helper()
		if !slices.Equal(got, want) {
			t.Errorf("%s keys = %v, want %v", name, got, want)
		}
	}

	assertKeys("omitted uses ALL", keys("alpha beta", nil), []string{"doc.both"})
	assertKeys("fuzziness preserves omitted ALL", keys("alpga beta", &pb.SearchOptions{Fuzziness: 1}), []string{"doc.both"})
	assertKeys("explicit ANY", keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY}), []string{"doc.alpha", "doc.beta", "doc.both"})
	assertKeys("MIN zero uses server threshold", keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD}), []string{"doc.both"})
	assertKeys("MIN explicit one", keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_MIN_SHOULD, MinShouldMatch: 1}), []string{"doc.alpha", "doc.beta", "doc.both"})

	invalid := []struct {
		name string
		opts *pb.SearchOptions
	}{
		{"unknown mode", &pb.SearchOptions{MatchMode: pb.MatchMode(99)}},
		{"minimum without explicit MIN", &pb.SearchOptions{MinShouldMatch: 1}},
		{"minimum with ANY", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ANY, MinShouldMatch: 1}},
		{"fuzziness out of range", &pb.SearchOptions{Fuzziness: 3}},
		{"phrase with explicit mode", &pb.SearchOptions{Phrase: true, MatchMode: pb.MatchMode_MATCH_MODE_ALL}},
		{"phrase with fuzziness", &pb.SearchOptions{Phrase: true, Fuzziness: 1}},
		{"phrase with prefix terms", &pb.SearchOptions{Phrase: true, PrefixTerms: true}},
	}
	for _, tc := range invalid {
		t.Run(tc.name, func(t *testing.T) {
			_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha beta", Options: tc.opts}))
			if got := connect.CodeOf(err); got != connect.CodeInvalidArgument {
				t.Fatalf("code = %v, want InvalidArgument (err=%v)", got, err)
			}
		})
	}
}

func TestSearchVertices_ExecutionBudgetsOverWire(t *testing.T) {
	tests := []struct {
		name     string
		limits   service.SearchLimits
		query    string
		options  *pb.SearchOptions
		seed     []string
		workKind string
	}{
		{
			name:     "query bytes",
			limits:   service.SearchLimits{Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100, MaxQueryBytes: 4},
			query:    "12345",
			workKind: "query_bytes",
		},
		{
			name:     "query terms",
			limits:   service.SearchLimits{Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100, WorkBudget: search.Budget{MaxQueryTerms: 1}},
			query:    "alpha beta",
			workKind: string(search.WorkQueryTerms),
		},
		{
			name:     "dictionary visits",
			limits:   service.SearchLimits{Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100, WorkBudget: search.Budget{MaxDictionaryVisits: 1}},
			query:    "zzzzz",
			options:  &pb.SearchOptions{Fuzziness: 1},
			seed:     []string{"alpha", "bravo", "charlie"},
			workKind: string(search.WorkDictionaryVisits),
		},
		{
			name:     "posting visits",
			limits:   service.SearchLimits{Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100, WorkBudget: search.Budget{MaxPostingVisits: 1}},
			query:    "alpha",
			seed:     []string{"alpha", "alpha", "alpha"},
			workKind: string(search.WorkPostingVisits),
		},
		{
			name:     "position visits",
			limits:   service.SearchLimits{Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100, WorkBudget: search.Budget{MaxPositionVisits: 1}},
			query:    "alpha beta",
			options:  &pb.SearchOptions{Phrase: true},
			seed:     []string{"alpha beta"},
			workKind: string(search.WorkPositionVisits),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := newSearchRawClientWithLimits(t, tc.limits)
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if len(tc.seed) > 0 {
				vertices := make([]*pb.Vertex, len(tc.seed))
				for i, value := range tc.seed {
					vertices[i] = &pb.Vertex{
						Key:        fmt.Sprintf("budget/%s/%d", strings.ReplaceAll(tc.name, " ", "-"), i),
						Value:      &pb.Vertex_String_{String_: value},
						Expiration: timestamppb.New(time.Now().Add(time.Hour)),
					}
				}
				if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
					t.Fatalf("PutVertices: %v", err)
				}
			}

			resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: tc.query, Options: tc.options}))
			if resp != nil {
				t.Fatalf("response = %+v, want nil on budget exhaustion", resp)
			}
			if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
				t.Fatalf("code = %v, want ResourceExhausted (err=%v)", got, err)
			}
			detail := wireSearchErrorDetail(t, err)
			if detail.GetReason() != pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED || detail.GetWorkKind() != tc.workKind {
				t.Fatalf("detail = %+v, want work-budget/%s", detail, tc.workKind)
			}
		})
	}
}

func TestSearchVertices_ServerTimeoutOverWire(t *testing.T) {
	c := newSearchRawClientWithLimits(t, service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: true,
		DefaultLimit:     10,
		MaxLimit:         100,
		Timeout:          time.Nanosecond,
	})
	_, err := c.SearchVertices(context.Background(), connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha"}))
	if got := connect.CodeOf(err); got != connect.CodeDeadlineExceeded {
		t.Fatalf("code = %v, want DeadlineExceeded (err=%v)", got, err)
	}

	statusClient := newSearchRawClientWithLimits(t, service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: true,
		DefaultLimit:     10,
		MaxLimit:         100,
		Timeout:          1500 * time.Millisecond,
		MaxQueryBytes:    123,
		WorkBudget: search.Budget{
			MaxQueryTerms:       12,
			MaxDictionaryVisits: 34,
			MaxPostingVisits:    56,
			MaxPositionVisits:   78,
			MaxExpirationVisits: 90,
		},
		MaxInFlight: 9,
	})
	status, err := statusClient.GetServerStatus(context.Background(), connect.NewRequest(&pb.GetServerStatusRequest{}))
	if err != nil {
		t.Fatalf("GetServerStatus: %v", err)
	}
	got := status.Msg.GetSearch()
	if got.GetTimeoutMs() != 1500 || got.GetMaxQueryBytes() != 123 || got.GetMaxQueryTerms() != 12 ||
		got.GetMaxDictionaryVisits() != 34 || got.GetMaxPostingVisits() != 56 ||
		got.GetMaxPositionVisits() != 78 || got.GetMaxExpirationVisits() != 90 || got.GetMaxInFlight() != 9 {
		t.Fatalf("execution capabilities = %+v, want configured limits", got)
	}
}

func TestSearchVertices_ExpirationOverWire(t *testing.T) {
	t.Run("ranking and expansion use only the live corpus", func(t *testing.T) {
		withExpired := newSearchRawClient(t, true)
		baseline := newSearchRawClient(t, true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		liveExpiration := timestamppb.New(time.Now().Add(time.Hour))
		expiration := time.Now().Add(100 * time.Millisecond)
		for _, c := range []graphv1connect.LanternServiceClient{withExpired, baseline} {
			_, err := c.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
				Key: "live", Value: &pb.Vertex_String_{String_: "alpha beta"}, Expiration: liveExpiration,
			}}))
			if err != nil {
				t.Fatalf("PutVertex live: %v", err)
			}
		}
		_, err := withExpired.PutVertex(ctx, connect.NewRequest(&pb.PutVertexRequest{Vertex: &pb.Vertex{
			Key: "expired", Value: &pb.Vertex_String_{String_: "alpha alpha zzuniqueonly"}, Expiration: timestamppb.New(expiration),
		}}))
		if err != nil {
			t.Fatalf("PutVertex expired: %v", err)
		}
		time.Sleep(time.Until(expiration) + 20*time.Millisecond)

		searchRequest := connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha"})
		got, err := withExpired.SearchVertices(ctx, searchRequest)
		if err != nil {
			t.Fatalf("SearchVertices with expired: %v", err)
		}
		want, err := baseline.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha"}))
		if err != nil {
			t.Fatalf("SearchVertices baseline: %v", err)
		}
		if len(got.Msg.GetHits()) != 1 || len(want.Msg.GetHits()) != 1 || got.Msg.GetHits()[0].GetKey() != "live" || math.Float64bits(got.Msg.GetHits()[0].GetScore()) != math.Float64bits(want.Msg.GetHits()[0].GetScore()) {
			t.Fatalf("live-corpus ranking = %+v, baseline = %+v", got.Msg.GetHits(), want.Msg.GetHits())
		}
		expanded, err := withExpired.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
			Query: "zzuniq", Options: &pb.SearchOptions{PrefixTerms: true},
		}))
		if err != nil {
			t.Fatalf("SearchVertices expired-only prefix: %v", err)
		}
		if len(expanded.Msg.GetHits()) != 0 {
			t.Fatalf("expired-only expansion hits = %+v", expanded.Msg.GetHits())
		}
		status, err := withExpired.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
		if err != nil {
			t.Fatalf("GetServerStatus: %v", err)
		}
		stats := status.Msg.GetSearch().GetIndexStats()
		if stats.GetDocuments() != 1 || stats.GetPhysicalDocuments() != 1 || stats.GetExpiredDocuments() != 0 || stats.GetExpirationQueueEntries() != 1 || stats.GetExpirationPurged() != 1 || stats.GetLastExpirationPurgeDuration() == nil {
			t.Fatalf("post-purge index stats = %+v", stats)
		}
	})

	t.Run("expiration work is bounded and retryable", func(t *testing.T) {
		c := newSearchRawClientWithLimits(t, service.SearchLimits{
			Enabled: true, PositionsEnabled: true, DefaultLimit: 10, MaxLimit: 100,
			WorkBudget: search.Budget{MaxExpirationVisits: 1, MaxPostingVisits: 100},
		})
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		expiration := time.Now().Add(100 * time.Millisecond)
		vertices := []*pb.Vertex{
			{Key: "dead-a", Value: &pb.Vertex_String_{String_: "alpha"}, Expiration: timestamppb.New(expiration)},
			{Key: "dead-b", Value: &pb.Vertex_String_{String_: "alpha"}, Expiration: timestamppb.New(expiration)},
		}
		if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}
		time.Sleep(time.Until(expiration) + 20*time.Millisecond)
		_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha"}))
		if got := connect.CodeOf(err); got != connect.CodeResourceExhausted {
			t.Fatalf("first purge code = %v, want ResourceExhausted (err=%v)", got, err)
		}
		detail := wireSearchErrorDetail(t, err)
		if detail.GetReason() != pb.SearchErrorReason_SEARCH_WORK_BUDGET_EXHAUSTED || detail.GetWorkKind() != string(search.WorkExpirationVisits) {
			t.Fatalf("detail = %+v, want expiration budget exhaustion", detail)
		}
		second, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "alpha"}))
		if err != nil {
			t.Fatalf("retry after bounded progress: %v", err)
		}
		if len(second.Msg.GetHits()) != 0 {
			t.Fatalf("retry hits = %+v, want none", second.Msg.GetHits())
		}
	})
}

func TestSearchVertices_CancellationAndAdmissionOverWire(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	cache.EnableSearchIndex(searchVertexDocument, strings.Compare)
	expiration := time.Now().Add(time.Hour)
	items := make([]graphcache.VertexItem[string, *pb.Vertex], 15_000)
	for i := range items {
		key := fmt.Sprintf("load/%05d", i)
		items[i] = graphcache.VertexItem[string, *pb.Vertex]{
			Key:        key,
			Value:      &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: fmt.Sprintf("candidate%05d", i)}},
			Expiration: expiration,
		}
	}
	if err := cache.PutVerticesWithExpiration(items); err != nil {
		t.Fatalf("PutVerticesWithExpiration: %v", err)
	}

	metrics := &wireSearchMetrics{executions: make(chan wireSearchExecution, 32)}
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: true,
		DefaultLimit:     10,
		MaxLimit:         100,
		Timeout:          5 * time.Second,
		MaxInFlight:      1,
	}).WithHotPathMetrics(metrics)
	srv := newConnectTestServer(t, svc, nil)
	c := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	terms := make([]string, 12)
	for i := range terms {
		terms[i] = fmt.Sprintf("candidatx%05d", i)
	}
	heavy := &pb.SearchVerticesRequest{
		Query:   strings.Join(terms, " "),
		Options: &pb.SearchOptions{Fuzziness: 1},
	}
	heavyCtx, cancelHeavy := context.WithCancel(context.Background())
	heavyDone := make(chan error, 1)
	go func() {
		_, err := c.SearchVertices(heavyCtx, connect.NewRequest(heavy))
		heavyDone <- err
	}()
	time.Sleep(10 * time.Millisecond)

	// An admission rejection proves the heavy call has crossed the real h2c
	// boundary and acquired the only execution slot. Cancel only after that
	// signal, so this exercises cancellation after backend entry.
	var admissionErr error
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		_, err := c.SearchVertices(context.Background(), connect.NewRequest(&pb.SearchVerticesRequest{Query: "probe"}))
		if connect.CodeOf(err) == connect.CodeResourceExhausted {
			admissionErr = err
			break
		}
		select {
		case err := <-heavyDone:
			t.Fatalf("heavy search finished before admission was observable: %v", err)
		default:
		}
	}
	if admissionErr == nil {
		t.Fatal("did not observe admission saturation while heavy search was active")
	}
	admissionDetail := wireSearchErrorDetail(t, admissionErr)
	if admissionDetail.GetReason() != pb.SearchErrorReason_SEARCH_ADMISSION_SATURATED {
		t.Fatalf("admission detail = %+v, want SEARCH_ADMISSION_SATURATED", admissionDetail)
	}

	cancelHeavy()
	if err := <-heavyDone; connect.CodeOf(err) != connect.CodeCanceled {
		t.Fatalf("heavy search error = %v, want Canceled", err)
	}

	metricDeadline := time.NewTimer(2 * time.Second)
	defer metricDeadline.Stop()
	for {
		select {
		case observation := <-metrics.executions:
			if observation.outcome != "canceled" {
				continue
			}
			if observation.reason != "canceled" || observation.stats.DictionaryVisits == 0 {
				t.Fatalf("canceled observation = %+v, want dictionary work before cancellation", observation)
			}
			return
		case <-metricDeadline.C:
			t.Fatal("server did not publish the canceled execution observation")
		}
	}
}

// newSearchSDKClient mirrors newSearchRawClient but returns the high-level
// *client.Lantern so the SDK SearchVertices forwarder (#625) is exercised
// against the real service handler — the SDK package itself keeps only
// white-box request/response tests.
func newSearchSDKClient(t *testing.T, enabled bool) *client.Lantern {
	t.Helper()
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	if enabled {
		cache.EnableSearchIndex(searchVertexDocument, strings.Compare)
	}
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:          enabled,
		PositionsEnabled: enabled,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})
	srv := newConnectTestServer(t, svc, nil)
	return newConnectClientFor(t, srv.url)
}

func TestSearchVertices_EndToEnd(t *testing.T) {
	c := newSearchRawClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "user.preferences.tone", Value: &pb.Vertex_String_{String_: "calm and concise"}, Expiration: exp},
		{Key: "user.preferences.format", Value: &pb.Vertex_String_{String_: "calm bullet points"}, Expiration: exp},
		{Key: "project.lantern.stack", Value: &pb.Vertex_String_{String_: "go and react"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	// Content query matches the two "calm" preference vertices, ranked.
	resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "calm"}))
	if err != nil {
		t.Fatalf("SearchVertices: %v", err)
	}
	hits := resp.Msg.GetHits()
	if len(hits) != 2 {
		t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
	}
	for _, h := range hits {
		if !strings.HasPrefix(h.GetKey(), "user.preferences.") {
			t.Errorf("unexpected hit key %q", h.GetKey())
		}
		if h.GetScore() <= 0 {
			t.Errorf("hit %q score = %v, want > 0", h.GetKey(), h.GetScore())
		}
	}

	// Prefix scopes the same query to a single namespace branch.
	scoped, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
		Query:  "calm",
		Prefix: "user.preferences.tone",
	}))
	if err != nil {
		t.Fatalf("SearchVertices scoped: %v", err)
	}
	if got := scoped.Msg.GetHits(); len(got) != 1 || got[0].GetKey() != "user.preferences.tone" {
		t.Fatalf("scoped hits = %+v, want only user.preferences.tone", got)
	}

	// Empty query is a zero-hit success, not an error.
	empty, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: ""}))
	if err != nil {
		t.Fatalf("SearchVertices empty query: %v", err)
	}
	if got := empty.Msg.GetHits(); len(got) != 0 {
		t.Fatalf("empty-query hits = %d, want 0", len(got))
	}
}

// TestSearchVertices_PluralWriteVisibilityOverWire pins #1051's batch
// visibility contract over the real Connect/h2c path: a search concurrent with
// PutVertices/DeleteVertices observes the complete pre-batch or post-batch hit
// set, never the midpoint between the prepared index update and vertex commit.
func TestSearchVertices_PluralWriteVisibilityOverWire(t *testing.T) {
	const documents = 128
	c := newSearchRawClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	exp := timestamppb.New(time.Now().Add(time.Hour))
	vertices := make([]*pb.Vertex, documents)
	keys := make([]string, documents)
	for i := range vertices {
		key := fmt.Sprintf("wire-batch-%03d", i)
		keys[i] = key
		vertices[i] = &pb.Vertex{Key: key, Value: &pb.Vertex_String_{String_: "wireatomicterm"}, Expiration: exp}
	}
	put := func() error {
		_, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices}))
		return err
	}
	del := func() error {
		_, err := c.DeleteVertices(ctx, connect.NewRequest(&pb.DeleteVerticesRequest{Keys: keys}))
		return err
	}
	searchCount := func() (int, error) {
		resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "wireatomicterm", Limit: documents + 1}))
		if err != nil {
			return 0, err
		}
		return len(resp.Msg.GetHits()), nil
	}

	if err := put(); err != nil {
		t.Fatalf("initial PutVertices: %v", err)
	}
	if got, err := searchCount(); err != nil || got != documents {
		t.Fatalf("post-batch hits=%d err=%v, want %d", got, err, documents)
	}
	if err := del(); err != nil {
		t.Fatalf("initial DeleteVertices: %v", err)
	}

	var stop atomic.Bool
	var reads atomic.Int64
	var partial atomic.Int64
	errCh := make(chan error, 1)
	var reader sync.WaitGroup
	reader.Add(1)
	go func() {
		defer reader.Done()
		for !stop.Load() {
			got, err := searchCount()
			if err != nil {
				select {
				case errCh <- err:
				default:
				}
				return
			}
			reads.Add(1)
			if got != 0 && got != documents {
				partial.Add(1)
			}
		}
	}()
	for range 20 {
		if err := put(); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}
		if err := del(); err != nil {
			t.Fatalf("DeleteVertices: %v", err)
		}
	}
	stop.Store(true)
	reader.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatalf("SearchVertices: %v", err)
	}
	if reads.Load() == 0 {
		t.Fatal("wire reader never executed")
	}
	if got := partial.Load(); got != 0 {
		t.Fatalf("wire search observed %d partial plural-write snapshots", got)
	}
}

func TestSearchVertices_DisabledReturnsFailedPrecondition(t *testing.T) {
	c := newSearchRawClient(t, false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: "calm"}))
	if err == nil {
		t.Fatalf("expected FAILED_PRECONDITION when search disabled")
	}
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	if got := wireSearchErrorReason(t, err); got != pb.SearchErrorReason_SEARCH_DISABLED {
		t.Fatalf("reason = %v, want SEARCH_DISABLED", got)
	}
}

// TestSearchVertices_SDKForwarder drives the SDK's high-level SearchVertices
// against the live handler: ranked SearchHit mapping, WithSearchPrefix
// scoping, the empty-query zero-hit success, and the disabled-server path
// surfacing as the client.ErrFailedPrecondition sentinel (#625).
func TestSearchVertices_SDKForwarder(t *testing.T) {
	t.Run("ranked hits, prefix scope and empty query", func(t *testing.T) {
		l := newSearchSDKClient(t, true)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		seed := []client.VertexInput{
			{Key: "user.preferences.tone", Value: "calm and concise", Expiration: time.Now().Add(time.Hour)},
			{Key: "user.preferences.format", Value: "calm bullet points", Expiration: time.Now().Add(time.Hour)},
			{Key: "project.lantern.stack", Value: "go and react", Expiration: time.Now().Add(time.Hour)},
		}
		if err := l.PutVertices(ctx, seed); err != nil {
			t.Fatalf("PutVertices: %v", err)
		}

		hits, err := l.SearchVertices(ctx, "calm")
		if err != nil {
			t.Fatalf("SearchVertices: %v", err)
		}
		if len(hits) != 2 {
			t.Fatalf("hits = %d, want 2 (%+v)", len(hits), hits)
		}
		for _, h := range hits {
			if !strings.HasPrefix(h.Key, "user.preferences.") {
				t.Errorf("unexpected hit key %q", h.Key)
			}
			if h.Score <= 0 {
				t.Errorf("hit %q score = %v, want > 0", h.Key, h.Score)
			}
		}

		scoped, err := l.SearchVertices(ctx, "calm", client.WithSearchPrefix("user.preferences.tone"))
		if err != nil {
			t.Fatalf("SearchVertices scoped: %v", err)
		}
		if len(scoped) != 1 || scoped[0].Key != "user.preferences.tone" {
			t.Fatalf("scoped hits = %+v, want only user.preferences.tone", scoped)
		}

		empty, err := l.SearchVertices(ctx, "")
		if err != nil {
			t.Fatalf("empty query must be a zero-hit success: %v", err)
		}
		if len(empty) != 0 {
			t.Fatalf("empty-query hits = %d, want 0", len(empty))
		}
	})

	t.Run("disabled server surfaces ErrFailedPrecondition", func(t *testing.T) {
		l := newSearchSDKClient(t, false)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		hits, err := l.SearchVertices(ctx, "calm")
		if hits != nil {
			t.Errorf("want nil hits when disabled, got %+v", hits)
		}
		if !errors.Is(err, client.ErrFailedPrecondition) {
			t.Fatalf("want errors.Is(err, client.ErrFailedPrecondition), got %v", err)
		}
		if !errors.Is(err, client.ErrSearchDisabled) {
			t.Fatalf("want errors.Is(err, client.ErrSearchDisabled), got %v", err)
		}
		if got := client.SearchFailureReason(err); got != client.SearchErrorReasonDisabled {
			t.Fatalf("SearchFailureReason = %v, want SEARCH_DISABLED", got)
		}
	})
}

func TestSearchVertices_SDKExecutionError(t *testing.T) {
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	cache.EnableSearchIndex(searchVertexDocument, strings.Compare)
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: true,
		DefaultLimit:     10,
		MaxLimit:         100,
		MaxQueryBytes:    4,
	})
	srv := newConnectTestServer(t, svc, nil)
	l := newConnectClientFor(t, srv.url)

	hits, err := l.SearchVertices(context.Background(), "12345")
	if hits != nil {
		t.Fatalf("hits = %+v, want nil", hits)
	}
	if !errors.Is(err, client.ErrResourceExhausted) || !errors.Is(err, client.ErrSearchWorkBudget) {
		t.Fatalf("error = %v, want resource + search-budget sentinels", err)
	}
	if got := client.SearchFailureReason(err); got != client.SearchErrorReasonWorkBudget {
		t.Fatalf("SearchFailureReason = %v, want work budget", got)
	}
	if got := client.SearchFailureWorkKind(err); got != "query_bytes" {
		t.Fatalf("SearchFailureWorkKind = %q, want query_bytes", got)
	}
}

// TestSearchVertices_IncrementalLatestInputWins drives the Go SDK's
// search-as-you-type orchestration over the real Connect/h2c path. The final
// query in a burst is the only result exposed, and an empty input invalidates
// pending debounce work immediately without a later stale delivery (#1052).
func TestSearchVertices_IncrementalLatestInputWins(t *testing.T) {
	l := newSearchSDKClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := l.PutVertices(ctx, []client.VertexInput{
		{Key: "doc.alpha", Value: "alpha", Expiration: time.Now().Add(time.Hour)},
		{Key: "doc.beta", Value: "beta", Expiration: time.Now().Add(time.Hour)},
	}); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	const debounce = 30 * time.Millisecond
	incremental := l.NewIncrementalSearch(ctx, client.WithDebounce(debounce))
	defer func() {
		if err := incremental.Close(); err != nil {
			t.Errorf("Close incremental search: %v", err)
		}
	}()

	incremental.Search("alpha")
	incremental.Search("beta")
	select {
	case update := <-incremental.Updates():
		if update.Err != nil {
			t.Fatalf("incremental beta: %v", update.Err)
		}
		if update.Query != "beta" || len(update.Hits) != 1 || update.Hits[0].Key != "doc.beta" {
			t.Fatalf("update = %+v, want beta/doc.beta", update)
		}
	case <-ctx.Done():
		t.Fatal("timed out waiting for incremental beta result")
	}

	incremental.Search("alpha")
	incremental.Search("")
	select {
	case update := <-incremental.Updates():
		if update.Query != "" || update.Err != nil || len(update.Hits) != 0 {
			t.Fatalf("empty reset = %+v, want synchronous empty update", update)
		}
	default:
		t.Fatal("empty input did not reset synchronously")
	}
	select {
	case update := <-incremental.Updates():
		t.Fatalf("pending alpha search delivered after empty input: %+v", update)
	case <-time.After(2 * debounce):
	}
}

// TestSearchVertices_Options drives the #892 SearchOptions end to end through
// the raw Connect handler: match mode, phrase adjacency, fuzzy/prefix term
// expansion, and minimum-should-match, each over a live index.
func TestSearchVertices_Options(t *testing.T) {
	c := newSearchRawClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "doc.both", Value: &pb.Vertex_String_{String_: "alpha beta gamma"}, Expiration: exp},
		{Key: "doc.alpha", Value: &pb.Vertex_String_{String_: "alpha delta"}, Expiration: exp},
		{Key: "doc.beta", Value: &pb.Vertex_String_{String_: "beta epsilon"}, Expiration: exp},
		{Key: "doc.phrase", Value: &pb.Vertex_String_{String_: "the alpha beta end"}, Expiration: exp},
		{Key: "doc.split", Value: &pb.Vertex_String_{String_: "beta stands well before the word alpha"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}
	keys := func(q string, o *pb.SearchOptions) map[string]bool {
		resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{Query: q, Options: o}))
		if err != nil {
			t.Fatalf("SearchVertices(%q): %v", q, err)
		}
		got := map[string]bool{}
		for _, h := range resp.Msg.GetHits() {
			got[h.GetKey()] = true
		}
		return got
	}

	t.Run("match_mode ALL requires every term", func(t *testing.T) {
		all := keys("alpha beta", &pb.SearchOptions{MatchMode: pb.MatchMode_MATCH_MODE_ALL})
		if all["doc.alpha"] || all["doc.beta"] {
			t.Errorf("MatchAll kept a single-term doc: %v", all)
		}
		for _, k := range []string{"doc.both", "doc.phrase", "doc.split"} {
			if !all[k] {
				t.Errorf("MatchAll dropped %q which has both terms: %v", k, all)
			}
		}
	})

	t.Run("phrase requires adjacency", func(t *testing.T) {
		got := keys("alpha beta", &pb.SearchOptions{Phrase: true})
		if !got["doc.both"] || !got["doc.phrase"] {
			t.Errorf("phrase dropped an adjacent doc: %v", got)
		}
		if got["doc.split"] {
			t.Errorf("phrase kept the scattered doc.split: %v", got)
		}
	})

	t.Run("fuzziness tolerates a typo", func(t *testing.T) {
		got := keys("alpga beta", &pb.SearchOptions{Fuzziness: 1}) // alpga is one edit from alpha
		if !got["doc.both"] {
			t.Errorf("fuzzy query did not reach doc.both: %v", got)
		}
	})

	t.Run("prefix_terms match an extending word", func(t *testing.T) {
		got := keys("gam", &pb.SearchOptions{PrefixTerms: true}) // gam -> gamma
		if !got["doc.both"] {
			t.Errorf("prefix_terms did not reach doc.both (gamma): %v", got)
		}
	})

	t.Run("min_should_match tunes coverage", func(t *testing.T) {
		msm := keys("alpha beta gamma", &pb.SearchOptions{
			MatchMode:      pb.MatchMode_MATCH_MODE_MIN_SHOULD,
			MinShouldMatch: 2,
		})
		if !msm["doc.both"] {
			t.Errorf("msm(2) dropped doc.both (has all three terms): %v", msm)
		}
		if msm["doc.alpha"] || msm["doc.beta"] {
			t.Errorf("msm(2) kept a single-term doc: %v", msm)
		}
	})
}

// TestSearchVertices_DeterministicAcrossHistories drives cap-overflow prefix
// and fuzzy expansion through independent real Connect/h2c servers whose final
// corpora are identical but whose document/term ordinal histories differ. The
// complete protobuf bytes (ordered keys and float bits) must agree on every
// repeated call.
func TestSearchVertices_DeterministicAcrossHistories(t *testing.T) {
	type document struct {
		key   string
		value string
	}
	var documents []document
	var prefixWant []string
	for i := 0; i < 80; i++ {
		key := fmt.Sprintf("docp-%03d", i)
		documents = append(documents, document{key: key, value: fmt.Sprintf("pr%03d", i)})
		if i < search.MaxTermExpansions {
			prefixWant = append(prefixWant, key)
		}
	}
	var fuzzyTerms []string
	for ch := byte('b'); ch <= 'z'; ch++ {
		fuzzyTerms = append(fuzzyTerms, string([]byte{ch, 'a'}), string([]byte{'a', ch}))
	}
	for digit := byte('0'); digit <= '9'; digit++ {
		fuzzyTerms = append(fuzzyTerms, string([]byte{'a', digit, 'a'}))
	}
	sort.Strings(fuzzyTerms)
	var fuzzyWant []string
	for i, term := range fuzzyTerms {
		key := fmt.Sprintf("docf-%03d", i)
		documents = append(documents, document{key: key, value: term})
		if i < search.MaxTermExpansions {
			fuzzyWant = append(fuzzyWant, key)
		}
	}

	seed := func(t *testing.T, c graphv1connect.LanternServiceClient, order []int, churn bool) {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		exp := timestamppb.New(time.Now().Add(time.Hour))
		if churn {
			var transient []*pb.Vertex
			for i := 0; i < len(documents); i += 3 {
				transient = append(transient, &pb.Vertex{
					Key:        documents[i].key,
					Value:      &pb.Vertex_String_{String_: fmt.Sprintf("temporary%03d", i)},
					Expiration: exp,
				})
			}
			if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: transient})); err != nil {
				t.Fatalf("PutVertices transient: %v", err)
			}
		}
		vertices := make([]*pb.Vertex, 0, len(order))
		for _, i := range order {
			vertices = append(vertices, &pb.Vertex{
				Key:        documents[i].key,
				Value:      &pb.Vertex_String_{String_: documents[i].value},
				Expiration: exp,
			})
		}
		if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: vertices})); err != nil {
			t.Fatalf("PutVertices final: %v", err)
		}
	}

	forward := make([]int, len(documents))
	for i := range forward {
		forward[i] = i
	}
	reverse := append([]int(nil), forward...)
	for i, j := 0, len(reverse)-1; i < j; i, j = i+1, j-1 {
		reverse[i], reverse[j] = reverse[j], reverse[i]
	}
	random := append([]int(nil), forward...)
	rand.New(rand.NewSource(1056)).Shuffle(len(random), func(i, j int) {
		random[i], random[j] = random[j], random[i]
	})
	histories := []struct {
		name  string
		order []int
		churn bool
	}{
		{"forward", forward, false},
		{"reverse", reverse, false},
		{"random", random, false},
		{"release-reuse", reverse, true},
	}
	tests := []struct {
		name    string
		query   string
		options *pb.SearchOptions
		want    []string
	}{
		{"prefix", "pr", &pb.SearchOptions{PrefixTerms: true}, prefixWant},
		{"fuzzy", "aa", &pb.SearchOptions{Fuzziness: 1}, fuzzyWant},
	}

	baseline := make(map[string][]byte, len(tests))
	for _, history := range histories {
		t.Run(history.name, func(t *testing.T) {
			c := newSearchRawClient(t, true)
			seed(t, c, history.order, history.churn)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			for _, tc := range tests {
				t.Run(tc.name, func(t *testing.T) {
					for repeat := 0; repeat < 100; repeat++ {
						resp, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
							Query:   tc.query,
							Limit:   100,
							Options: tc.options,
						}))
						if err != nil {
							t.Fatalf("repeat %d SearchVertices: %v", repeat, err)
						}
						gotKeys := make([]string, len(resp.Msg.GetHits()))
						for i, hit := range resp.Msg.GetHits() {
							gotKeys[i] = hit.GetKey()
						}
						if !slices.Equal(gotKeys, tc.want) {
							t.Fatalf("repeat %d keys = %v, want %v", repeat, gotKeys, tc.want)
						}
						wire, err := proto.MarshalOptions{Deterministic: true}.Marshal(resp.Msg)
						if err != nil {
							t.Fatalf("marshal response: %v", err)
						}
						if want, ok := baseline[tc.name]; !ok {
							baseline[tc.name] = append([]byte(nil), wire...)
						} else if !bytes.Equal(wire, want) {
							t.Fatalf("repeat %d response bytes differ across calls/histories", repeat)
						}
					}
				})
			}
		})
	}
}

// TestSearchVertices_PositionsOff verifies that the wire service fails closed:
// without positions it cannot prove adjacency, so it must never silently
// reinterpret phrase=true as an unordered AND query.
func TestSearchVertices_PositionsOff(t *testing.T) {
	// Build a search-enabled service whose cache index tracks no positions.
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	cache.EnablePrefixIndex(func(k string) string { return k })
	cache.EnableSearchIndex(searchVertexDocument, strings.Compare, graphcache.WithoutSearchPositions())
	svc := service.NewLanternService(cache).WithSearchLimits(service.SearchLimits{
		Enabled:          true,
		PositionsEnabled: false,
		DefaultLimit:     100,
		MaxLimit:         1000,
	})
	srv := newConnectTestServer(t, svc, nil)
	c := graphv1connect.NewLanternServiceClient(h2cClient(), srv.url)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	exp := timestamppb.New(time.Now().Add(time.Hour))
	seed := []*pb.Vertex{
		{Key: "doc.phrase", Value: &pb.Vertex_String_{String_: "the alpha beta end"}, Expiration: exp},
		{Key: "doc.split", Value: &pb.Vertex_String_{String_: "beta stands well before the word alpha"}, Expiration: exp},
	}
	if _, err := c.PutVertices(ctx, connect.NewRequest(&pb.PutVerticesRequest{Vertices: seed})); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	_, err := c.SearchVertices(ctx, connect.NewRequest(&pb.SearchVerticesRequest{
		Query:   "alpha beta",
		Options: &pb.SearchOptions{Phrase: true},
	}))
	if got := connect.CodeOf(err); got != connect.CodeFailedPrecondition {
		t.Fatalf("code = %v, want FailedPrecondition", got)
	}
	if got := wireSearchErrorReason(t, err); got != pb.SearchErrorReason_SEARCH_POSITIONS_DISABLED {
		t.Fatalf("reason = %v, want SEARCH_POSITIONS_DISABLED", got)
	}
	status, statusErr := c.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if statusErr != nil {
		t.Fatalf("GetServerStatus: %v", statusErr)
	}
	capabilities := status.Msg.GetSearch()
	if !capabilities.GetEnabled() || capabilities.GetPositionsEnabled() {
		t.Fatalf("search capabilities = %+v, want enabled with positions off", capabilities)
	}
	if capabilities.GetConfigFingerprint() == "" {
		t.Fatal("search config fingerprint is empty")
	}
	withPositions := newSearchRawClient(t, true)
	peerStatus, peerErr := withPositions.GetServerStatus(ctx, connect.NewRequest(&pb.GetServerStatusRequest{}))
	if peerErr != nil {
		t.Fatalf("GetServerStatus positions-enabled peer: %v", peerErr)
	}
	if peerStatus.Msg.GetSearch().GetConfigFingerprint() == capabilities.GetConfigFingerprint() {
		t.Fatal("mixed search configuration is invisible: peer fingerprints match")
	}
}

// TestSearchVertices_SDKOptions drives the #892 options through the high-level
// SDK: WithMatchMode(MatchAll) narrows the OR-union to the document holding
// every term.
func TestSearchVertices_SDKOptions(t *testing.T) {
	l := newSearchSDKClient(t, true)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	seed := []client.VertexInput{
		{Key: "doc.both", Value: "alpha beta gamma", Expiration: time.Now().Add(time.Hour)},
		{Key: "doc.alpha", Value: "alpha delta", Expiration: time.Now().Add(time.Hour)},
		{Key: "doc.beta", Value: "beta epsilon", Expiration: time.Now().Add(time.Hour)},
	}
	if err := l.PutVertices(ctx, seed); err != nil {
		t.Fatalf("PutVertices: %v", err)
	}

	any, err := l.SearchVertices(ctx, "alpha beta")
	if err != nil {
		t.Fatalf("SearchVertices any: %v", err)
	}
	all, err := l.SearchVertices(ctx, "alpha beta", client.WithMatchMode(client.MatchAll))
	if err != nil {
		t.Fatalf("SearchVertices all: %v", err)
	}
	if len(all) >= len(any) {
		t.Errorf("WithMatchMode(MatchAll) should narrow: any=%d all=%d", len(any), len(all))
	}
	got := map[string]bool{}
	for _, h := range all {
		got[h.Key] = true
	}
	if !got["doc.both"] || got["doc.alpha"] || got["doc.beta"] {
		t.Errorf("MatchAll hits = %v, want only doc.both", got)
	}
}
