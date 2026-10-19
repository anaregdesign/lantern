package metrics

import (
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
)

func TestPublicationMeasurementsBoundLabelsAndDistinguishAttemptFromCommit(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := New(reg, Options{})
	m.BindPublicationWaiters(func(category string) int64 {
		if category == "remote_apply" {
			return 3
		}
		return 0
	})
	m.OnPublicationGate("remote_apply", 2*time.Millisecond, 3*time.Millisecond, time.Millisecond)
	m.OnPublicationGate("untrusted-key", 0, 0, 0)
	m.OnPublicationRead(4, "consistent", time.Millisecond)
	m.OnPublicationRead(999, "untrusted-result", 0)
	m.OnPublicationApply("new_sequence", "committed", time.Millisecond, true, 2, 0)
	m.OnPublicationApply("unclassified", "rejected", time.Millisecond, false, 0, 0)
	m.OnPublicationApply("committed_duplicate", "committed_duplicate", time.Millisecond, false, 0, 0)
	families, err := reg.Gather()
	if err != nil {
		t.Fatal(err)
	}
	seen := map[string]bool{}
	for _, family := range families {
		for _, metric := range family.GetMetric() {
			labels := map[string]string{}
			for _, label := range metric.GetLabel() {
				if label.GetValue() == "untrusted-key" || label.GetValue() == "untrusted-result" || label.GetValue() == "999" {
					t.Fatalf("unbounded label escaped: %v", metric)
				}
				labels[label.GetName()] = label.GetValue()
			}
			switch family.GetName() {
			case "lantern_publication_gate_waiters":
				if labels["category"] == "remote_apply" {
					seen["current_waiters"] = metric.GetGauge().GetValue() == 3
				}
			case "lantern_publication_gate_seconds":
				if labels["category"] == "remote_apply" && labels["phase"] == "wait" {
					if metric.GetHistogram().GetSampleCount() != 1 || metric.GetHistogram().GetSampleSum() != .002 {
						t.Fatalf("wait measurement = %v", metric)
					}
					seen["wait"] = true
				}
			case "lantern_replication_apply_attempts_total":
				if labels["stage"] == "read_only" && labels["outcome"] == "committed_duplicate" {
					seen["read_only_duplicate"] = metric.GetCounter().GetValue() == 1
				}
				if labels["stage"] == "preflight" && labels["outcome"] == "rejected" {
					seen["rejected_before_gate"] = metric.GetCounter().GetValue() == 1
				}
			case "lantern_replication_apply_committed_per_acquisition":
				if metric.GetHistogram().GetSampleCount() != 1 || metric.GetHistogram().GetSampleSum() != 2 {
					t.Fatalf("preflight rejection counted as acquisition: %v", metric)
				}
				seen["committed"] = true
			}
		}
	}
	if !seen["wait"] || !seen["rejected_before_gate"] || !seen["committed"] || !seen["current_waiters"] || !seen["read_only_duplicate"] {
		t.Fatalf("missing observations: %v", seen)
	}
}
