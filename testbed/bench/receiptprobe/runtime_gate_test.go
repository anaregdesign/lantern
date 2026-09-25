package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const testHeapBaseline = int64(100 << 20)

func TestParseRuntimeMetricsEndpoints(t *testing.T) {
	t.Parallel()
	valid := "http://localhost:9390/metrics,http://localhost:9391/metrics,http://localhost:9392/metrics"
	endpoints, err := parseRuntimeMetricsEndpoints(valid)
	if err != nil || len(endpoints) != 3 || endpoints[2].endpoint != "localhost:9392" {
		t.Fatalf("parseRuntimeMetricsEndpoints() = %+v, %v", endpoints, err)
	}
	for _, raw := range []string{
		"http://localhost:9390/metrics",
		"http://localhost:9390/metrics,http://localhost:9390/metrics,http://localhost:9392/metrics",
		"http://localhost:9390/metrics,http://localhost:9391/metrics,https://localhost:9392/metrics",
		"http://localhost:9390/metrics,http://localhost:9391/metrics,http://localhost:9392/other",
	} {
		if _, err := parseRuntimeMetricsEndpoints(raw); err == nil {
			t.Errorf("accepted incomplete or invalid endpoints %q", raw)
		}
	}
}

func TestParseRuntimeMetricsRejectsMissingAndInvalidReadings(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name string
		body string
		ok   bool
	}{
		{"scientific", "# HELP go_goroutines Go\n" +
			"go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 1.048576e+08\n", true},
		{"missing", "go_goroutines 40\n", false},
		{"labeled-not-scalar", "go_goroutines{replica=\"0\"} 40\n" +
			"go_memstats_heap_alloc_bytes 104857600\n", false},
		{"duplicate", "go_goroutines 40\ngo_goroutines 41\n" +
			"go_memstats_heap_alloc_bytes 104857600\n", false},
		{"nan", "go_goroutines NaN\ngo_memstats_heap_alloc_bytes 104857600\n", false},
		{"infinity", "go_goroutines +Inf\ngo_memstats_heap_alloc_bytes 104857600\n", false},
		{"fraction", "go_goroutines 40.5\ngo_memstats_heap_alloc_bytes 104857600\n", false},
		{"zero", "go_goroutines 0\ngo_memstats_heap_alloc_bytes 104857600\n", false},
		{"extra-field", "go_goroutines 40 1000\ngo_memstats_heap_alloc_bytes 104857600\n", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := parseRuntimeMetrics(strings.NewReader(test.body))
			if test.ok && (err != nil || got.Goroutines != 40 || got.HeapAllocBytes != testHeapBaseline) {
				t.Fatalf("parseRuntimeMetrics() = %+v, %v", got, err)
			}
			if !test.ok && err == nil {
				t.Fatalf("parseRuntimeMetrics() accepted %q", test.body)
			}
		})
	}
}

func TestSampleSteadyMetricsScrapesAllThreeReplicas(t *testing.T) {
	t.Parallel()
	var servers []*httptest.Server
	var urls []string
	stop := make(chan struct{})
	var finalReplicaScrapes atomic.Int32
	for i := range receiptReplicaCount {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte("go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n"))
			if i == receiptReplicaCount-1 && finalReplicaScrapes.Add(1) == 2 {
				close(stop)
			}
		}))
		servers = append(servers, server)
		urls = append(urls, server.URL+"/metrics")
	}
	defer func() {
		for _, server := range servers {
			server.Close()
		}
	}()
	endpoints, err := parseRuntimeMetricsEndpoints(strings.Join(urls, ","))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan steadyMetricsReport, 1)
	go func() {
		result <- sampleSteadyMetrics(ctx, steadyMetricsConfig{
			endpoints: endpoints,
			interval:  10 * time.Millisecond,
		}, time.Now(), stop)
	}()
	var report steadyMetricsReport
	select {
	case report = <-result:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if report.Failure != "" || len(report.Samples) < 2 {
		t.Fatalf("steady samples = %+v", report)
	}
	for i, round := range report.Samples {
		if len(round.Replicas) != receiptReplicaCount {
			t.Fatalf("round %d has %d replicas", i, len(round.Replicas))
		}
		for j, value := range round.Replicas {
			if value.Endpoint != endpoints[j].endpoint || value.Goroutines != 40 || value.HeapAllocBytes != testHeapBaseline {
				t.Fatalf("round %d replica %d = %+v", i, j, value)
			}
		}
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusServiceUnavailable)
	}))
	defer failed.Close()
	if _, err := scrapeRuntimeMetrics(ctx, http.DefaultClient, metricsEndpoint{url: failed.URL + "/metrics", endpoint: "failed"}); err == nil {
		t.Fatal("unavailable replica returned a successful metrics reading")
	}
}

