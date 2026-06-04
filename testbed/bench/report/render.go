// Package main is the bench-harness report renderer.
//
// It consumes the JSON artifacts emitted by testbed/bench/run.sh under
// out/<scenario>/<timestamp>/ and writes a Markdown summary to stdout:
//
//   - leak_gate.json            -> Leak Gate section + verdict header
//   - ghz_*.json                -> Per-phase / per-call latency tables
//   - prom/_index.ndjson        -> Indexed list of Prom range queries
//   - pprof/                    -> Inventory of captured pprof profiles
//
// The renderer is intentionally side-effect free aside from reading the
// run directory and writing Markdown to its writer, so it round-trips
// cleanly under unit tests with synthetic fixtures.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// LeakGate mirrors the schema written by run.sh.
//
// The leak gate evaluates against heap_alloc (post-GC live bytes), not
// heap_inuse (span-level, includes free slots). See issue #248 — under
// sustained churn, heap_inuse drifts upward as the allocator opens new
// spans even when live memory is flat, producing false-positive verdicts.
type LeakGate struct {
	Thresholds struct {
		GoroutineMaxDelta   int `json:"goroutine_max_delta"`
		HeapAllocMaxDeltaMB int `json:"heap_alloc_max_delta_mb"`
		// HeapInuseMaxDeltaMB is the legacy field name retained for backward
		// compatibility with older leak_gate.json artifacts.
		HeapInuseMaxDeltaMB int `json:"heap_inuse_max_delta_mb,omitempty"`
	} `json:"thresholds"`
	Replicas []LeakGateReplica `json:"replicas"`
	Verdict  string            `json:"verdict"`
}

// LeakGateReplica is one row of the per-replica delta table.
type LeakGateReplica struct {
	Endpoint            string `json:"endpoint"`
	GoroutinesPre       int    `json:"goroutines_pre"`
	GoroutinesPost      int    `json:"goroutines_post"`
	GoroutineDelta      int    `json:"goroutine_delta"`
	HeapInusePreBytes   int64  `json:"heap_inuse_pre_bytes"`
	HeapInusePostBytes  int64  `json:"heap_inuse_post_bytes"`
	HeapInuseDeltaBytes int64  `json:"heap_inuse_delta_bytes"`
	HeapAllocPreBytes   int64  `json:"heap_alloc_pre_bytes"`
	HeapAllocPostBytes  int64  `json:"heap_alloc_post_bytes"`
	HeapAllocDeltaBytes int64  `json:"heap_alloc_delta_bytes"`
	HeapObjectsPre      int64  `json:"heap_objects_pre"`
	HeapObjectsPost     int64  `json:"heap_objects_post"`
	HeapObjectsDelta    int64  `json:"heap_objects_delta"`
}

// GhzSummary captures the subset of fields ghz writes that the report uses.
// All durations are nanoseconds as serialised by ghz --format json.
type GhzSummary struct {
	Name    string  `json:"name"`
	Count   uint64  `json:"count"`
	Average int64   `json:"average"`
	Fastest int64   `json:"fastest"`
	Slowest int64   `json:"slowest"`
	Rps     float64 `json:"rps"`

	StatusCodeDistribution map[string]int `json:"statusCodeDistribution"`
	LatencyDistribution    []struct {
		Percentage int   `json:"percentage"`
		Latency    int64 `json:"latency"`
	} `json:"latencyDistribution"`
}

// PromIndexEntry is one row of prom/_index.ndjson.
type PromIndexEntry struct {
	Query string `json:"query"`
	File  string `json:"file"`
}

// Input is the rendered report's input model. All fields are optional —
// missing artifacts produce "(not captured)" sections rather than errors.
type Input struct {
	Scenario  string
	Timestamp string
	LeakGate  *LeakGate
	GhzFiles  []GhzFile
	PromIndex []PromIndexEntry
	PprofList []string
}

// GhzFile is one ghz JSON output paired with its source filename so the
// report can show readers which on-disk artifact each row came from.
type GhzFile struct {
	Name    string
	Summary GhzSummary
}

