package main

import (
	"strings"
	"testing"
)

func ptr(v float64) *float64 { return &v }

func TestEvaluateMetricGate(t *testing.T) {
	t.Run("baseline tolerances pass", func(t *testing.T) {
		pre := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"docs": ptr(100), "healthy": ptr(1)}}}
		post := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"docs": ptr(105), "healthy": ptr(1)}}}
		rep := evaluate(map[string]threshold{
			"docs":    {MaxIncrease: ptr(10), MaxRatio: ptr(1.1)},
			"healthy": {MinPost: ptr(1), MaxPost: ptr(1)},
		}, pre, post)
		if rep.Verdict != "pass" {
			t.Fatalf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
		}
	})

	t.Run("broken cleanup fails", func(t *testing.T) {
		pre := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"docs": ptr(100)}}}
		post := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"docs": ptr(1000)}}}
		rep := evaluate(map[string]threshold{"docs": {MaxIncrease: ptr(20), MaxRatio: ptr(1.5)}}, pre, post)
		if rep.Verdict != "fail" || len(rep.Results) != 1 {
			t.Fatalf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
		}
		failures := strings.Join(rep.Results[0].Failures, " ")
		if !strings.Contains(failures, "increase") || !strings.Contains(failures, "ratio") {
			t.Fatalf("failures = %q", failures)
		}
	})

	t.Run("absolute retained ceiling is stable from a tiny baseline", func(t *testing.T) {
		pre := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"retained": ptr(13)}}}
		post := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"retained": ptr(801)}}}
		rep := evaluate(map[string]threshold{
			"retained": {MaxIncrease: ptr(100000), MaxPost: ptr(4096)},
		}, pre, post)
		if rep.Verdict != "pass" {
			t.Fatalf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
		}
	})

	for _, tc := range []struct {
		name    string
		ceiling float64
	}{
		{name: "retained bytes", ceiling: 262144},
		{name: "retained ordinals", ceiling: 4096},
		{name: "retained term slots", ceiling: 8192},
	} {
		t.Run(tc.name+" exceeds absolute ceiling", func(t *testing.T) {
			pre := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"retained": ptr(0)}}}
			post := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"retained": ptr(tc.ceiling + 1)}}}
			rep := evaluate(map[string]threshold{
				"retained": {MaxIncrease: ptr(tc.ceiling * 100), MaxPost: ptr(tc.ceiling)},
			}, pre, post)
			if rep.Verdict != "fail" || len(rep.Results) != 1 {
				t.Fatalf("verdict = %s, results = %+v", rep.Verdict, rep.Results)
			}
			if failures := strings.Join(rep.Results[0].Failures, " "); !strings.Contains(failures, "post") || !strings.Contains(failures, "exceeds") {
				t.Fatalf("failures = %q", failures)
			}
		})
	}

	t.Run("missing sample fails closed", func(t *testing.T) {
		pre := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"docs": nil}}}
		post := []snapshotReplica{{Endpoint: "r0", Metrics: map[string]*float64{"docs": ptr(0)}}}
		rep := evaluate(map[string]threshold{"docs": {MaxIncrease: ptr(0)}}, pre, post)
		if rep.Verdict != "fail" || !strings.Contains(strings.Join(rep.Results[0].Failures, " "), "missing") {
			t.Fatalf("report = %+v", rep)
		}
	})
}

func TestValidateThreshold(t *testing.T) {
	if err := validateThreshold(threshold{}); err == nil {
		t.Fatal("empty threshold should fail")
	}
	if err := validateThreshold(threshold{MaxRatio: ptr(0.5)}); err == nil {
		t.Fatal("ratio below one should fail")
	}
	if err := validateThreshold(threshold{MinPost: ptr(2), MaxPost: ptr(1)}); err == nil {
		t.Fatal("inverted post range should fail")
	}
}
