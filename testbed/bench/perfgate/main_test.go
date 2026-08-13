package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestEvaluateSeparatesTypedLifecycleFromUnexpectedErrors(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.yaml")
	writeText(t, scenario, `target:
  endpoints: ["localhost:6380"]
  calls:
    - { name: expected, call: graph.v1.LanternService/SearchVertices }
    - { name: mixed, call: graph.v1.LanternService/SearchVertices }
perf_gate:
  min_steady_rps_total: 10
  max_p99_ms: 100
  max_non_ok_ratio: 0.02
  producers:
    expected: { min_steady_rps: 1, max_p99_ms: 100, max_non_ok_ratio: 0.02 }
    mixed: { min_steady_rps: 1, max_p99_ms: 100, max_non_ok_ratio: 0.02 }
lifecycle_gate:
  reason: SEARCH_INDEX_INCOMPLETE
  max_ratio: 0.20
  producers:
    expected:
      metric_labels: { mode: server, phrase: "no", fuzziness: "0", prefix_terms: "no", prefix_present: "no" }
    mixed:
      metric_labels: { mode: server, phrase: "yes", fuzziness: "0", prefix_terms: "no", prefix_present: "no" }
`)
	writeGhz(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), 100, 20, map[string]int{
		"OK": 85, indexIncompleteStatus: 12, "Unavailable": 3,
	})
	writeGhz(t, filepath.Join(dir, "ghz_steady_1_localhost_6380.json"), 100, 20, map[string]int{
		"OK": 94, indexIncompleteStatus: 4, "Unavailable": 2,
	})
	pre := snapshotWithSeries(0,
		seriesFor(false, 10),
		seriesFor(true, 20),
	)
	post := snapshotWithSeries(0,
		seriesFor(false, 22), // all 12 FailedPrecondition responses are expected
		seriesFor(true, 23),  // one FailedPrecondition response remains unexpected
	)
	prePath, postPath := filepath.Join(dir, "pre.json"), filepath.Join(dir, "post.json")
	if err := writeJSON(prePath, pre); err != nil {
		t.Fatal(err)
	}
	if err := writeJSON(postPath, post); err != nil {
		t.Fatal(err)
	}

	report, err := evaluate(scenario, dir, prePath, postPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "fail" {
		t.Fatalf("verdict = %s, want fail because mixed has 3 unexpected errors", report.Verdict)
	}
	if got := report.ProducerResults[0].Observed; got.RawNonOK != 15 || got.ExpectedLifecycle != 12 || got.UnexpectedNonOK != 3 {
		t.Errorf("expected producer observation = %+v", got)
	}
	if report.ProducerResults[0].Verdict != "fail" {
		t.Errorf("expected producer should still fail: 3%% Unavailable remains above the 2%% generic budget")
	}
	if got := report.ProducerResults[1].Observed; got.RawNonOK != 6 || got.ExpectedLifecycle != 3 || got.UnexpectedNonOK != 3 {
		t.Errorf("mixed producer observation = %+v", got)
	}

	// Prove expected lifecycle traffic itself does not consume the generic
	// budget by removing the unrelated Unavailable responses.
	writeGhz(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), 100, 20, map[string]int{
		"OK": 88, indexIncompleteStatus: 12,
	})
	writeGhz(t, filepath.Join(dir, "ghz_steady_1_localhost_6380.json"), 100, 20, map[string]int{
		"OK": 97, indexIncompleteStatus: 3,
	})
	report, err = evaluate(scenario, dir, prePath, postPath)
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "pass" || report.Observed.RawNonOKTotal != 15 || report.Observed.UnexpectedNonOKTotal != 0 {
		t.Fatalf("typed-only report = %+v", report)
	}
}

func TestEvaluateLifecycleBoundAndClassificationFailClosed(t *testing.T) {
	tests := []struct {
		name        string
		maxRatio    string
		status      int
		counter     float64
		wantFailure string
	}{
		{name: "frequency bound", maxRatio: "0.05", status: 10, counter: 10},
		{name: "counter exceeds matching status", maxRatio: "0.20", status: 4, counter: 5, wantFailure: "exceeds FailedPrecondition"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			scenario := filepath.Join(dir, "scenario.yaml")
			writeText(t, scenario, fmt.Sprintf(`target:
  endpoints: ["localhost:6380"]
  calls: [{ name: search, call: graph.v1.LanternService/SearchVertices }]
perf_gate:
  max_non_ok_ratio: 0.02
  producers:
    search: { max_non_ok_ratio: 0.02 }
lifecycle_gate:
  reason: SEARCH_INDEX_INCOMPLETE
  max_ratio: %s
  producers:
    search:
      metric_labels: { mode: server, phrase: "no", fuzziness: "0", prefix_terms: "no", prefix_present: "no" }
`, tc.maxRatio))
			writeGhz(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), 100, 10, map[string]int{
				"OK": 100 - tc.status, indexIncompleteStatus: tc.status,
			})
			pre, post := snapshotWithSeries(0, seriesFor(false, 0)), snapshotWithSeries(0, seriesFor(false, tc.counter))
			prePath, postPath := filepath.Join(dir, "pre.json"), filepath.Join(dir, "post.json")
			if err := writeJSON(prePath, pre); err != nil {
				t.Fatal(err)
			}
			if err := writeJSON(postPath, post); err != nil {
				t.Fatal(err)
			}
			report, err := evaluate(scenario, dir, prePath, postPath)
			if err != nil {
				t.Fatal(err)
			}
			if report.Verdict != "fail" || report.ProducerResults[0].Verdict != "fail" {
				t.Fatalf("report = %+v", report)
			}
			if tc.wantFailure != "" && (len(report.ProducerResults[0].Failures) == 0 || !strings.Contains(report.ProducerResults[0].Failures[0], tc.wantFailure)) {
				t.Fatalf("failures = %v", report.ProducerResults[0].Failures)
			}
		})
	}
}