// LoadInput walks `dir` and assembles an Input from whatever artifacts the
// run produced. Missing files are tolerated.
func LoadInput(dir, scenario, ts string) (Input, error) {
	in := Input{Scenario: scenario, Timestamp: ts}

	if b, err := os.ReadFile(filepath.Join(dir, "leak_gate.json")); err == nil {
		var lg LeakGate
		if err := json.Unmarshal(b, &lg); err != nil {
			return in, fmt.Errorf("parse leak_gate.json: %w", err)
		}
		in.LeakGate = &lg
	} else if !errors.Is(err, fs.ErrNotExist) {
		return in, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return in, err
	}
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, "ghz_") || !strings.HasSuffix(name, ".json") {
			continue
		}
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return in, err
		}
		var s GhzSummary
		if err := json.Unmarshal(b, &s); err != nil {
			return in, fmt.Errorf("parse %s: %w", name, err)
		}
		in.GhzFiles = append(in.GhzFiles, GhzFile{Name: name, Summary: s})
	}
	sort.Slice(in.GhzFiles, func(i, j int) bool { return in.GhzFiles[i].Name < in.GhzFiles[j].Name })

	if b, err := os.ReadFile(filepath.Join(dir, "prom", "_index.ndjson")); err == nil {
		for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if line == "" {
				continue
			}
			var p PromIndexEntry
			if err := json.Unmarshal([]byte(line), &p); err != nil {
				return in, fmt.Errorf("parse prom index: %w", err)
			}
			in.PromIndex = append(in.PromIndex, p)
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return in, err
	}

	if pprofs, err := os.ReadDir(filepath.Join(dir, "pprof")); err == nil {
		for _, p := range pprofs {
			if !p.IsDir() {
				in.PprofList = append(in.PprofList, p.Name())
			}
		}
		sort.Strings(in.PprofList)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return in, err
	}

	return in, nil
}

