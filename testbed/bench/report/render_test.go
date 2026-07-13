package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRenderReport_AllSectionsAndVerdict(t *testing.T) {
	in := Input{
		Scenario:  "write_heavy",
		Timestamp: "20250101T000000Z",
		LeakGate: &LeakGate{
			Verdict: "pass",
			Thresholds: struct {
				GoroutineMaxDelta   int `json:"goroutine_max_delta"`
				HeapAllocMaxDeltaMB int `json:"heap_alloc_max_delta_mb"`
				HeapInuseMaxDeltaMB int `json:"heap_inuse_max_delta_mb,omitempty"`
			}{GoroutineMaxDelta: 20, HeapAllocMaxDeltaMB: 32},
			Replicas: []LeakGateReplica{{
				Endpoint:      "localhost:9390",
				GoroutinesPre: 100, GoroutinesPost: 110, GoroutineDelta: 10,
				HeapInusePreBytes: 10 << 20, HeapInusePostBytes: 15 << 20, HeapInuseDeltaBytes: 5 << 20,
				HeapAllocPreBytes: 8 << 20, HeapAllocPostBytes: 9 << 20, HeapAllocDeltaBytes: 1 << 20,
				HeapObjectsPre: 1000, HeapObjectsPost: 1100, HeapObjectsDelta: 100,
				VertexHLCEntriesPre: 5, VertexHLCEntriesPost: 12,
				VertexHLCHighWaterPre: 200_000, VertexHLCHighWaterPost: 240_000,
			}},
		},
		PerfGate: func() *PerfGate {
			pg := &PerfGate{Verdict: "pass"}
			minRps := 50.0
			pg.Thresholds.MinSteadyRpsTotal = &minRps // p99 / non-OK deliberately un-gated
			pg.Observed.Producers = 1
			pg.Observed.SteadyRpsTotal = 100.5
			pg.Observed.P99WorstMs = 50
			pg.Observed.CountTotal = 1000
			pg.Observed.NonOKTotal = 10
			pg.Observed.NonOKRatio = 0.01
			return pg
		}(),
		GhzFiles: []GhzFile{{
			Name: "ghz_steady_localhost_6380.json",
			Summary: GhzSummary{
				Count: 1000, Rps: 100.5, Average: 5_000_000,
				StatusCodeDistribution: map[string]int{"OK": 990, "DEADLINE_EXCEEDED": 10},
				LatencyDistribution: []struct {
					Percentage int   `json:"percentage"`
					Latency    int64 `json:"latency"`
				}{{Percentage: 99, Latency: 50_000_000}},
			},
		}},
		PromIndex: []PromIndexEntry{{Query: "go_goroutines", File: "q_01.json"}},
		PprofList: []string{"localhost_9390__post__heap.pb.gz"},
	}

	var buf bytes.Buffer
	if err := RenderReport(&buf, in); err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	out := buf.String()

	mustContain := []string{
		"# Bench report — `write_heavy` @ `20250101T000000Z`",
		"**Leak gate verdict:** `pass`",
		"**Perf gate verdict:** `pass`",
		"## Leak gate",
		"## Perf gate",
		"| steady rps (total, floor) | 50.0 | 100.5 |",
		"| p99 ms (worst, ceiling) | — | 50.00 |", // un-gated metric renders "—"
		"| non-OK ratio (ceiling) | — | 0.01000 |",
		"goroutine_max_delta=20",
		"heap_alloc_max_delta_mb=32",
		"`localhost:9390`",
		"**+10**",
		"| 12 / 240000 |", // vertexHLC entries (drained) / high-water (peak)
		"50.00",           // p99 ms
		"`ghz_steady_localhost_6380.json`",
		"| 10 |", // non-OK column
		"`go_goroutines` → `prom/q_01.json`",
		"`pprof/localhost_9390__post__heap.pb.gz`",
		"## Drill-down",
	}
	for _, want := range mustContain {
		if !strings.Contains(out, want) {
			t.Errorf("report missing %q\n---\n%s", want, out)
		}
	}
	if strings.Contains(out, "DEADLINE_EXCEEDED") {
		t.Errorf("report should not name individual non-OK codes:\n%s", out)
	}
}

