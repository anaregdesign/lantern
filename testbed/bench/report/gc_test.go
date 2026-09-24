package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGCTickReportUsesRawTickDurations(t *testing.T) {
	dir := t.TempDir()
	// The intentionally false gc_p99_ns summary proves the report derives
	// quantiles from the raw samples, rather than another pre-aggregated field.
	data := `{"sha":"abc123","vertices":100000,"seeded_edges":3200000,"shape":"hub","budget_tails":1000,"interval_ms":1000,"gc_p99_ns":999999999,"unreclaimed_probes":1,"samples":[{"duration_ns":1000000,"sweep":{"ScannedEdges":5,"ExpiredContributions":2,"CompactedContributionsInLiveBuckets":1,"ZeroRemoved":1,"DanglingRemoved":0,"BacklogTails":4}},{"duration_ns":3000000,"sweep":{"ScannedEdges":7,"ExpiredContributions":1,"CompactedContributionsInLiveBuckets":1,"ZeroRemoved":0,"DanglingRemoved":2,"BacklogTails":2}}]}`
	if err := os.WriteFile(filepath.Join(dir, "gc_hub_budget_run1.json"), []byte(data), 0o644); err != nil {
		t.Fatal(err)
	}
	in, err := LoadInput(dir, "edge_ttl_churn", "20260101T000000Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(in.GCFiles) != 1 {
		t.Fatalf("GC files = %d, want 1", len(in.GCFiles))
	}
	var buf bytes.Buffer
	if err := RenderReport(&buf, in); err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"## GC ticks", "direct raw `GraphCache.Watch`", "gc_p99 ms",
		"`gc_hub_budget_run1.json`", "`abc123`", "`hub`", "100000 / 3200000",
		"| 2 | 3.00 | 3.00 | 3.00 | 12 | 3 | 2 | 1 / 2 | 4 | 0.00 | 1 |",
		"Synthetic GC trigger", "| `hub` | 1000 | 1000 | 100000 / 3200000 | 1 | 0 | `unknown` |",
	} {
		if !strings.Contains(buf.String(), want) {
			t.Errorf("report missing %q:\n%s", want, buf.String())
		}
	}
}

func TestGCTickReportRejectsMissingRawSamples(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "gc_empty.json"), []byte(`{"gc_p99_ns":100}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadInput(dir, "x", "t"); err == nil || !strings.Contains(err.Error(), "no direct GC tick samples") {
		t.Fatalf("LoadInput error = %v, want missing raw samples", err)
	}
}

func TestGCTickReportRequiresTwoOfThreeComparableHundredTickRuns(t *testing.T) {
	makeFile := func(name string, p99 int64, ticks int) GCFile {
		samples := make([]GCSample, ticks)
		for i := range samples {
			samples[i].DurationNS = p99
		}
		return GCFile{Name: name, SHA: "same", Vertices: 100000, SeededEdges: 3200000,
			Shape: "uniform", BudgetTails: 5000, IntervalMS: 1000, Samples: samples}
	}
	in := Input{GCFiles: []GCFile{
		makeFile("gc_r1.json", 200_000_000, 100),
		makeFile("gc_r2.json", 201_000_000, 100),
		makeFile("gc_r3.json", 199_000_000, 100),
	}}
	var buf bytes.Buffer
	if err := RenderReport(&buf, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "| `uniform` | 5000 | 1000 | 100000 / 3200000 | 3 | 2 | `fired` |") {
		t.Fatalf("2/3 threshold not applied:\n%s", buf.String())
	}
	in.GCFiles[2].Samples = in.GCFiles[2].Samples[:99]
	buf.Reset()
	if err := RenderReport(&buf, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "| `uniform` | 5000 | 1000 | 100000 / 3200000 | 3 | 2 | `unknown` |") {
		t.Fatalf("undersampled run must make verdict unknown:\n%s", buf.String())
	}
}
