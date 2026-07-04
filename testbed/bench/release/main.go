// Package main is the release-time bench aggregator.
//
// It consumes the per-scenario artifacts produced by testbed/bench/run.sh
// (one run directory per scenario, each containing leak_gate.json,
// ghz_*.json, and a pre-rendered report.md) and emits a single fixed-format
// Markdown document suitable for appending to a GitHub Release body.
//
// The output schema is intentionally stable across releases so that
// readers can diff bench numbers tag-over-tag without parsing pain:
//
//	# Lantern benchmark — <tag>
//	- Commit: <sha>
//	- Runner: <runner>
//	- Captured: <UTC timestamp>
//
//	## Summary
//	| scenario | leak gate | rps | p99 (ms) | non-OK |
//	| ... |
//
//	## <scenario>
//	<per-scenario report.md, with H1 demoted to H3 so it nests cleanly>
//
// Invocation:
//
//	go run ./testbed/bench/release \
//	    -tag v1.2.3 -commit abc1234 -runner linux/amd64 \
//	    -captured 20260604T120000Z \
//	    -out bench-report.md \
//	    smoke_write_heavy=path/to/out/smoke_write_heavy/<ts> \
//	    smoke_read_heavy=path/to/out/smoke_read_heavy/<ts> \
//	    ...
//
// Missing per-scenario artifacts do NOT abort the run — the scenario is
// rendered as a `(failed)` row in the summary so a single noisy scenario
// does not prevent the rest of the report from shipping. The driver script
// (release.sh) decides whether a fail row should fail the CI job.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// leakGate mirrors the subset of testbed/bench/report.LeakGate that the
// aggregator needs. We re-declare it here (rather than importing the
// renderer) because the renderer is package main and Go forbids importing
// `main`. Keep the JSON tags in sync.
type leakGate struct {
	Verdict string `json:"verdict"`
}

// perfGate mirrors the subset of testbed/bench/report.PerfGate the summary
// table needs (run.sh only writes perf_gate.json when the scenario declares
// a `perf_gate:` block — absent file renders as "—").
type perfGate struct {
	Verdict string `json:"verdict"`
}

// ghzSummary mirrors the subset of testbed/bench/report.GhzSummary that
// the summary table needs.
type ghzSummary struct {
	Count                  uint64         `json:"count"`
	Rps                    float64        `json:"rps"`
	StatusCodeDistribution map[string]int `json:"statusCodeDistribution"`
	LatencyDistribution    []struct {
		Percentage int   `json:"percentage"`
		Latency    int64 `json:"latency"`
	} `json:"latencyDistribution"`
}

// scenarioInput is one row in the release report.
type scenarioInput struct {
	Name   string
	Dir    string // run directory; empty if the scenario was not produced
	Detail string // pre-rendered per-scenario report.md, with headers demoted
	Row    summaryRow
}

// summaryRow is the rendered values for one row in the Summary table.
type summaryRow struct {
	Verdict     string // "pass", "fail", or "(failed)" if artifacts are missing
	PerfVerdict string // "pass", "fail", or "—" when the scenario gates no perf metric
	Rps         string // pre-formatted, e.g. "5000.0" or "—"
	P99ms       string // pre-formatted, e.g. "0.89" or "—"
	NonOK       string // pre-formatted, e.g. "0" or "—"
}

// Header carries the fixed-format report header fields.
type Header struct {
	Tag      string
	Commit   string
	Runner   string
	Captured string
}

func loadScenario(name, dir string) scenarioInput {
	in := scenarioInput{Name: name, Dir: dir, Row: summaryRow{Verdict: "(failed)", PerfVerdict: "—", Rps: "—", P99ms: "—", NonOK: "—"}}
	if dir == "" {
		return in
	}

	// Leak-gate verdict.
	if b, err := os.ReadFile(filepath.Join(dir, "leak_gate.json")); err == nil {
		var lg leakGate
		if err := json.Unmarshal(b, &lg); err == nil && lg.Verdict != "" {
			in.Row.Verdict = lg.Verdict
		}
	}

	// Perf-gate verdict (optional artifact — see run.sh).
	if b, err := os.ReadFile(filepath.Join(dir, "perf_gate.json")); err == nil {
		var pg perfGate
		if err := json.Unmarshal(b, &pg); err == nil && pg.Verdict != "" {
			in.Row.PerfVerdict = pg.Verdict
		}
	}

	// Steady-phase ghz: pick the file whose basename starts with `ghz_steady_`.
	// If multiple exist (e.g. mixed_rw forks per-call), aggregate them:
	// rps = sum(rps), p99 = max(p99), non-OK = sum, count = sum.
	if entries, err := os.ReadDir(dir); err == nil {
		var sums []ghzSummary
		for _, e := range entries {
			n := e.Name()
			if e.IsDir() || !strings.HasPrefix(n, "ghz_steady_") || !strings.HasSuffix(n, ".json") {
				continue
			}
			b, err := os.ReadFile(filepath.Join(dir, n))
			if err != nil {
				continue
			}
			var s ghzSummary
			if err := json.Unmarshal(b, &s); err == nil {
				sums = append(sums, s)
			}
		}
		if len(sums) > 0 {
			var totalRps float64
			var maxP99 int64
			var nonOK int
			for _, s := range sums {
				totalRps += s.Rps
				if p := percentileNs(s, 99); p > maxP99 {
					maxP99 = p
				}
				for code, n := range s.StatusCodeDistribution {
					if code != "OK" {
						nonOK += n
					}
				}
			}
			in.Row.Rps = fmt.Sprintf("%.1f", totalRps)
			in.Row.P99ms = fmt.Sprintf("%.2f", float64(maxP99)/1e6)
			in.Row.NonOK = fmt.Sprintf("%d", nonOK)
		}
	}

	// Detail section: re-use the renderer's output if present.
	if b, err := os.ReadFile(filepath.Join(dir, "report.md")); err == nil {
		in.Detail = demoteHeaders(string(b))
	}

	return in
}