func TestEvaluateReceiptLeakGatesTransientAndRetainedGrowth(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		peakG      int64
		peakHeapMB int64
		postG      int64
		postHeapMB int64
		wantPass   bool
		wantDelta  int64
	}{
		{"at both steady bounds", 15, 32, 0, 0, true, 15},
		{"goroutine spike recovers", 16, 0, 0, 0, false, 16},
		{"heap spike recovers", 0, 33, 0, 0, false, 0},
		{"post-GC growth persists", 0, 0, 16, 33, false, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			prePath, postPath, steadyPath, samples := receiptLeakFixture(t)
			samples.Samples[4].Replicas[1].Goroutines += test.peakG
			samples.Samples[4].Replicas[2].HeapAllocBytes += test.peakHeapMB << 20
			if err := writeReceiptArtifact(steadyPath, samples); err != nil {
				t.Fatal(err)
			}
			post := testRuntimeSnapshots()
			*post[1].Goroutines += test.postG
			*post[2].HeapAllocBytes += test.postHeapMB << 20
			if err := writeReceiptArtifact(postPath, post); err != nil {
				t.Fatal(err)
			}
			got := evaluateReceiptLeak(prePath, postPath, steadyPath, 45*time.Second, 5*time.Second, 15, 32)
			if (got.Verdict == "pass") != test.wantPass || len(got.Replicas) != receiptReplicaCount {
				t.Fatalf("leak verdict/replicas = %q/%d; failures = %v", got.Verdict, len(got.Replicas), got.Failures)
			}
			if got.Replicas[1].GoroutinePeakDelta != test.wantDelta {
				t.Errorf("steady goroutine peak delta = %d, want %d", got.Replicas[1].GoroutinePeakDelta, test.wantDelta)
			}
			if got.Replicas[1].GoroutineDelta != test.postG || got.Replicas[2].HeapAllocDeltaBytes != test.postHeapMB<<20 {
				t.Errorf("post-GC deltas = %+v", got.Replicas)
			}
			if got.Replicas[2].HeapAllocPeakDelta != test.peakHeapMB<<20 {
				t.Errorf("steady heap peak delta = %d, want %d MiB", got.Replicas[2].HeapAllocPeakDelta, test.peakHeapMB)
			}
		})
	}
}

