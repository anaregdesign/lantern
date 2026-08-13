package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/pb/graph/v1/graphv1connect"
	client "github.com/anaregdesign/lantern/sdks/go"
)

type fakeSearcher struct {
	responses [][]client.SearchHit
	pages     []client.SearchPage
	call      int
	pageCall  int
}

func (f *fakeSearcher) SearchVertices(context.Context, string, ...client.SearchOption) ([]client.SearchHit, error) {
	if f.call >= len(f.responses) {
		return []client.SearchHit{}, nil
	}
	response := f.responses[f.call]
	f.call++
	return response, nil
}

func (f *fakeSearcher) SearchVerticesPage(context.Context, string, ...client.SearchOption) (client.SearchPage, error) {
	if f.pageCall >= len(f.pages) {
		return client.SearchPage{}, nil
	}
	page := f.pages[f.pageCall]
	f.pageCall++
	return page, nil
}

func (f *fakeSearcher) Close() error { return nil }

func successfulPaginationPages() []client.SearchPage {
	pages := make([]client.SearchPage, 0, 5)
	for start := 0; start < 9; start += 2 {
		end := min(start+2, 9)
		hits := make([]client.SearchHit, 0, end-start)
		for i := start; i < end; i++ {
			hits = append(hits, client.SearchHit{
				Key:              fmt.Sprintf("bench:search:page:%02d", i),
				Vertex:           mustStringVertex(fmt.Sprintf("bench:search:page:%02d", i), "deeppaginationbeacon"),
				ProjectionStatus: client.SearchHitProjectionSnapshot,
			})
		}
		var cursor []byte
		if end < 9 {
			cursor = []byte{byte(end)}
		}
		pages = append(pages, client.SearchPage{
			Hits:           hits,
			NextCursor:     cursor,
			EffectiveLimit: 2,
			Truncated:      end < 9,
		})
	}
	return pages
}

func mustStringVertex(key, value string) *client.Vertex {
	vertex, err := client.UnmarshalVertexJSON([]byte(fmt.Sprintf(`{"key":%q,"type":"string","value":%q}`, key, value)))
	if err != nil {
		panic(err)
	}
	return vertex
}

type fakeWriter struct {
	inputs     []client.VertexInput
	deletedKey string
	putOutcome client.PutOutcome
}

func (f *fakeWriter) PutVertices(_ context.Context, inputs []client.VertexInput) ([]client.VertexPutResult, error) {
	f.inputs = append([]client.VertexInput(nil), inputs...)
	results := make([]client.VertexPutResult, len(inputs))
	outcome := f.putOutcome
	if outcome == 0 {
		outcome = client.PutOutcomeAppliedAndLive
	}
	for i, input := range inputs {
		results[i] = client.VertexPutResult{Key: input.Key, Outcome: outcome}
	}
	return results, nil
}

func TestSeedRejectsNonLivePutOutcome(t *testing.T) {
	f := &fakeWriter{putOutcome: client.PutOutcomeExpired}
	if err := seed(context.Background(), f, time.Now()); err == nil {
		t.Fatal("seed accepted expired Put outcome")
	}
}

func (f *fakeWriter) DeleteVertex(_ context.Context, key string) (bool, error) {
	f.deletedKey = key
	return true, nil
}

func TestVerifyOnce(t *testing.T) {
	t.Run("expected semantic matrix passes", func(t *testing.T) {
		checks := searchChecks()
		responses := make([][]client.SearchHit, 0, len(checks))
		for _, c := range checks {
			keys := append(append(append([]string(nil), c.Want...), c.Top...), c.Contains...)
			hits := make([]client.SearchHit, len(keys))
			for i, key := range keys {
				hits[i].Key = key
			}
			responses = append(responses, hits)
		}
		if err := verifyOnce(context.Background(), &fakeSearcher{responses: responses, pages: successfulPaginationPages()}); err != nil {
			t.Fatal(err)
		}
	})

	t.Run("HTTP OK with empty hits fails closed", func(t *testing.T) {
		err := verifyOnce(context.Background(), &fakeSearcher{})
		if err == nil || !strings.Contains(err.Error(), "live_sentinel") {
			t.Fatalf("error = %v", err)
		}
	})
}

type paginationCapture struct {
	graphv1connect.UnimplementedLanternServiceHandler
	requests []*pb.SearchVerticesRequest
}

