package metrics

import (
	"context"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/core/concurrent/pubsub"
	"github.com/anaregdesign/lantern/core/search"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	dto "github.com/prometheus/client_model/go"
)

func TestDomainMetrics_ExposesLanternFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v0.7.0", Commit: "deadbeef", SampleInterval: time.Hour})
	m.BindSampler(func() (int, int) { return 12, 34 })

	// Drive one tick directly so gauges have values without spinning up Run.
	m.tick()
	m.OnExpire("vertex", 2)
	m.OnExpire("edge", 5)
	m.OnExpire("dangling_edge", 1)
	m.OnGCDuration(7 * time.Millisecond)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_vertices",
		"lantern_edges",
		"lantern_ttl_expirations_total",
		"lantern_gc_duration_seconds",
		"lantern_build_info",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	if got := testutil.ToFloat64(m.vertices); got != 12 {
		t.Errorf("lantern_vertices = %v, want 12", got)
	}
	if got := testutil.ToFloat64(m.edges); got != 34 {
		t.Errorf("lantern_edges = %v, want 34", got)
	}
	if got := testutil.ToFloat64(m.expirations.WithLabelValues("vertex")); got != 2 {
		t.Errorf("expirations[vertex] = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.expirations.WithLabelValues("edge")); got != 5 {
		t.Errorf("expirations[edge] = %v, want 5", got)
	}
	if got := testutil.ToFloat64(m.expirations.WithLabelValues("dangling_edge")); got != 1 {
		t.Errorf("expirations[dangling_edge] = %v, want 1", got)
	}

	// build_info should be exactly 1 with the labels we passed.
	if got := testutil.ToFloat64(m.buildInfo.WithLabelValues("v0.7.0", "deadbeef", runtime.Version())); got != 1 {
		t.Errorf("lantern_build_info gauge = %v, want 1", got)
	}
}