func TestRenderReport_StreamingGhzRowsMaskNonOK(t *testing.T) {
	in := Input{
		Scenario:  "many_subscribers",
		Timestamp: "t",
		GhzFiles: []GhzFile{
			{
				Name: "ghz_steady_localhost_6380.json",
				Summary: GhzSummary{
					Count: 100, Rps: 100,
					StatusCodeDistribution: map[string]int{"OK": 95, "Unavailable": 5},
				},
			},
			{
				Name: "ghz_sub_1.json",
				Summary: GhzSummary{
					Count: 130000, Rps: 433,
					StatusCodeDistribution: map[string]int{"FailedPrecondition": 130000},
				},
			},
		},
	}
	var buf bytes.Buffer
	if err := RenderReport(&buf, in); err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "| `ghz_steady_localhost_6380.json` | 100 | 100.0 | 0.00 | 0.00 | 5 |") {
		t.Errorf("steady row should report numeric non-OK; got:\n%s", out)
	}
	if !strings.Contains(out, "| `ghz_sub_1.json` | 130000 | 433.0 | 0.00 | 0.00 | — |") {
		t.Errorf("streaming row should mask non-OK with \"—\"; got:\n%s", out)
	}
	if !strings.Contains(out, "`ghz_sub_*` rows show `—` for non-OK") {
		t.Errorf("expected footnote explaining masked non-OK; got:\n%s", out)
	}
}

func TestRenderReport_HandlesMissingArtifactsGracefully(t *testing.T) {
	var buf bytes.Buffer
	if err := RenderReport(&buf, Input{Scenario: "x", Timestamp: "t"}); err != nil {
		t.Fatalf("RenderReport: %v", err)
	}
	out := buf.String()
	for _, want := range []string{
		"**Leak gate verdict:** `(no leak gate captured)`",
		"**Perf gate verdict:** `(no perf gate configured)`",
		"_not captured_",
		"_not configured_",
		"_no ghz artifacts found_",
		"_no prom queries captured_",
		"_no pprof profiles captured_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in: %s", want, out)
		}
	}
}

func TestRenderReport_ShowsIndependentProducerGates(t *testing.T) {
	minRPS, maxP99 := 10.0, 250.0
	pg := &PerfGate{Verdict: "fail"}
	pg.ProducerResults = []PerfProducerResult{{Name: "community_arborescence_min", Verdict: "fail"}}
	pg.ProducerResults[0].Thresholds.MinSteadyRps = &minRPS
	pg.ProducerResults[0].Thresholds.MaxP99Ms = &maxP99
	pg.ProducerResults[0].Observed.SteadyRps = 8
	pg.ProducerResults[0].Observed.P99Ms = 300
	var buf bytes.Buffer
	if err := RenderReport(&buf, Input{Scenario: "x", Timestamp: "t", PerfGate: pg}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"Named producer gates are conjunctive",
		"`community_arborescence_min`",
		"`fail`",
		"8.0 (10.0)",
		"300.00 (250.00)",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in report:\n%s", want, out)
		}
	}
}

func TestRenderReport_ShowsMetricAndSemanticGates(t *testing.T) {
	pre, post, delta, ratio := 100.0, 105.0, 5.0, 1.05
	mg := &MetricGate{Verdict: "pass", Results: []MetricGateResult{{
		Endpoint: "localhost:9390",
		Metric:   "lantern_search_index_docs",
		Pre:      &pre,
		Post:     &post,
		Delta:    &delta,
		Ratio:    &ratio,
		Verdict:  "pass",
	}}}
	semanticPre := &SemanticGate{Phase: "pre", Verdict: "fail", Replicas: []SemanticGateReplica{{Endpoint: "http://localhost:6380", Checks: 11, Verdict: "fail", Failure: "deep_pagination"}}}
	semanticPost := &SemanticGate{Phase: "post", Verdict: "pass", Replicas: []SemanticGateReplica{{Endpoint: "http://localhost:6380", Checks: 11, Verdict: "pass"}}}

	var buf bytes.Buffer
	if err := RenderReport(&buf, Input{Scenario: "search", Timestamp: "t", MetricGate: mg, SemanticPre: semanticPre, SemanticPost: semanticPost}); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	for _, want := range []string{
		"**Metric gate verdict:** `pass`",
		"**Semantic gate verdict:** `fail`",
		"## Metric gate",
		"`lantern_search_index_docs`",
		"100 | 105 | 5 | 1.05",
		"## Semantic gate",
		"| `pre` | `http://localhost:6380` | 11 | `fail` | `deep_pagination` |",
		"| `post` | `http://localhost:6380` | 11 | `pass` | — |",
		"query text, prefixes, keys, and values are omitted",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in report:\n%s", want, out)
		}
	}
}