// RenderReport writes a Markdown report for `in` to `w`. Output is stable
// for fixed inputs (no timestamps from time.Now, etc.).
func RenderReport(w io.Writer, in Input) error {
	bw := &errWriter{w: w}

	verdict := "(no leak gate captured)"
	if in.LeakGate != nil {
		verdict = in.LeakGate.Verdict
	}
	bw.printf("# Bench report — `%s` @ `%s`\n\n", in.Scenario, in.Timestamp)
	bw.printf("**Leak gate verdict:** `%s`\n\n", verdict)

	bw.printf("## Leak gate\n\n")
	if in.LeakGate == nil {
		bw.printf("_not captured_\n\n")
	} else {
		hMB := in.LeakGate.Thresholds.HeapAllocMaxDeltaMB
		if hMB == 0 {
			hMB = in.LeakGate.Thresholds.HeapInuseMaxDeltaMB
		}
		bw.printf("Thresholds: goroutine_max_delta=%d, heap_alloc_max_delta_mb=%d\n\n",
			in.LeakGate.Thresholds.GoroutineMaxDelta, hMB)
		bw.printf("Gate evaluates against `heap_alloc` (post-GC live bytes); `heap_inuse` and `heap_objects` are shown for context only.\n\n")
		bw.printf("| replica | goroutines (Δ) | heap_alloc MiB (pre → post = Δ) | heap_inuse MiB (pre → post = Δ) | heap_objects (Δ) |\n")
		bw.printf("| --- | --- | --- | --- | --- |\n")
		for _, r := range in.LeakGate.Replicas {
			bw.printf("| `%s` | %d → %d = **%+d** | %.1f → %.1f = **%+.1f** | %.1f → %.1f = **%+.1f** | %d → %d = **%+d** |\n",
				r.Endpoint,
				r.GoroutinesPre, r.GoroutinesPost, r.GoroutineDelta,
				bytesToMiB(r.HeapAllocPreBytes),
				bytesToMiB(r.HeapAllocPostBytes),
				bytesToMiB(r.HeapAllocDeltaBytes),
				bytesToMiB(r.HeapInusePreBytes),
				bytesToMiB(r.HeapInusePostBytes),
				bytesToMiB(r.HeapInuseDeltaBytes),
				r.HeapObjectsPre, r.HeapObjectsPost, r.HeapObjectsDelta,
			)
		}
		bw.printf("\n")
	}

	bw.printf("## ghz runs\n\n")
	if len(in.GhzFiles) == 0 {
		bw.printf("_no ghz artifacts found_\n\n")
	} else {
		bw.printf("| file | count | rps | avg ms | p99 ms | non-OK |\n")
		bw.printf("| --- | ---: | ---: | ---: | ---: | ---: |\n")
		sawStream := false
		for _, g := range in.GhzFiles {
			// ghz reports server-streaming RPCs (file prefix `ghz_sub_`)
			// as a sequence of short-lived call iterations whose terminal
			// status is almost always non-OK (Subscribe never returns OK
			// for a healthy stream — it streams forever). In that mode
			// `non-OK` ≈ `count` and tells us nothing useful, so we mask
			// the column with `—` and surface a footnote instead. See
			// issue #258, item 4.
			isStream := strings.HasPrefix(g.Name, "ghz_sub_")
			nonOKCell := "—"
			if !isStream {
				nonOK := 0
				for code, n := range g.Summary.StatusCodeDistribution {
					if code != "OK" {
						nonOK += n
					}
				}
				nonOKCell = fmt.Sprintf("%d", nonOK)
			} else {
				sawStream = true
			}
			bw.printf("| `%s` | %d | %.1f | %.2f | %.2f | %s |\n",
				g.Name,
				g.Summary.Count,
				g.Summary.Rps,
				nsToMs(g.Summary.Average),
				nsToMs(percentileNs(g.Summary, 99)),
				nonOKCell,
			)
		}
		bw.printf("\n")
		if sawStream {
			bw.printf("> `ghz_sub_*` rows show `—` for non-OK: ghz reports server-streaming\n")
			bw.printf("> RPCs as repeated short-lived iterations, so the column is structurally\n")
			bw.printf("> equal to `count` and not actionable. Inspect `lantern_subscription_*`\n")
			bw.printf("> Prom series or per-stream `count` to assess stream health.\n\n")
		}
	}

	bw.printf("## Prometheus range queries\n\n")
	if len(in.PromIndex) == 0 {
		bw.printf("_no prom queries captured_\n\n")
	} else {
		for _, p := range in.PromIndex {
			bw.printf("- `%s` → `prom/%s`\n", p.Query, p.File)
		}
		bw.printf("\n")
	}

	bw.printf("## pprof profiles\n\n")
	if len(in.PprofList) == 0 {
		bw.printf("_no pprof profiles captured_\n\n")
	} else {
		for _, p := range in.PprofList {
			bw.printf("- `pprof/%s`\n", p)
		}
		bw.printf("\n")
	}

	bw.printf("## Drill-down\n\n")
	bw.printf("1. Compare `Δ` cells in the leak gate. Any cell exceeding its threshold is the first place to look.\n")
	bw.printf("2. For a suspect replica, diff `pprof/<replica>__pre__heap.pb.gz` against `pprof/<replica>__post__heap.pb.gz`:\n")
	bw.printf("   `go tool pprof -http=:0 -base <pre> <post>`\n")
	bw.printf("3. For elevated tail latency, cross-reference the matching Prom histogram query with `<replica>__post__goroutine.pb.gz` (look for blocked stacks).\n")
	return bw.err
}

func percentileNs(s GhzSummary, pct int) int64 {
	for _, d := range s.LatencyDistribution {
		if d.Percentage == pct {
			return d.Latency
		}
	}
	return 0
}

func nsToMs(ns int64) float64    { return float64(ns) / 1e6 }
func bytesToMiB(b int64) float64 { return float64(b) / (1024 * 1024) }

type errWriter struct {
	w   io.Writer
	err error
}

func (e *errWriter) printf(format string, args ...any) {
	if e.err != nil {
		return
	}
	_, e.err = fmt.Fprintf(e.w, format, args...)
}

func main() {
	dir := flag.String("dir", "", "run output directory (required)")
	scenario := flag.String("scenario", "", "scenario name")
	ts := flag.String("timestamp", "", "run timestamp")
	flag.Parse()
	if *dir == "" {
		fmt.Fprintln(os.Stderr, "render: -dir is required")
		os.Exit(2)
	}
	in, err := LoadInput(*dir, *scenario, *ts)
	if err != nil {
		fmt.Fprintln(os.Stderr, "render: load:", err)
		os.Exit(1)
	}
	if err := RenderReport(os.Stdout, in); err != nil {
		fmt.Fprintln(os.Stderr, "render: write:", err)
		os.Exit(1)
	}
}