// TestDomainMetrics_GetHitMissCounters asserts the #539 recall hit/miss
// counters register, scrape as 0 from process start (plain counters need no
// pre-warm), and accumulate across calls via the OnGetVertices / OnGetEdges
// hooks.
func TestDomainMetrics_GetHitMissCounters(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v", Commit: "c", SampleInterval: time.Hour})

	// Registered and scrape as 0 before any traffic.
	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_get_vertex_hits_total",
		"lantern_get_vertex_misses_total",
		"lantern_get_edge_hits_total",
		"lantern_get_edge_misses_total",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered (plain counters should scrape as 0 from start)", want)
		}
	}

	// Accumulate across two batches; zero-valued sides are skipped but the
	// counters still total correctly.
	m.OnGetVertices(2, 1)
	m.OnGetVertices(3, 0)
	m.OnGetEdges(0, 4)
	m.OnGetEdges(1, 1)

	if got := testutil.ToFloat64(m.getVertexHits); got != 5 {
		t.Errorf("get_vertex_hits_total = %v, want 5", got)
	}
	if got := testutil.ToFloat64(m.getVertexMisses); got != 1 {
		t.Errorf("get_vertex_misses_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.getEdgeHits); got != 1 {
		t.Errorf("get_edge_hits_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.getEdgeMisses); got != 5 {
		t.Errorf("get_edge_misses_total = %v, want 5", got)
	}
}

func TestDomainMetrics_Run_NoSampler_StopsOnContextCancel(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// TestDomainMetrics_ReplicationFamilies asserts the #187 replication and
// anti-entropy collectors register with the expected names and that the
// adapter methods used by the pump (Metrics) and anti-entropy driver
// (AntiEntropyMetrics) interfaces emit on the right counters/gauges.
func TestDomainMetrics_ReplicationFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v", Commit: "c", SampleInterval: time.Hour})

	// Drive each emission path once.
	m.OnReplicationApplied("aabbccdd")
	m.OnReplicationDropped("peer-a:7000", "ctx_cancel")
	m.OnPumpDropSelfEcho("peer-b:7000") // → dropped{peer,self_echo}
	m.OnAntiEntropyCycle()
	m.OnAntiEntropyBehind("peer-a:7000", "aabbccdd", 42)   // gauge=42, gaps=1
	m.OnAntiEntropyCaughtUp("peer-a:7000", "aabbccdd", 42) // gauge→0
	m.OnSearchConfig("peer-a:7000", false)
	m.OnSearchConfig("peer-b:7000", true)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_replication_applied_total",
		"lantern_replication_dropped_total",
		"lantern_replication_lag_seq",
		"lantern_anti_entropy_cycles_total",
		"lantern_anti_entropy_gaps_found_total",
		"lantern_search_config_match",
		"lantern_search_config_mismatch_total",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	if got := testutil.ToFloat64(m.replicationApplied.WithLabelValues("aabbccdd")); got != 1 {
		t.Errorf("replication_applied_total{origin=aabbccdd} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.replicationDropped.WithLabelValues("peer-a:7000", "ctx_cancel")); got != 1 {
		t.Errorf("replication_dropped_total{peer-a,ctx_cancel} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.replicationDropped.WithLabelValues("peer-b:7000", "self_echo")); got != 1 {
		t.Errorf("replication_dropped_total{peer-b,self_echo} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.antiEntropyCycles); got != 1 {
		t.Errorf("anti_entropy_cycles_total = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.antiEntropyGapsFound.WithLabelValues("peer-a:7000", "aabbccdd")); got != 1 {
		t.Errorf("anti_entropy_gaps_found_total = %v, want 1", got)
	}
	// CaughtUp reset the lag back to 0 after the Behind tick set it to 42.
	if got := testutil.ToFloat64(m.replicationLag.WithLabelValues("peer-a:7000", "aabbccdd")); got != 0 {
		t.Errorf("replication_lag_seq after CaughtUp = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.searchConfigMatch.WithLabelValues("peer-a:7000")); got != 0 {
		t.Errorf("search_config_match{peer-a} = %v, want 0", got)
	}
	if got := testutil.ToFloat64(m.searchConfigMatch.WithLabelValues("peer-b:7000")); got != 1 {
		t.Errorf("search_config_match{peer-b} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.searchConfigMismatch.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("search_config_mismatch_total{peer-a} = %v, want 1", got)
	}
}

// TestDomainMetrics_HotPathFamilies asserts the #220 hot-path collectors
// register with the expected family names, that the label set is
// pre-warmed (so dashboards render the full variant set from process
// start), and that the OnIlluminate / OnScan / OnBatch adapters emit on
// the right histograms. Per #410/#963 the Illuminate label space is the
// orthogonal quad (algorithm, reduction, objective, weighting).
func TestDomainMetrics_HotPathFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	m.OnIlluminate("bfs", "mst", "minimize", "raw", 17, 42, 5*time.Millisecond, 2*time.Millisecond)
	m.OnIlluminate("bfs", "none", "minimize", "raw", 1, 0, 100*time.Microsecond, 0) // optimize=0 → no observation on optimize phase
	m.OnIlluminateResult("ppr", "none", "maximize", "raw", "traversal", "deadline_exceeded")
	m.OnScan("ScanVertices", 64, 750*time.Microsecond)
	m.OnScan("ScanVertexKeys", 32, 500*time.Microsecond)
	m.OnScan("ScanEdges", 128, time.Millisecond)
	m.OnScan("CountVerticesByPrefix", 10, 100*time.Microsecond)
	m.OnScan("DeleteVerticesByPrefix", 32, 200*time.Microsecond)
	m.OnScan("DeleteEdgesByPrefix", 16, 150*time.Microsecond)
	m.OnScan("TopVerticesByDegree", 8, 250*time.Microsecond)
	m.OnBatch("PutVertices", 8)
	m.OnBatch("AddEdges", 4)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_illuminate_visited_vertices",
		"lantern_illuminate_visited_edges",
		"lantern_illuminate_duration_seconds",
		"lantern_illuminate_calls_total",
		"lantern_scan_results",
		"lantern_scan_duration_seconds",
		"lantern_batch_size",
		"lantern_search_results",
		"lantern_search_duration_seconds",
		"lantern_search_phase_duration_seconds",
		"lantern_search_calls_total",
		"lantern_search_work",
		"lantern_search_rejections_total",
		"lantern_search_index_terms",
		"lantern_search_index_docs",
		"lantern_search_index_physical_documents",
		"lantern_search_index_expired_documents",
		"lantern_search_index_expiration_queue_entries",
		"lantern_search_index_expiration_purged",
		"lantern_search_index_last_expiration_purge_duration_seconds",
		"lantern_search_index_retained_term_slots",
		"lantern_search_index_retained_ordinals",
		"lantern_search_index_postings",
		"lantern_search_index_position_entries",
		"lantern_search_index_estimated_live_bytes",
		"lantern_search_index_estimated_retained_bytes",
		"lantern_search_index_retained_ratio",
		"lantern_search_index_rebuild_count",
		"lantern_search_index_last_rebuild_duration_seconds",
		"lantern_search_index_healthy",
		"lantern_search_index_state",
		"lantern_vertex_hlc_entries",
		"lantern_vertex_hlc_entries_high_water",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// Pre-warming: every (algorithm × reduction × objective × weighting)
	// tuple should have a histogram row for the traversal phase even
	// though only "bfs/mst/minimize/raw" and "bfs/none/minimize/raw" have
	// observations.
	for _, algo := range algorithmLabels {
		for _, red := range reductionLabels {
			for _, obj := range objectiveLabels {
				for _, w := range weightingLabels {
					if got := testutil.CollectAndCount(m.illuminateDuration.WithLabelValues(algo, red, obj, w, "traversal").(prometheus.Histogram)); got == 0 {
						t.Errorf("illuminate_duration{algorithm=%q,reduction=%q,objective=%q,weighting=%q,phase=traversal} row not pre-warmed", algo, red, obj, w)
					}
				}
			}
		}
	}

	// Observed values land on the right rows.
	if got := histSampleCount(t, m.illuminateVisitedVertices.WithLabelValues("bfs", "mst", "minimize", "raw")); got != 1 {
		t.Errorf("illuminate_visited_vertices{bfs,mst,minimize,raw} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.illuminateDuration.WithLabelValues("bfs", "mst", "minimize", "raw", "optimize")); got != 1 {
		t.Errorf("illuminate_duration{bfs,mst,minimize,raw,optimize} sample count = %v, want 1", got)
	}
	// optimize=0 must NOT record on the optimize phase (it would skew
	// p99 toward zero on RPCs that didn't run an algorithm).
	if got := histSampleCount(t, m.illuminateDuration.WithLabelValues("bfs", "none", "minimize", "raw", "optimize")); got != 0 {
		t.Errorf("illuminate_duration{bfs,none,minimize,raw,optimize} sample count = %v, want 0 (optimize=0 must not observe)", got)
	}
	if got := testutil.ToFloat64(m.illuminateCalls.WithLabelValues("ppr", "none", "maximize", "raw", "traversal", "deadline_exceeded")); got != 1 {
		t.Errorf("illuminate_calls_total{ppr,traversal,deadline_exceeded} = %v, want 1", got)
	}

	for _, op := range scanOps {
		if got := histSampleCount(t, m.scanResults.WithLabelValues(op)); got != 1 {
			t.Errorf("scan_results{%s} sample count = %v, want 1", op, got)
		}
	}

	if got := histSampleCount(t, m.batchSize.WithLabelValues("PutVertices")); got != 1 {
		t.Errorf("batch_size{PutVertices} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.batchSize.WithLabelValues("AddEdges")); got != 1 {
		t.Errorf("batch_size{AddEdges} sample count = %v, want 1", got)
	}

	// One terminal observation emits all bounded SearchVertices series.
	m.OnSearchExecution(service.SearchObservation{
		Mode: "all", Phrase: true, Fuzziness: 2, PrefixTerms: true, PrefixPresent: true,
		Outcome: "resource_exhausted", Reason: string(search.WorkPostingVisits), TotalDuration: 3 * time.Millisecond,
		Stats: search.Stats{
			QueryBytes: 23, QueryTokens: 4, QueryClauses: 3, QueryTerms: 3,
			DictionaryVisits: 19, ExpansionRetained: 2, PostingVisits: 17,
			PositionVisits: 13, ExpirationVisits: 7, CandidateVisits: 11, CandidateSkips: 3,
			AnalysisDuration: 100 * time.Microsecond, ExpansionDuration: 200 * time.Microsecond, SelectionDuration: 300 * time.Microsecond,
		},
	})
	if got := histSampleCount(t, m.searchResults.WithLabelValues("all", "resource_exhausted")); got != 1 {
		t.Errorf("search_results{all,resource_exhausted} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.searchDuration.WithLabelValues("all", "resource_exhausted")); got != 1 {
		t.Errorf("search_duration{all,resource_exhausted} sample count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.searchCalls.WithLabelValues("all", "yes", "2", "yes", "yes", "resource_exhausted", string(search.WorkPostingVisits))); got != 1 {
		t.Errorf("search_calls_total{resource_exhausted,posting_visits} = %v, want 1", got)
	}
	if got := histSampleCount(t, m.searchWork.WithLabelValues(string(search.WorkPostingVisits), "all")); got != 1 {
		t.Errorf("search_work{posting_visits} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.searchWork.WithLabelValues(string(search.WorkCandidateVisits), "all")); got != 1 {
		t.Errorf("search_work{candidate_visits} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.searchWork.WithLabelValues(string(search.WorkCandidateSkips), "all")); got != 1 {
		t.Errorf("search_work{candidate_skips} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.searchPhaseDuration.WithLabelValues("analysis", "all")); got != 1 {
		t.Errorf("search_phase_duration{analysis,all} sample count = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.searchRejections.WithLabelValues(string(search.WorkPostingVisits))); got != 1 {
		t.Errorf("search_rejections_total{posting_visits} = %v, want 1", got)
	}
	m.OnSearchExecution(service.SearchObservation{Mode: "all", Outcome: "resource_exhausted", Reason: string(search.WorkExpirationVisits), Stats: search.Stats{ExpirationVisits: 5}})
	if got := histSampleCount(t, m.searchWork.WithLabelValues(string(search.WorkExpirationVisits), "all")); got != 2 {
		t.Errorf("search_work{expiration_visits} sample count = %v, want 2", got)
	}
	// Unknown values are collapsed to bounded fallbacks instead of becoming
	// user-controlled labels.
	m.OnSearchExecution(service.SearchObservation{Mode: "raw-query", Fuzziness: 999, Outcome: "raw-query", Reason: "raw-prefix"})
	if got := testutil.ToFloat64(m.searchCalls.WithLabelValues("unknown", "no", "other", "no", "no", "internal", "internal")); got != 1 {
		t.Errorf("search_calls_total{internal,internal} = %v, want sanitized sample", got)
	}
	mfs, err = reg.Gather()
	if err != nil {
		t.Fatalf("gather after search observations: %v", err)
	}
	for _, mf := range mfs {
		if !strings.HasPrefix(mf.GetName(), "lantern_search_") {
			continue
		}
		for _, metric := range mf.GetMetric() {
			for _, label := range metric.GetLabel() {
				if strings.Contains(label.GetName(), "query") || strings.Contains(label.GetName(), "prefix") && label.GetName() != "prefix_terms" && label.GetName() != "prefix_present" {
					t.Errorf("search metric %s has user-controlled label name %q", mf.GetName(), label.GetName())
				}
				if label.GetValue() == "raw-query" || label.GetValue() == "raw-prefix" {
					t.Errorf("search metric %s leaked raw label value %q", mf.GetName(), label.GetValue())
				}
			}
		}
	}
}

// TestDomainMetrics_VertexHLCHighWaterGauge confirms the #727 confirm-the-
// phenomenon gauge: lantern_vertex_hlc_entries_high_water registers and tick()
// publishes the bound sampler's value independently of the post-sweep len
// reported by lantern_vertex_hlc_entries. The pairing (low entries, high
// high-water) is the fingerprint of born-expired LWW churn retaining heap.
func TestDomainMetrics_VertexHLCHighWaterGauge(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	// Simulate a post-sweep state: the live count is drained low while the
	// per-cycle peak stays high (Go never shrinks the map's bucket array).
	m.BindVertexHLCSampler(func() int { return 7 })
	m.BindVertexHLCHighWaterSampler(func() int { return 240_000 })
	m.tick()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_vertex_hlc_entries",
		"lantern_vertex_hlc_entries_high_water",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	if got := testutil.ToFloat64(m.vertexHLCEntries); got != 7 {
		t.Errorf("vertex_hlc_entries = %v, want 7 (post-sweep len)", got)
	}
	if got := testutil.ToFloat64(m.vertexHLCHighWater); got != 240_000 {
		t.Errorf("vertex_hlc_entries_high_water = %v, want 240000 (per-cycle peak)", got)
	}
}

func TestDomainMetrics_SearchIndexCapacityGauges(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})
	m.BindSearchIndexSampler(func() search.IndexMemoryStats {
		return search.IndexMemoryStats{Documents: 2, PhysicalDocuments: 4, ExpiredDocuments: 2, ExpirationQueueEntries: 2, ExpirationPurged: 8, LastExpirationPurge: 15 * time.Millisecond, LiveTerms: 3, RetainedTermSlots: 5, RetainedOrdinals: 4, Postings: 7, PositionEntries: 11, EstimatedLiveBytes: 100, EstimatedRetainedBytes: 140, RebuildCount: 6, LastRebuildDuration: 25 * time.Millisecond, Health: search.IndexHealthy}
	})
	m.tick()
	checks := []struct {
		gauge prometheus.Gauge
		want  float64
	}{
		{m.searchIndexDocs, 2}, {m.searchIndexPhysicalDocs, 4}, {m.searchIndexExpiredDocs, 2},
		{m.searchIndexExpirationQueue, 2}, {m.searchIndexExpirationPurged, 8}, {m.searchIndexPurgeDuration, 0.015},
		{m.searchIndexTerms, 3}, {m.searchIndexRetainedTerms, 5},
		{m.searchIndexRetainedOrdinals, 4}, {m.searchIndexPostings, 7}, {m.searchIndexPositions, 11},
		{m.searchIndexLiveBytes, 100}, {m.searchIndexRetainedBytes, 140}, {m.searchIndexRebuilds, 6},
		{m.searchIndexRetainedRatio, 1.4}, {m.searchIndexRebuildDuration, 0.025}, {m.searchIndexHealthy, 1},
	}
	for _, check := range checks {
		if got := testutil.ToFloat64(check.gauge); got != check.want {
			t.Errorf("gauge = %v, want %v", got, check.want)
		}
	}
	if got := testutil.ToFloat64(m.searchIndexState.WithLabelValues("healthy")); got != 1 {
		t.Errorf("search_index_state{healthy} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.searchIndexState.WithLabelValues("disabled")); got != 0 {
		t.Errorf("search_index_state{disabled} = %v, want 0", got)
	}
}

func TestDomainMetrics_SearchIndexDefaultsToDisabled(t *testing.T) {
	m := New(prometheus.NewRegistry(), Options{SampleInterval: time.Hour})
	if got := testutil.ToFloat64(m.searchIndexState.WithLabelValues("disabled")); got != 1 {
		t.Errorf("search_index_state{disabled} = %v, want 1 before a sampler is bound", got)
	}
	if got := testutil.ToFloat64(m.searchIndexState.WithLabelValues("healthy")); got != 0 {
		t.Errorf("search_index_state{healthy} = %v, want 0", got)
	}
}

// histSampleCount extracts the observation count from a histogram via
// the dto round-trip. testutil.CollectAndCount counts series, not
// samples — which is the opposite of what these assertions need.
func histSampleCount(t *testing.T, o prometheus.Observer) uint64 {
	t.Helper()
	h, ok := o.(prometheus.Histogram)
	if !ok {
		t.Fatalf("observer is not a Histogram: %T", o)
	}
	var m dto.Metric
	if err := h.Write(&m); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	if m.Histogram == nil {
		return 0
	}
	return m.Histogram.GetSampleCount()
}

// TestDomainMetrics_HotPathSanitizesUnknownAxes confirms a stale or
// typo'd Illuminate axis label folds onto "unknown" rather than
// inflating the cardinality of the histogram. Per #410 each axis has
// its own bounded label set; an out-of-set value on ANY axis routes the
// observation to the "unknown" bucket for THAT axis only.
func TestDomainMetrics_HotPathSanitizesUnknownAxes(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	m.OnIlluminate("bogus", "alsobogus", "minimize", "raw", 1, 1, time.Microsecond, 0)
	if got := histSampleCount(t, m.illuminateVisitedVertices.WithLabelValues("unknown", "unknown", "minimize", "raw")); got != 1 {
		t.Errorf("unknown algorithm+reduction should fall back to \"unknown\"; sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.illuminateVisitedVertices.WithLabelValues("bogus", "alsobogus", "minimize", "raw")); got != 0 {
		t.Errorf("unknown algorithm must NOT route observations to its own row; sample count = %v, want 0", got)
	}
}

// TestDomainMetrics_ReplicationObservabilityFamilies asserts the #221
// collectors register, the per-MutationOp counter is pre-warmed, the
// pump adapter flips the per-peer gauge and emits the snapshot
// histograms, and the periodic tick populates mutation-log + origin
// gauges from the bound samplers.
func TestDomainMetrics_ReplicationObservabilityFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	// Adapter paths.
	m.OnPumpConnect("peer-a:7000")
	m.OnPumpConnect("peer-b:7000")
	m.OnPumpSnapshotReplayed("peer-a:7000", 100, 250, 5*time.Millisecond)
	m.OnPumpDisconnect("peer-b:7000", "ctx_cancel")
	for _, op := range []string{"PutVertex", "PutVertices", "AddEdge", "DeleteEdges"} {
		m.OnReplicationApply(op)
	}
	m.OnReplicationApply("bogus-op") // → "unknown"

	// Periodic tick should pick up bound samplers.
	m.BindMutationLogSampler(func() (int, int, uint64) { return 750, 1000, 42 })
	m.BindOriginStatesSampler(func() int { return 3 })
	m.tick()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_peer_connected",
		"lantern_replication_apply_total",
		"lantern_snapshot_replayed_total",
		"lantern_snapshot_vertices",
		"lantern_snapshot_edges",
		"lantern_snapshot_duration_seconds",
		"lantern_mutation_log_fill_ratio",
		"lantern_mutation_log_evicted_total",
		"lantern_origin_states_count",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// peer-a connected → 1, peer-b disconnected → 0.
	if got := testutil.ToFloat64(m.peerConnected.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("peer_connected{peer-a} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.peerConnected.WithLabelValues("peer-b:7000")); got != 0 {
		t.Errorf("peer_connected{peer-b} = %v, want 0", got)
	}
	// Disconnect with reason fans out to replication_dropped_total.
	if got := testutil.ToFloat64(m.replicationDropped.WithLabelValues("peer-b:7000", "ctx_cancel")); got != 1 {
		t.Errorf("replication_dropped_total{peer-b,ctx_cancel} = %v, want 1", got)
	}

	// Snapshot counter + histograms.
	if got := testutil.ToFloat64(m.snapshotReplayedTotal.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_replayed_total{peer-a} = %v, want 1", got)
	}
	if got := histSampleCount(t, m.snapshotVertices.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_vertices{peer-a} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.snapshotEdges.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_edges{peer-a} sample count = %v, want 1", got)
	}
	if got := histSampleCount(t, m.snapshotDuration.WithLabelValues("peer-a:7000")); got != 1 {
		t.Errorf("snapshot_duration_seconds{peer-a} sample count = %v, want 1", got)
	}

	// Per-MutationOp counter: pre-warmed for all 12 variants and the
	// observed ones incremented exactly once.
	for _, op := range replicationApplyOps {
		if testutil.ToFloat64(m.replicationApplyTotal.WithLabelValues(op)) < 0 {
			t.Errorf("replication_apply_total{op=%q} not pre-warmed", op)
		}
	}
	for _, want := range []struct {
		op  string
		exp float64
	}{
		{"PutVertex", 1},
		{"PutVertices", 1},
		{"AddEdge", 1},
		{"DeleteEdges", 1},
		{"unknown", 1},
	} {
		if got := testutil.ToFloat64(m.replicationApplyTotal.WithLabelValues(want.op)); got != want.exp {
			t.Errorf("replication_apply_total{op=%q} = %v, want %v", want.op, got, want.exp)
		}
	}

	// Sampler-driven gauges populated by tick().
	if got := testutil.ToFloat64(m.mutationLogFillRatio); got != 0.75 {
		t.Errorf("mutation_log_fill_ratio = %v, want 0.75", got)
	}
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 42 {
		t.Errorf("mutation_log_evicted_total = %v, want 42", got)
	}
	if got := testutil.ToFloat64(m.originStatesCount); got != 3 {
		t.Errorf("origin_states_count = %v, want 3", got)
	}
}

// TestDomainMetrics_MutationLogEvictedMonotonic confirms the
// Counter.Add path delta-applies cumulative evicted samples and
// recovers gracefully from a sampler reset (e.g. log rebuild).
func TestDomainMetrics_MutationLogEvictedMonotonic(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	var evicted uint64
	m.BindMutationLogSampler(func() (int, int, uint64) { return 1, 10, evicted })

	evicted = 5
	m.tick()
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 5 {
		t.Errorf("after first tick (5): counter = %v, want 5", got)
	}

	evicted = 12 // +7 since last sample
	m.tick()
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 12 {
		t.Errorf("after second tick (12): counter = %v, want 12", got)
	}

	evicted = 3 // sampler reset; treat as a fresh +3
	m.tick()
	if got := testutil.ToFloat64(m.mutationLogEvicted); got != 15 {
		t.Errorf("after reset+3: counter = %v, want 15 (12+3)", got)
	}
}

// TestDomainMetrics_RejectionFamilies asserts the #222 rejection
// collectors register, the validation reason label is pre-warmed for
// every canonical reason (plus the "unknown" fallback row), and the
// three adapters increment exactly the right counters.
func TestDomainMetrics_RejectionFamilies(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{SampleInterval: time.Hour})

	for _, r := range validationRejectReasons {
		m.OnValidationRejected(r)
	}
	m.OnValidationRejected("bogus-reason") // → "unknown"
	m.OnRateLimitRejected()
	m.OnRateLimitRejected()
	m.OnTombstoneClampRejected()

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_validation_rejected_total",
		"lantern_rate_limit_rejected_total",
		"lantern_tombstone_clamp_rejected_total",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// Pre-warming: every canonical reason has a row visible from the
	// start (so dashboards never have to wait for the first reject).
	for _, r := range validationRejectReasons {
		if got := testutil.ToFloat64(m.validationRejected.WithLabelValues(r)); got != 1 {
			t.Errorf("validation_rejected_total{reason=%q} = %v, want 1", r, got)
		}
	}
	if got := testutil.ToFloat64(m.validationRejected.WithLabelValues("unknown")); got != 1 {
		t.Errorf("validation_rejected_total{reason=unknown} = %v, want 1 (bogus reason should fold)", got)
	}
	if got := testutil.ToFloat64(m.rateLimitRejected); got != 2 {
		t.Errorf("rate_limit_rejected_total = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.tombstoneClampRejected); got != 1 {
		t.Errorf("tombstone_clamp_rejected_total = %v, want 1", got)
	}
}

func TestDomainMetrics_PubsubFamiliesAndPreWarm(t *testing.T) {
	// Acceptance for #240: the three pubsub collectors must register and
	// every DropPolicies label row must scrape as 0 from process start
	// (pre-warmed in New). Methods must increment / observe correctly.
	reg := prometheus.NewRegistry()
	m := New(reg, Options{Version: "v0.7.0", Commit: "deadbeef", SampleInterval: time.Hour})

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("gather: %v", err)
	}
	names := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	for _, want := range []string{
		"lantern_subscription_queue_depth",
		"lantern_subscription_dropped_total",
		"lantern_subscription_dispatch_duration_seconds",
	} {
		if !names[want] {
			t.Errorf("metric family %q not registered", want)
		}
	}

	// Every DropPolicy label row must be pre-warmed at 0.
	for _, p := range pubsub.DropPolicies {
		if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(p)); got != 0 {
			t.Errorf("pubsub_dropped_total{policy=%q} pre-warm = %v, want 0", p, got)
		}
	}

	// Methods bump the right collectors.
	m.OnPubsubQueueDepth(7)
	m.OnPubsubDrop(pubsub.DropPolicyNewest)
	m.OnPubsubDrop(pubsub.DropPolicyNewest)
	m.OnPubsubDrop(pubsub.DropPolicyOldest)
	m.OnPubsubDispatchDuration(3 * time.Millisecond)

	if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(pubsub.DropPolicyNewest)); got != 2 {
		t.Errorf("pubsub_dropped_total{policy=drop_newest} = %v, want 2", got)
	}
	if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(pubsub.DropPolicyOldest)); got != 1 {
		t.Errorf("pubsub_dropped_total{policy=drop_oldest} = %v, want 1", got)
	}
	if got := testutil.ToFloat64(m.pubsubDropped.WithLabelValues(pubsub.DropPolicyNewestAfterOldest)); got != 0 {
		t.Errorf("pubsub_dropped_total{policy=drop_newest_after_oldest} = %v, want 0 (not called)", got)
	}

	// Histograms expose count via the dto.Metric; assert sample_count.
	if got := collectHistogramCount(t, m.pubsubQueueDepth); got != 1 {
		t.Errorf("pubsub_queue_depth count = %d, want 1", got)
	}
	if got := collectHistogramCount(t, m.pubsubDispatchDuration); got != 1 {
		t.Errorf("pubsub_dispatch_duration count = %d, want 1", got)
	}
}

func collectHistogramCount(t *testing.T, h prometheus.Histogram) uint64 {
	t.Helper()
	var dtoM dto.Metric
	if err := h.Write(&dtoM); err != nil {
		t.Fatalf("histogram write: %v", err)
	}
	return dtoM.GetHistogram().GetSampleCount()
}
