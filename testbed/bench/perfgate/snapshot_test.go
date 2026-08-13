package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCaptureSearchCounters(t *testing.T) {
	metrics := `# HELP lantern_search_calls_total bounded calls
# TYPE lantern_search_calls_total counter
lantern_search_calls_total{fuzziness="0",mode="server",outcome="failed_precondition",phrase="no",prefix_present="no",prefix_terms="no",reason="index_incomplete"} 12
`
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, metrics)
	}))
	defer server.Close()

	snapshot, err := captureSearchCounters(context.Background(), server.Client(), []string{server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Metric != searchCallsMetric || len(snapshot.Replicas) != 1 || len(snapshot.Replicas[0].Series) != 1 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	series := snapshot.Replicas[0].Series[0]
	if series.Value != 12 || series.Labels["reason"] != indexIncompleteMetricLabel {
		t.Fatalf("series = %+v", series)
	}
}
