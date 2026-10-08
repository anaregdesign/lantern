package main

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
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

func TestCaptureSearchCountersExposition(t *testing.T) {
	registry := prometheus.NewRegistry()
	counter := prometheus.NewCounterVec(prometheus.CounterOpts{Name: searchCallsMetric, Help: "bounded calls"}, []string{"reason"})
	registry.MustRegister(counter)
	const escaped = "東京\"\\\n"
	counter.WithLabelValues(escaped).Add(3)
	counter.WithLabelValues("index_incomplete").Add(7)
	server := httptest.NewServer(promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	defer server.Close()
	snapshot, err := captureSearchCounters(context.Background(), server.Client(), []string{server.URL, server.URL})
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshot.Replicas) != 2 {
		t.Fatalf("replicas = %v", snapshot.Replicas)
	}
	for index, replica := range snapshot.Replicas {
		if replica.Index != index || replica.Endpoint != server.URL || len(replica.Series) != 2 || replica.Series[0].Labels["reason"] != "index_incomplete" || replica.Series[0].Value != 7 || replica.Series[1].Labels["reason"] != escaped || replica.Series[1].Value != 3 {
			t.Fatalf("escaped labels, values or canonical order lost: %+v", replica)
		}
	}
}

func TestCaptureSearchCountersRejectsInvalidScrapes(t *testing.T) {
	for _, tc := range []struct {
		name, body, want string
		status           int
	}{
		{"HTTP failure", "", "HTTP 503", http.StatusServiceUnavailable},
		{"malformed text", "not valid metrics!\n", "parse replica 0", http.StatusOK},
		{"missing counter", "other_metric 1\n", "missing counter", http.StatusOK},
		{"wrong type", "# TYPE lantern_search_calls_total gauge\nlantern_search_calls_total 1\n", "missing counter", http.StatusOK},
		{"negative", "# TYPE lantern_search_calls_total counter\nlantern_search_calls_total -1\n", "invalid", http.StatusOK},
		{"NaN", "# TYPE lantern_search_calls_total counter\nlantern_search_calls_total NaN\n", "invalid", http.StatusOK},
		{"infinite", "# TYPE lantern_search_calls_total counter\nlantern_search_calls_total +Inf\n", "invalid", http.StatusOK},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(tc.status)
				_, _ = fmt.Fprint(w, tc.body)
			}))
			defer server.Close()
			_, err := captureSearchCounters(context.Background(), server.Client(), []string{server.URL})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("scrape error = %v, want %q", err, tc.want)
			}
		})
	}
	t.Run("empty endpoints", func(t *testing.T) {
		if _, err := captureSearchCounters(context.Background(), http.DefaultClient, nil); err == nil {
			t.Fatal("empty scrape accepted")
		}
	})
	t.Run("cancelled request", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if _, err := captureSearchCounters(ctx, http.DefaultClient, []string{"http://127.0.0.1:1"}); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled scrape = %v", err)
		}
	})
}
