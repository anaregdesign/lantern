package integration_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/anaregdesign/lantern/core/graphcache"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
)

// TestLantern_Illuminate_MetricReductionLabel is the #963 external-surface
// guard: the Illuminate hot-path metrics must expose the traversal FAMILY
// (algorithm ∈ bfs|ppr|community) and the post-traversal REDUCTION
// (reduction ∈ none|mst|spt) as two INDEPENDENT Prometheus labels, so a
// community walk's reduction is observable in its own right instead of being
// collapsed into the family name (the pre-#963 bug, where the community arm
// discarded comm.GetReduction() and always reported algorithm="community").
//
// It drives real Illuminate RPCs over the h2c Connect wire into a service
// wired to a real metrics.DomainMetrics, then scrapes the backing registry
// through a promhttp handler — the same text-exposition surface Prometheus
// scrapes in production.
func TestLantern_Illuminate_MetricReductionLabel(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg, metrics.Options{SampleInterval: time.Hour})

	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithHotPathMetrics(m)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	srv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	l := newConnectClientFor(t, srv.url)

	// Scrape endpoint over the real text-exposition surface.
	scrape := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	t.Cleanup(scrape.Close)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	t.Cleanup(cancel)

	// A small connected weighted graph so every family has something to walk
	// and MST/SPT have edges to reduce.
	const ttl = time.Hour
	for _, k := range []string{"a", "b", "c", "d"} {
		if err := l.PutVertex(ctx, k, k, ttl); err != nil {
			t.Fatalf("PutVertex %q: %v", k, err)
		}
	}
	edges := []struct {
		tail, head string
		weight     float32
	}{
		{"a", "b", 1}, {"a", "c", 2}, {"b", "c", 1}, {"b", "d", 5}, {"c", "d", 3},
	}
	for _, e := range edges {
		if _, err := l.AddEdge(ctx, e.tail, e.head, e.weight, ttl); err != nil {
			t.Fatalf("AddEdge %s→%s: %v", e.tail, e.head, err)
		}
	}

	// The crux: a community walk WITH an MST reduction. Pre-#963 this landed
	// on algorithm="community" with the reduction silently dropped.
	if _, err := l.Illuminate(ctx, "a", client.WithLocalCommunity(client.LocalCommunityOpts{
		MaxSize:   4,
		Reduction: client.ReductionMinimumSpanningTree,
		Objective: client.ObjectiveMinimize,
	})); err != nil {
		t.Fatalf("Illuminate community+mst: %v", err)
	}
	// A BFS walk with an SPT reduction — the family label must be "bfs", not
	// the reduction (the pre-#961 conflation, already fixed but guarded here).
	if _, err := l.Illuminate(ctx, "a", client.WithBFS(client.BFSOpts{
		Step:      3,
		FanOut:    8,
		Reduction: client.ReductionShortestPathTree,
		Objective: client.ObjectiveMinimize,
	})); err != nil {
		t.Fatalf("Illuminate bfs+spt: %v", err)
	}
	// PPR is a distinct family that carries no reduction: reduction="none".
	if _, err := l.Illuminate(ctx, "a", client.WithPPR(client.PPROpts{TopN: 4})); err != nil {
		t.Fatalf("Illuminate ppr: %v", err)
	}

	body := scrapeMetrics(t, scrape.URL)
	const vertsCount = "lantern_illuminate_visited_vertices_count"

	// Community+MST is recorded on its OWN family/reduction pair.
	if got, ok := metricSeriesValue(t, body, vertsCount, map[string]string{
		"algorithm": "community", "reduction": "mst", "objective": "minimize", "weighting": "raw",
	}); !ok || got < 1 {
		t.Errorf("%s{algorithm=community,reduction=mst,objective=minimize,weighting=raw} = %v (present=%v), want ≥1", vertsCount, got, ok)
	}
	// The reduction label DISCRIMINATES: a community walk we never ran with an
	// SPT (or no) reduction must stay at zero. This is exactly what the pre-#963
	// collapse made impossible to observe.
	for _, red := range []string{"spt", "none"} {
		if got, ok := metricSeriesValue(t, body, vertsCount, map[string]string{
			"algorithm": "community", "reduction": red, "objective": "minimize", "weighting": "raw",
		}); ok && got != 0 {
			t.Errorf("%s{algorithm=community,reduction=%s,...} = %v, want 0 (only community+mst was run)", vertsCount, red, got)
		}
	}
	// BFS keeps its family label independent of the reduction.
	if got, ok := metricSeriesValue(t, body, vertsCount, map[string]string{
		"algorithm": "bfs", "reduction": "spt", "objective": "minimize", "weighting": "raw",
	}); !ok || got < 1 {
		t.Errorf("%s{algorithm=bfs,reduction=spt,objective=minimize,weighting=raw} = %v (present=%v), want ≥1", vertsCount, got, ok)
	}
	// PPR reports reduction="none".
	if got, ok := metricSeriesValue(t, body, vertsCount, map[string]string{
		"algorithm": "ppr", "reduction": "none", "objective": "maximize", "weighting": "raw",
	}); !ok || got < 1 {
		t.Errorf("%s{algorithm=ppr,reduction=none,objective=maximize,weighting=raw} = %v (present=%v), want ≥1", vertsCount, got, ok)
	}
}

// scrapeMetrics GETs the Prometheus text exposition from url and returns the body.
func scrapeMetrics(t *testing.T, url string) string {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("scrape GET: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("scrape read: %v", err)
	}
	return string(b)
}

// metricSeriesValue parses the value of the single text-exposition series named
// `name` whose label set contains every pair in `labels`. Label order in the
// exposition is alphabetical; matching on `key="value"` substrings within the
// one line is order-independent, and the fully-specified label map plus the
// exact metric-name token make the match unique (a `_count` line never also
// carries a differing value for the same label). Returns (value, found).
func metricSeriesValue(t *testing.T, body, name string, labels map[string]string) (float64, bool) {
	t.Helper()
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, name+"{") {
			continue
		}
		open := strings.IndexByte(line, '{')
		closeIdx := strings.LastIndexByte(line, '}')
		if open < 0 || closeIdx < 0 || closeIdx < open {
			continue
		}
		labelStr := line[open+1 : closeIdx]
		match := true
		for k, v := range labels {
			if !strings.Contains(labelStr, k+`="`+v+`"`) {
				match = false
				break
			}
		}
		if !match {
			continue
		}
		valStr := strings.TrimSpace(line[closeIdx+1:])
		f, err := strconv.ParseFloat(valStr, 64)
		if err != nil {
			t.Fatalf("parse metric value from %q: %v", line, err)
		}
		return f, true
	}
	return 0, false
}