func percentileNs(s ghzSummary, pct int) int64 {
	for _, d := range s.LatencyDistribution {
		if d.Percentage == pct {
			return d.Latency
		}
	}
	return 0
}

// demoteHeaders shifts every Markdown ATX heading down by two levels so
// the per-scenario report nests under `## <scenario>` without colliding
// with the top-level structure. `# X` becomes `### X`, `## X` becomes
// `#### X`, etc. Capped at H6 so we stay within CommonMark.
func demoteHeaders(s string) string {
	lines := strings.Split(s, "\n")
	for i, ln := range lines {
		j := 0
		for j < len(ln) && ln[j] == '#' {
			j++
		}
		if j == 0 || j >= len(ln) || ln[j] != ' ' {
			continue
		}
		newLevel := j + 2
		if newLevel > 6 {
			newLevel = 6
		}
		lines[i] = strings.Repeat("#", newLevel) + ln[j:]
	}
	return strings.Join(lines, "\n")
}

// Render writes the aggregated report to w. Output is stable for fixed
// inputs (no time.Now, no os.Hostname) so it round-trips under tests.
func Render(w io.Writer, h Header, scenarios []scenarioInput) error {
	bw := &errWriter{w: w}
	bw.printf("# Lantern benchmark — %s\n", h.Tag)
	bw.printf("- Commit: `%s`\n", h.Commit)
	bw.printf("- Runner: `%s`\n", h.Runner)
	bw.printf("- Captured: `%s`\n\n", h.Captured)

	bw.printf("## Summary\n\n")
	bw.printf("| scenario | leak gate | perf gate | rps | p99 (ms) | non-OK |\n")
	bw.printf("| --- | --- | --- | ---: | ---: | ---: |\n")
	for _, s := range scenarios {
		bw.printf("| `%s` | `%s` | `%s` | %s | %s | %s |\n",
			s.Name, s.Row.Verdict, s.Row.PerfVerdict, s.Row.Rps, s.Row.P99ms, s.Row.NonOK)
	}
	bw.printf("\n")

	for _, s := range scenarios {
		bw.printf("## %s\n\n", s.Name)
		if s.Detail == "" {
			bw.printf("_no report.md produced for this scenario; see workflow logs._\n\n")
			continue
		}
		bw.printf("%s\n", strings.TrimRight(s.Detail, "\n"))
		bw.printf("\n")
	}
	return bw.err
}

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

// parseScenarioArgs accepts repeated `name=dir` arguments and returns
// scenarios in the order given (CI cares about ordering so the table is
// reproducible). An empty `dir` indicates the scenario did not produce
// artifacts (CI passes `name=` for those).
func parseScenarioArgs(args []string) ([]scenarioInput, error) {
	out := make([]scenarioInput, 0, len(args))
	seen := map[string]bool{}
	for _, a := range args {
		idx := strings.IndexByte(a, '=')
		if idx <= 0 {
			return nil, fmt.Errorf("scenario arg %q must be name=dir", a)
		}
		name, dir := a[:idx], a[idx+1:]
		if seen[name] {
			return nil, fmt.Errorf("duplicate scenario: %s", name)
		}
		seen[name] = true
		out = append(out, loadScenario(name, dir))
	}
	return out, nil
}

func main() {
	tag := flag.String("tag", "", "release tag, e.g. v1.2.3 (required)")
	commit := flag.String("commit", "", "release commit SHA (required)")
	runner := flag.String("runner", "", "runner platform, e.g. linux/amd64 (required)")
	captured := flag.String("captured", "", "report capture timestamp (UTC, e.g. 20260604T120000Z) (required)")
	out := flag.String("out", "", "output bench-report.md path (required)")
	flag.Parse()

	for k, v := range map[string]string{"tag": *tag, "commit": *commit, "runner": *runner, "captured": *captured, "out": *out} {
		if v == "" {
			fmt.Fprintf(os.Stderr, "release: -%s is required\n", k)
			os.Exit(2)
		}
	}

	scenarios, err := parseScenarioArgs(flag.Args())
	if err != nil {
		fmt.Fprintln(os.Stderr, "release:", err)
		os.Exit(2)
	}

	// Stable ordering is determined by CLI arg order, but make sure the
	// failed-scenario rows still group last when callers pass them mixed
	// in — keeps the summary readable.
	sort.SliceStable(scenarios, func(i, j int) bool {
		ai := scenarios[i].Row.Verdict == "(failed)"
		aj := scenarios[j].Row.Verdict == "(failed)"
		if ai != aj {
			return !ai
		}
		return false
	})

	f, err := os.Create(*out)
	if err != nil {
		fmt.Fprintln(os.Stderr, "release: create:", err)
		os.Exit(1)
	}

	h := Header{Tag: *tag, Commit: *commit, Runner: *runner, Captured: *captured}
	if err := Render(f, h, scenarios); err != nil {
		_ = f.Close()
		fmt.Fprintln(os.Stderr, "release: render:", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		fmt.Fprintln(os.Stderr, "release: close:", err)
		os.Exit(1)
	}
}