func TestLoadInput_AssemblesFromDisk(t *testing.T) {
	dir := t.TempDir()

	lg := LeakGate{Verdict: "fail"}
	lg.Thresholds.GoroutineMaxDelta = 5
	lg.Thresholds.HeapAllocMaxDeltaMB = 16
	lg.Replicas = []LeakGateReplica{{Endpoint: "x", GoroutineDelta: 9, HeapAllocDeltaBytes: 1 << 20}}
	mustWriteJSON(t, filepath.Join(dir, "leak_gate.json"), lg)

	pg := PerfGate{Verdict: "fail"}
	maxP99 := 250.0
	pg.Thresholds.MaxP99Ms = &maxP99
	pg.Observed.P99WorstMs = 900
	mustWriteJSON(t, filepath.Join(dir, "perf_gate.json"), pg)

	mg := MetricGate{Verdict: "pass"}
	mustWriteJSON(t, filepath.Join(dir, "metric_gate.json"), mg)
	mustWriteJSON(t, filepath.Join(dir, "semantic_pre.json"), SemanticGate{Phase: "pre", Verdict: "pass"})
	mustWriteJSON(t, filepath.Join(dir, "semantic_post.json"), SemanticGate{Phase: "post", Verdict: "pass"})

	gh := GhzSummary{Count: 42, Rps: 1, Average: 1_000_000,
		StatusCodeDistribution: map[string]int{"OK": 42},
		LatencyDistribution: []struct {
			Percentage int   `json:"percentage"`
			Latency    int64 `json:"latency"`
		}{{Percentage: 99, Latency: 2_000_000}}}
	mustWriteJSON(t, filepath.Join(dir, "ghz_steady_localhost_6380.json"), gh)

	if err := os.MkdirAll(filepath.Join(dir, "prom"), 0o755); err != nil {
		t.Fatal(err)
	}
	pi, _ := json.Marshal(PromIndexEntry{Query: "up", File: "q_01.json"})
	if err := os.WriteFile(filepath.Join(dir, "prom", "_index.ndjson"), append(pi, '\n'), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(filepath.Join(dir, "pprof"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pprof", "x__post__heap.pb.gz"), []byte{}, 0o644); err != nil {
		t.Fatal(err)
	}

	in, err := LoadInput(dir, "sc", "ts")
	if err != nil {
		t.Fatalf("LoadInput: %v", err)
	}
	if in.LeakGate == nil || in.LeakGate.Verdict != "fail" {
		t.Errorf("leak gate not loaded: %+v", in.LeakGate)
	}
	if in.PerfGate == nil || in.PerfGate.Verdict != "fail" ||
		in.PerfGate.Thresholds.MaxP99Ms == nil || *in.PerfGate.Thresholds.MaxP99Ms != 250 ||
		in.PerfGate.Thresholds.MinSteadyRpsTotal != nil {
		t.Errorf("perf gate not loaded: %+v", in.PerfGate)
	}
	if in.MetricGate == nil || in.MetricGate.Verdict != "pass" {
		t.Errorf("metric gate not loaded: %+v", in.MetricGate)
	}
	if in.SemanticPre == nil || in.SemanticPre.Phase != "pre" || in.SemanticPost == nil || in.SemanticPost.Phase != "post" {
		t.Errorf("semantic gates not loaded: pre=%+v post=%+v", in.SemanticPre, in.SemanticPost)
	}
	if len(in.GhzFiles) != 1 || in.GhzFiles[0].Summary.Count != 42 {
		t.Errorf("ghz files: %+v", in.GhzFiles)
	}
	if len(in.PromIndex) != 1 || in.PromIndex[0].Query != "up" {
		t.Errorf("prom index: %+v", in.PromIndex)
	}
	if len(in.PprofList) != 1 || in.PprofList[0] != "x__post__heap.pb.gz" {
		t.Errorf("pprof list: %+v", in.PprofList)
	}
}

func mustWriteJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}
