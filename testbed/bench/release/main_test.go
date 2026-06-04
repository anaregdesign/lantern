package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestDemoteHeaders(t *testing.T) {
	in := "# Top\n## Sub\n#NotAHeading\ntext\n###### Already deep\n"
	got := demoteHeaders(in)
	want := "### Top\n#### Sub\n#NotAHeading\ntext\n###### Already deep\n"
	if got != want {
		t.Errorf("demoteHeaders mismatch\n got:%q\nwant:%q", got, want)
	}
}

func TestLoadScenario_MissingDir(t *testing.T) {
	s := loadScenario("smoke_write_heavy", "")
	if s.Row.Verdict != "(failed)" {
		t.Errorf("missing dir verdict: %q", s.Row.Verdict)
	}
	if s.Row.Rps != "—" || s.Row.P99ms != "—" || s.Row.NonOK != "—" {
		t.Errorf("missing dir row: %+v", s.Row)
	}
}

func TestLoadScenario_PopulatesRowAndDetail(t *testing.T) {
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "leak_gate.json"), map[string]any{"verdict": "pass"})
	writeJSON(t, filepath.Join(dir, "ghz_steady_localhost_6380.json"), map[string]any{
		"count":                  1000,
		"rps":                    4995.5,
		"statusCodeDistribution": map[string]int{"OK": 990, "DEADLINE_EXCEEDED": 10},
		"latencyDistribution":    []map[string]any{{"percentage": 99, "latency": 1_230_000}},
	})
	if err := os.WriteFile(filepath.Join(dir, "report.md"), []byte("# Inner\n## Section\nbody\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	s := loadScenario("smoke_write_heavy", dir)
	if s.Row.Verdict != "pass" {
		t.Errorf("verdict: %q", s.Row.Verdict)
	}
	if s.Row.Rps != "4995.5" {
		t.Errorf("rps: %q", s.Row.Rps)
	}
	if s.Row.P99ms != "1.23" {
		t.Errorf("p99ms: %q", s.Row.P99ms)
	}
	if s.Row.NonOK != "10" {
		t.Errorf("nonOK: %q", s.Row.NonOK)
	}
	if !strings.Contains(s.Detail, "### Inner") || !strings.Contains(s.Detail, "#### Section") {
		t.Errorf("detail not demoted: %q", s.Detail)
	}
}

func TestLoadScenario_AggregatesMultipleGhzFiles(t *testing.T) {
	// mixed_rw forks two ghz invocations — verify aggregation: sum(rps),
	// max(p99), sum(non-OK).
	dir := t.TempDir()
	writeJSON(t, filepath.Join(dir, "leak_gate.json"), map[string]any{"verdict": "pass"})
	writeJSON(t, filepath.Join(dir, "ghz_steady_put.json"), map[string]any{
		"count":                  1000,
		"rps":                    2000.0,
		"statusCodeDistribution": map[string]int{"OK": 1000},
		"latencyDistribution":    []map[string]any{{"percentage": 99, "latency": 5_000_000}},
	})
	writeJSON(t, filepath.Join(dir, "ghz_steady_get.json"), map[string]any{
		"count":                  1000,
		"rps":                    3000.0,
		"statusCodeDistribution": map[string]int{"OK": 995, "UNAVAILABLE": 5},
		"latencyDistribution":    []map[string]any{{"percentage": 99, "latency": 8_000_000}},
	})

	s := loadScenario("smoke_mixed_rw", dir)
	if s.Row.Rps != "5000.0" {
		t.Errorf("rps aggregate: %q", s.Row.Rps)
	}
	if s.Row.P99ms != "8.00" {
		t.Errorf("p99 aggregate: %q", s.Row.P99ms)
	}
	if s.Row.NonOK != "5" {
		t.Errorf("nonOK aggregate: %q", s.Row.NonOK)
	}
}

func TestRender_FixedFormat(t *testing.T) {
	scenarios := []scenarioInput{
		{
			Name:   "smoke_write_heavy",
			Detail: "### Bench report — `smoke_write_heavy`\nbody\n",
			Row:    summaryRow{Verdict: "pass", Rps: "5000.0", P99ms: "1.23", NonOK: "0"},
		},
		{
			Name: "smoke_read_heavy",
			Row:  summaryRow{Verdict: "(failed)", Rps: "—", P99ms: "—", NonOK: "—"},
		},
	}
	var buf bytes.Buffer
	if err := Render(&buf, Header{Tag: "v9.9.9", Commit: "abc1234", Runner: "linux/amd64", Captured: "20260101T000000Z"}, scenarios); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	for _, want := range []string{
		"# Lantern benchmark — v9.9.9",
		"- Commit: `abc1234`",
		"- Runner: `linux/amd64`",
		"- Captured: `20260101T000000Z`",
		"## Summary",
		"| scenario | leak gate | rps | p99 (ms) | non-OK |",
		"| `smoke_write_heavy` | `pass` | 5000.0 | 1.23 | 0 |",
		"| `smoke_read_heavy` | `(failed)` | — | — | — |",
		"## smoke_write_heavy",
		"### Bench report — `smoke_write_heavy`",
		"## smoke_read_heavy",
		"_no report.md produced for this scenario; see workflow logs._",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%s", want, out)
		}
	}
}

func TestParseScenarioArgs(t *testing.T) {
	tmp := t.TempDir()
	writeJSON(t, filepath.Join(tmp, "leak_gate.json"), map[string]any{"verdict": "pass"})

	got, err := parseScenarioArgs([]string{"a=" + tmp, "b="})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Name != "a" || got[1].Name != "b" {
		t.Errorf("got: %+v", got)
	}
	if got[0].Row.Verdict != "pass" {
		t.Errorf("a verdict: %q", got[0].Row.Verdict)
	}
	if got[1].Row.Verdict != "(failed)" {
		t.Errorf("b verdict: %q", got[1].Row.Verdict)
	}

	if _, err := parseScenarioArgs([]string{"no-equals"}); err == nil {
		t.Errorf("expected error on missing =")
	}
	if _, err := parseScenarioArgs([]string{"x=" + tmp, "x="}); err == nil {
		t.Errorf("expected error on duplicate name")
	}
}