func TestEvaluateReceiptLeakRejectsIncompleteEvidence(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		damage func(t *testing.T, pre, post, steady string, samples *steadyMetricsReport)
	}{
		{"missing round replica", func(t *testing.T, _, _, steady string, samples *steadyMetricsReport) {
			samples.Samples[4].Replicas = samples.Samples[4].Replicas[:2]
			mustWriteReceiptArtifact(t, steady, samples)
		}},
		{"zero metric", func(t *testing.T, _, _, steady string, samples *steadyMetricsReport) {
			samples.Samples[4].Replicas[1].HeapAllocBytes = 0
			mustWriteReceiptArtifact(t, steady, samples)
		}},
		{"late gap", func(t *testing.T, _, _, steady string, samples *steadyMetricsReport) {
			samples.Samples[6].ElapsedMS = 33_000
			mustWriteReceiptArtifact(t, steady, samples)
		}},
		{"failed sampler", func(t *testing.T, _, _, steady string, samples *steadyMetricsReport) {
			samples.Failure = "scrape replica 2: HTTP 503"
			mustWriteReceiptArtifact(t, steady, samples)
		}},
		{"missing steady file", func(t *testing.T, _, _, steady string, _ *steadyMetricsReport) {
			if err := os.Remove(steady); err != nil {
				t.Fatal(err)
			}
		}},
		{"missing pre metric", func(t *testing.T, pre, _, _ string, _ *steadyMetricsReport) {
			values := testRuntimeSnapshots()
			values[1].Goroutines = nil
			mustWriteReceiptArtifact(t, pre, values)
		}},
		{"non-finite post metric", func(t *testing.T, _, post, _ string, _ *steadyMetricsReport) {
			if err := os.WriteFile(post, []byte(`[{"endpoint":"localhost:9390","goroutines":NaN}]`), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{"post endpoint drift", func(t *testing.T, _, post, _ string, _ *steadyMetricsReport) {
			values := testRuntimeSnapshots()
			values[1].Endpoint = "localhost:9999"
			mustWriteReceiptArtifact(t, post, values)
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			pre, post, steady, samples := receiptLeakFixture(t)
			test.damage(t, pre, post, steady, &samples)
			got := evaluateReceiptLeak(pre, post, steady, 45*time.Second, 5*time.Second, 15, 32)
			if got.Verdict != "fail" || len(got.Failures) == 0 {
				t.Fatalf("incomplete evidence passed: %+v", got)
			}
		})
	}
}

func TestRunReceiptLeakEvaluationWritesFailureArtifact(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	out := filepath.Join(dir, "leak_gate.json")
	exitCode := runReceiptLeakEvaluation([]string{
		"-pre", filepath.Join(dir, "missing-pre.json"),
		"-post", filepath.Join(dir, "missing-post.json"),
		"-steady", filepath.Join(dir, "missing-steady.json"),
		"-duration", "45s", "-interval", "5s",
		"-max-goroutines", "15", "-max-heap-mb", "32", "-out", out,
	})
	if exitCode != 1 {
		t.Fatalf("exit = %d, want 1", exitCode)
	}
	content, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report receiptLeakReport
	if err := json.Unmarshal(content, &report); err != nil || report.Verdict != "fail" || len(report.Failures) == 0 {
		t.Fatalf("failure artifact = %+v, %v", report, err)
	}
}

func receiptLeakFixture(t *testing.T) (string, string, string, steadyMetricsReport) {
	t.Helper()
	dir := t.TempDir()
	pre := filepath.Join(dir, "pre.json")
	post := filepath.Join(dir, "post.json")
	steady := filepath.Join(dir, "steady.json")
	mustWriteReceiptArtifact(t, pre, testRuntimeSnapshots())
	mustWriteReceiptArtifact(t, post, testRuntimeSnapshots())

	report := steadyMetricsReport{IntervalMS: 5_000, DurationMS: 45_000}
	for _, replica := range testRuntimeSnapshots() {
		report.Endpoints = append(report.Endpoints, replica.Endpoint)
	}
	for i := range 9 {
		round := metricsRound{ElapsedMS: int64(i) * 5_000}
		for _, replica := range testRuntimeSnapshots() {
			round.Replicas = append(round.Replicas, runtimeMetrics{
				Endpoint: replica.Endpoint, Goroutines: *replica.Goroutines, HeapAllocBytes: *replica.HeapAllocBytes,
			})
		}
		report.Samples = append(report.Samples, round)
	}
	mustWriteReceiptArtifact(t, steady, report)
	return pre, post, steady, report
}

func testRuntimeSnapshots() []runtimeSnapshot {
	return []runtimeSnapshot{
		{Endpoint: "localhost:9390", Goroutines: int64Pointer(40), HeapAllocBytes: int64Pointer(testHeapBaseline)},
		{Endpoint: "localhost:9391", Goroutines: int64Pointer(40), HeapAllocBytes: int64Pointer(testHeapBaseline)},
		{Endpoint: "localhost:9392", Goroutines: int64Pointer(40), HeapAllocBytes: int64Pointer(testHeapBaseline)},
	}
}

func int64Pointer(value int64) *int64 { return &value }

func mustWriteReceiptArtifact(t *testing.T, path string, value any) {
	t.Helper()
	if err := writeReceiptArtifact(path, value); err != nil {
		t.Fatal(err)
	}
}
