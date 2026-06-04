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
			}},
		},
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
		"## Leak gate",
		"goroutine_max_delta=20",
		"heap_alloc_max_delta_mb=32",
		"`localhost:9390`",
		"**+10**",
		"50.00", // p99 ms
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
		"_not captured_",
		"_no ghz artifacts found_",
		"_no prom queries captured_",
		"_no pprof profiles captured_",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in: %s", want, out)
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