func TestEvaluateWithoutLifecyclePreservesGenericNonOKContract(t *testing.T) {
	dir := t.TempDir()
	scenario := filepath.Join(dir, "scenario.yaml")
	writeText(t, scenario, `target:
  endpoints: ["localhost:6380"]
  calls: [{ name: writer }]
perf_gate:
  max_non_ok_ratio: 0.02
  producers:
    writer: { max_non_ok_ratio: 0.02 }
`)
	writeGhz(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), 100, 10, map[string]int{
		"OK": 95, indexIncompleteStatus: 5,
	})
	report, err := evaluate(scenario, dir, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if report.Verdict != "fail" || report.Observed.RawNonOKTotal != 5 || report.Observed.UnexpectedNonOKTotal != 5 {
		t.Fatalf("report = %+v", report)
	}
}

func TestLoadProducerSummaryRequiresCompleteStatusDistribution(t *testing.T) {
	dir := t.TempDir()
	writeGhz(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), 100, 10, map[string]int{
		"OK":          94,
		"Unavailable": 5,
	})
	_, err := loadProducerSummary(dir, 0, true)
	if err == nil || !strings.Contains(err.Error(), "status distribution total 99 does not equal count 100") {
		t.Fatalf("loadProducerSummary error = %v, want incomplete status distribution rejection", err)
	}
}

func TestLoadProducerSummaryRejectsStatusDistributionOverflow(t *testing.T) {
	if strconv.IntSize < 64 {
		t.Skip("requires 64-bit int status counts")
	}
	dir := t.TempDir()
	writeText(t, filepath.Join(dir, "ghz_steady_0_localhost_6380.json"), `{
  "count": 0,
  "rps": 0,
  "statusCodeDistribution": {
    "OK": 9223372036854775807,
    "Unavailable": 9223372036854775807,
    "Canceled": 2
  }
}`)
	_, err := loadProducerSummary(dir, 0, true)
	if err == nil || !strings.Contains(err.Error(), "status distribution total exceeds int64") {
		t.Fatalf("loadProducerSummary error = %v, want status total overflow rejection", err)
	}
}

func TestScenarioCorpusUsesValidPerfGateConfiguration(t *testing.T) {
	paths, err := filepath.Glob(filepath.Join("..", "scenarios", "*.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(paths) == 0 {
		t.Fatal("no scenarios found")
	}
	for _, path := range paths {
		t.Run(filepath.Base(path), func(t *testing.T) {
			doc, err := loadScenario(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateScenario(doc); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestValidateScenarioRejectsUnsafeLifecycleJoins(t *testing.T) {
	selector := `metric_labels: { mode: server, phrase: "no", fuzziness: "0", prefix_terms: "no", prefix_present: "no" }`
	tests := []struct {
		name    string
		calls   string
		gates   string
		wantErr string
	}{
		{
			name:    "non search producer",
			calls:   `[{ name: writer, call: graph.v1.LanternService/PutVertex }]`,
			gates:   "    writer:\n      " + selector,
			wantErr: "is not a SearchVertices call",
		},
		{
			name:    "ambiguous same replica selector",
			calls:   `[{ name: first, call: graph.v1.LanternService/SearchVertices }, { name: second, call: graph.v1.LanternService/SearchVertices }]`,
			gates:   "    first:\n      " + selector + "\n    second:\n      " + selector,
			wantErr: "share one replica/selector",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "scenario.yaml")
			writeText(t, path, fmt.Sprintf(`target:
  endpoints: ["localhost:6380"]
  calls: %s
lifecycle_gate:
  reason: SEARCH_INDEX_INCOMPLETE
  max_ratio: 0.10
  producers:
%s
`, tc.calls, tc.gates))
			doc, err := loadScenario(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := validateScenario(doc); err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("validateScenario error = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func snapshotWithSeries(index int, series ...snapshotSeries) searchCounterSnapshot {
	return searchCounterSnapshot{
		Metric:   searchCallsMetric,
		Replicas: []snapshotReplica{{Index: index, Endpoint: "test", Series: series}},
	}
}

func seriesFor(phrase bool, value float64) snapshotSeries {
	phraseLabel := "no"
	if phrase {
		phraseLabel = "yes"
	}
	return snapshotSeries{
		Labels: map[string]string{
			"mode": "server", "phrase": phraseLabel, "fuzziness": "0",
			"prefix_terms": "no", "prefix_present": "no",
			"outcome": indexIncompleteOutcome, "reason": indexIncompleteMetricLabel,
		},
		Value: value,
	}
}

func writeGhz(t *testing.T, path string, count uint64, rps float64, statuses map[string]int) {
	t.Helper()
	summary := ghzSummary{Count: count, RPS: rps, StatusCodeDistribution: statuses}
	summary.LatencyDistribution = append(summary.LatencyDistribution, struct {
		Percentage int   `json:"percentage"`
		Latency    int64 `json:"latency"`
	}{Percentage: 99, Latency: 10_000_000})
	if err := writeJSON(path, summary); err != nil {
		t.Fatal(err)
	}
}

func writeText(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0o644); err != nil {
		t.Fatal(err)
	}
}