func (c *paginationCapture) SearchVertices(_ context.Context, req *connect.Request[pb.SearchVerticesRequest]) (*connect.Response[pb.SearchVerticesResponse], error) {
	c.requests = append(c.requests, req.Msg)
	start := 0
	if cursor := req.Msg.GetCursor(); len(cursor) > 0 {
		start = int(cursor[0])
	}
	end := min(start+2, 9)
	hits := make([]*pb.SearchHit, 0, end-start)
	for i := start; i < end; i++ {
		key := fmt.Sprintf("%s%02d", pagePrefix, i)
		hits = append(hits, &pb.SearchHit{
			Key: key,
			Vertex: &pb.Vertex{
				Key:   key,
				Value: &pb.Vertex_String_{String_: "deeppaginationbeacon"},
			},
			ProjectionStatus: pb.SearchHitProjectionStatus_SEARCH_HIT_PROJECTION_STATUS_SNAPSHOT,
		})
	}
	var next []byte
	if end < 9 {
		next = []byte{byte(end)}
	}
	return connect.NewResponse(&pb.SearchVerticesResponse{
		Hits:           hits,
		NextCursor:     next,
		EffectiveLimit: 2,
		Truncated:      end < 9,
	}), nil
}

func TestVerifyDeepPaginationScopesEveryPage(t *testing.T) {
	capture := &paginationCapture{}
	path, handler := graphv1connect.NewLanternServiceHandler(capture)
	mux := http.NewServeMux()
	mux.Handle(path, handler)
	server := httptest.NewUnstartedServer(mux)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	server.Config.Protocols = protocols
	server.Start()
	defer server.Close()

	lantern, err := client.NewLantern(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lantern.Close() }()
	if err := verifyDeepPagination(context.Background(), lantern); err != nil {
		t.Fatal(err)
	}
	if len(capture.requests) != 5 {
		t.Fatalf("requests = %d, want 5", len(capture.requests))
	}
	for i, request := range capture.requests {
		if request.GetPrefix() != pagePrefix {
			t.Errorf("request %d prefix = %q, want %q", i+1, request.GetPrefix(), pagePrefix)
		}
	}
}

type blockingSearchClient struct {
	started chan<- struct{}
}

func (b *blockingSearchClient) SearchVertices(ctx context.Context, _ string, _ ...client.SearchOption) ([]client.SearchHit, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	b.started <- struct{}{}
	<-ctx.Done()
	return nil, ctx.Err()
}

func (b *blockingSearchClient) SearchVerticesPage(context.Context, string, ...client.SearchOption) (client.SearchPage, error) {
	return client.SearchPage{}, nil
}

func (b *blockingSearchClient) Close() error { return nil }

func TestVerifyReplicasRunsConcurrentlyAndReportsCheckCategory(t *testing.T) {
	started := make(chan struct{}, 3)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	report := verifyReplicas(ctx, "pre", []string{"a", "b", "c"}, func(string) (searchClient, error) {
		return &blockingSearchClient{started: started}, nil
	})
	if len(started) != 3 {
		t.Fatalf("started replicas = %d, want 3", len(started))
	}
	if report.Verdict != "fail" || len(report.Replicas) != 3 {
		t.Fatalf("report = %+v", report)
	}
	for _, replica := range report.Replicas {
		if replica.Verdict != "fail" || replica.Failure != "live_sentinel" {
			t.Errorf("replica = %+v", replica)
		}
	}
}

func TestWaitForChecksPreservesSemanticFailureAtDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Millisecond)
	defer cancel()
	err := waitForChecks(ctx, &fakeSearcher{})
	if err == nil || !strings.Contains(err.Error(), "live_sentinel") {
		t.Fatalf("error = %v", err)
	}
}

func TestFailureReportIsBounded(t *testing.T) {
	path := filepath.Join(t.TempDir(), "semantic_pre.json")
	writeReport(path, probeReport{
		Phase:   "pre",
		Verdict: "fail",
		Replicas: []replicaReport{{
			Endpoint: "http://localhost:6380",
			Checks:   searchCheckCount(),
			Verdict:  "fail",
			Failure:  "deep_pagination",
		}},
	})
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{liveKey, "persistentbeacon", "quick brown"} {
		if strings.Contains(string(raw), forbidden) {
			t.Errorf("bounded report contains fixture data %q: %s", forbidden, raw)
		}
	}
	if !strings.Contains(string(raw), `"failure": "deep_pagination"`) {
		t.Fatalf("bounded report omitted failure category: %s", raw)
	}
}

func TestSeedWritesLifecycleSentinels(t *testing.T) {
	now := time.Unix(1000, 0)
	w := &fakeWriter{}
	if err := seed(context.Background(), w, now); err != nil {
		t.Fatal(err)
	}
	if len(w.inputs) != 15 || w.deletedKey != deletedKey {
		t.Fatalf("inputs = %d, deleted = %q", len(w.inputs), w.deletedKey)
	}
	var expiration time.Time
	for _, input := range w.inputs {
		if input.Key == expiredKey {
			expiration = input.Expiration
		}
	}
	if want := now.Add(10 * time.Second); !expiration.Equal(want) {
		t.Fatalf("expiration = %v, want %v", expiration, want)
	}
}
