package main

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestRunEvaluateProducerFailureForcesFailedArtifact(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.yaml")
	out := filepath.Join(dir, "perf_gate.json")
	writeText(t, scenario, `target:
  endpoints: ["localhost:6380"]
  calls: [{ name: writer, call: graph.v1.LanternService/PutVertex }]
perf_gate:
  max_non_ok_ratio: 0.02
  producers:
    writer: { max_non_ok_ratio: 0.02 }
`)
	writeGhz(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), 100, 10, map[string]int{"OK": 100})

	code := runEvaluate([]string{
		"-scenario", scenario,
		"-run-dir", dir,
		"-out", out,
		"-producer-failed",
	})
	if code != 1 {
		t.Fatalf("runEvaluate exit code = %d, want 1", code)
	}

	var report perfGateReport
	if err := readJSON(out, &report); err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "fail" {
		t.Fatalf("verdict = %q, want fail", report.Verdict)
	}
	want := "one or more steady ghz producer processes exited non-zero"
	if !slices.Contains(report.Failures, want) {
		t.Fatalf("failures = %v, want %q", report.Failures, want)
	}
}
