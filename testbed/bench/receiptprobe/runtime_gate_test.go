package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"
)

const testHeapBaseline = int64(100 << 20)

const testRuntimeObservations = "go_gc_duration_seconds_count 7\n" +
	"go_memstats_last_gc_time_seconds 1720000000.125\n" +
	"go_memstats_next_gc_bytes 209715200\n" +
	"lantern_receipt_entries 8\n" +
	"lantern_receipt_bytes 2048\n" +
	"lantern_receipt_oldest_deadline_seconds 1720003600.5\n" +
	"lantern_mutation_log_capacity 10000\n" +
	"lantern_mutation_log_fill_ratio 0.25\n" +
	"lantern_mutation_log_evicted_total 0\n"

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
		{"scientific-integer-goroutines", "go_goroutines 4e+01\n" +
			"go_memstats_heap_alloc_bytes 104857600\n", true},
		{"rounded-fractional-heap", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 104857600.000000001\n", false},
		{"rounded-fractional-scientific", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 1.04857600000000001e+08\n", false},
		{"rounded-fractional-goroutines", "go_goroutines 40.0000000000000001\n" +
			"go_memstats_heap_alloc_bytes 104857600\n", false},
		{"subunit-fraction", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 0.1\n", false},
		{"subunit-scientific", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 1e-40\n", false},
		{"tiny-exponent-underflow", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 1e-9999\n", false},
		{"huge-exponent-overflow", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 1e+9999\n", false},
		{"int64-overflow", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 9223372036854775808\n", false},
		{"malformed-exponent", "go_goroutines 40\n" +
			"go_memstats_heap_alloc_bytes 1e-\n", false},
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
			got, err := parseRuntimeMetrics(strings.NewReader(test.body + testRuntimeObservations))
			if test.ok && (err != nil || got.Goroutines != 40 || got.HeapAllocBytes != testHeapBaseline) {
				t.Fatalf("parseRuntimeMetrics() = %+v, %v", got, err)
			}
			if !test.ok && err == nil {
				t.Fatalf("parseRuntimeMetrics() accepted %q", test.body)
			}
		})
	}
}

func TestParseMetricValuePreservesExactIntegerAboveFloatPrecision(t *testing.T) {
	t.Parallel()
	const raw = "9007199254740993"
	value, err := parseMetricValue(raw)
	if err != nil || value != 9007199254740993 {
		t.Fatalf("parseMetricValue(%q) = %d, %v; want exact integer", raw, value, err)
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
			_, _ = w.Write([]byte("go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n" + testRuntimeObservations))
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
			if value.Metrics == nil || value.Metrics.GCCount != 7 || value.Metrics.LastGCTimeSeconds != 1720000000.125 ||
				value.Metrics.ReceiptEntries != 8 || value.Metrics.MutationLogFillRatio != 0.25 {
				t.Fatalf("round %d replica %d observations = %+v", i, j, value.Metrics)
			}
		}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	var saved steadyMetricsReport
	if err := json.Unmarshal(raw, &saved); err != nil || saved.Samples[0].Replicas[0].Metrics == nil ||
		saved.Samples[0].Replicas[0].Metrics.ReceiptOldestDeadline != 1720003600.5 {
		t.Fatalf("saved steady observations = %s, %v", raw, err)
	}
	failed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "failed", http.StatusServiceUnavailable)
	}))
	defer failed.Close()
	if _, err := scrapeRuntimeMetrics(ctx, http.DefaultClient, metricsEndpoint{url: failed.URL + "/metrics", endpoint: "failed"}); err == nil {
		t.Fatal("unavailable replica returned a successful metrics reading")
	}
}

func TestSampleSteadyMetricsHTTPDeadlineAndConnections(t *testing.T) {
	t.Run("per request deadline with fresh and reused connections", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		stop := make(chan struct{})
		var servers []*httptest.Server
		var endpoints []metricsEndpoint
		var mu sync.Mutex
		var connections [receiptReplicaCount][]string
		defer func() {
			cancel()
			for _, server := range servers {
				server.Close()
			}
		}()
		for i := range receiptReplicaCount {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				mu.Lock()
				connections[i] = append(connections[i], request.RemoteAddr)
				count := len(connections[i])
				mu.Unlock()
				if count == 1 {
					// The first round takes >2s in total, while each request
					// remains below the unchanged 2s HTTP client timeout.
					timer := time.NewTimer(750 * time.Millisecond)
					defer timer.Stop()
					select {
					case <-timer.C:
					case <-request.Context().Done():
						return
					}
				}
				if i == 0 {
					// Match the bridge's HTTP/1.0 close-after-response lifecycle.
					w.Header().Set("Connection", "close")
				}
				_, _ = io.WriteString(w, "go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n"+testRuntimeObservations)
				if i == receiptReplicaCount-1 && count == 2 {
					close(stop)
				}
			}))
			servers = append(servers, server)
			address := strings.Replace(server.URL, "127.0.0.1", "localhost", 1)
			endpoints = append(endpoints, metricsEndpoint{url: address + "/metrics", endpoint: address})
		}
		started := time.Now()
		report := sampleSteadyMetrics(ctx, steadyMetricsConfig{endpoints: endpoints, interval: 10 * time.Millisecond}, started, stop)
		if report.Failure != "" || len(report.Samples) != 2 || ctx.Err() != nil {
			t.Fatalf("two complete rounds under per-request deadline = %+v, caller=%v", report, ctx.Err())
		}
		if time.Since(started) <= 2*time.Second {
			t.Fatal("local HTTP control did not span the 2s per-request timeout")
		}
		mu.Lock()
		defer mu.Unlock()
		for i, addresses := range connections {
			if len(addresses) != 2 || len(report.Samples[0].Replicas) != receiptReplicaCount || len(report.Samples[1].Replicas) != receiptReplicaCount {
				t.Fatalf("replica %d requests/complete samples = %v, %+v", i, addresses, report.Samples)
			}
			if (addresses[0] == addresses[1]) != (i != 0) {
				t.Fatalf("replica %d connection reuse = %v; want fresh for0, reused for1/2", i, addresses)
			}
		}
	})
	t.Run("headers timeout keeps caller active and fails incomplete round", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		release := make(chan struct{})
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			attempts.Add(1)
			select {
			case <-request.Context().Done():
			case <-release:
			}
		}))
		defer func() {
			cancel()
			close(release)
			server.Close()
		}()
		var laterAttempts atomic.Int32
		later := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			laterAttempts.Add(1)
			_, _ = io.WriteString(w, "go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n"+testRuntimeObservations)
		}))
		defer later.Close()
		report := sampleSteadyMetrics(ctx, steadyMetricsConfig{endpoints: []metricsEndpoint{
			{url: server.URL + "/metrics", endpoint: "held headers"},
			{url: later.URL + "/metrics", endpoint: "later replica"},
		}, interval: 5 * time.Second}, time.Now(), make(chan struct{}))
		if report.Failure == "" || !strings.Contains(report.Failure, "Client.Timeout exceeded while awaiting headers") ||
			ctx.Err() != nil || len(report.Samples) != 0 || attempts.Load() != 1 || laterAttempts.Load() != 0 {
			t.Fatalf("headers timeout must fail closed once with active caller: report=%+v caller=%v attempts=%d later=%d",
				report, ctx.Err(), attempts.Load(), laterAttempts.Load())
		}
	})
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
		{"at steady bounds", 15, 40, 0, 0, true, 15},
		{"goroutine spike recovers", 16, 0, 0, 0, false, 16},
		{"heap spike within steady headroom recovers", 0, 33, 0, 0, true, 0},
		{"heap spike above steady headroom recovers", 0, 41, 0, 0, false, 0},
		{"post-cooldown growth at bound", 0, 0, 0, 32, true, 0},
		{"post-cooldown heap growth persists", 0, 0, 0, 33, false, 0},
		{"post-cooldown goroutine growth persists", 0, 0, 16, 0, false, 0},
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
			got := evaluateReceiptLeak(prePath, postPath, steadyPath, 45*time.Second, 5*time.Second, 15, 32, 40)
			if (got.Verdict == "pass") != test.wantPass || len(got.Replicas) != receiptReplicaCount {
				t.Fatalf("leak verdict/replicas = %q/%d; failures = %v", got.Verdict, len(got.Replicas), got.Failures)
			}
			if got.Replicas[1].GoroutinePeakDelta != test.wantDelta {
				t.Errorf("steady goroutine peak delta = %d, want %d", got.Replicas[1].GoroutinePeakDelta, test.wantDelta)
			}
			if got.Replicas[1].GoroutineDelta != test.postG || got.Replicas[2].HeapAllocDeltaBytes != test.postHeapMB<<20 {
				t.Errorf("post-cooldown deltas = %+v", got.Replicas)
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
			got := evaluateReceiptLeak(pre, post, steady, 45*time.Second, 5*time.Second, 15, 32, 40)
			if got.Verdict != "fail" || len(got.Failures) == 0 {
				t.Fatalf("incomplete evidence passed: %+v", got)
			}
		})
	}
}

func TestEvaluateReceiptLeakRequiresSeparateHeapThresholds(t *testing.T) {
	t.Parallel()
	pre, post, steady, _ := receiptLeakFixture(t)
	for _, thresholds := range [][2]int{{0, 40}, {32, 0}, {32, -1}} {
		got := evaluateReceiptLeak(pre, post, steady, 45*time.Second, 5*time.Second, 15, thresholds[0], thresholds[1])
		if got.Verdict != "fail" || len(got.Failures) == 0 {
			t.Fatalf("invalid post/steady heap thresholds %v passed: %+v", thresholds, got)
		}
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
		"-max-goroutines", "15", "-max-heap-mb", "32", "-max-steady-heap-mb", "40", "-out", out,
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

func TestRunReceiptLeakEvaluationSamplingProtocol(t *testing.T) {
	t.Parallel()
	for _, protocol := range []string{"forced_gc_minimum", "natural_gc_single", "invalid"} {
		t.Run(protocol, func(t *testing.T) {
			pre, post, steady, _ := receiptLeakFixture(t)
			out := filepath.Join(t.TempDir(), "gate.json")
			args := []string{"-pre", pre, "-post", post, "-steady", steady, "-duration", "45s", "-interval", "5s", "-max-goroutines", "15", "-max-heap-mb", "32", "-max-steady-heap-mb", "40", "-out", out, "-snapshot-protocol", protocol}
			code := runReceiptLeakEvaluation(args)
			if protocol == "invalid" {
				if code != 2 {
					t.Fatalf("invalid protocol exit=%d", code)
				}
				if _, err := os.Stat(out); !os.IsNotExist(err) {
					t.Fatal("invalid protocol wrote artifact")
				}
				return
			}
			if code != 0 {
				t.Fatalf("exit=%d", code)
			}
			raw, err := os.ReadFile(out)
			if err != nil {
				t.Fatal(err)
			}
			var report receiptLeakReport
			if err := json.Unmarshal(raw, &report); err != nil || report.SnapshotProtocol != protocol {
				t.Fatalf("protocol=%q, error=%v", report.SnapshotProtocol, err)
			}
		})
	}
}

func TestParseRuntimeMetricsObservations(t *testing.T) {
	t.Parallel()
	const required = "go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n"
	for _, line := range strings.Split(strings.TrimSpace(testRuntimeObservations), "\n") {
		name := strings.Fields(line)[0]
		t.Run(name, func(t *testing.T) {
			for _, test := range []struct {
				name        string
				replacement string
			}{
				{"missing", ""},
				{"duplicate", line + "\n" + line + "\n"},
				{"malformed", name + " nope\n"},
				{"negative", name + " -1\n"},
				{"nan", name + " NaN\n"},
				{"infinity", name + " +Inf\n"},
				{"extra-field", name + " 0 1000\n"},
				{"labelled", name + "{replica=\"0\"} 0\n"},
			} {
				t.Run(test.name, func(t *testing.T) {
					body := required + strings.Replace(testRuntimeObservations, line+"\n", test.replacement, 1)
					if _, err := parseRuntimeMetrics(strings.NewReader(body)); err == nil {
						t.Fatal("accepted missing, duplicate or invalid diagnostic observation")
					}
				})
			}
			zero := required + strings.Replace(testRuntimeObservations, line+"\n", name+" 0\n", 1)
			if _, err := parseRuntimeMetrics(strings.NewReader(zero)); err != nil {
				t.Fatalf("valid zero observation: %v", err)
			}
		})
	}
	for _, test := range []struct {
		name, value string
	}{
		{"go_gc_duration_seconds_count", "0.5"},
		{"go_memstats_next_gc_bytes", "9007199254740993.1"},
		{"lantern_receipt_entries", "9223372036854775808"},
		{"lantern_receipt_bytes", "1e-9999"},
		{"go_memstats_last_gc_time_seconds", "1e-9999"},
		{"lantern_mutation_log_fill_ratio", "1.00000000000000001"},
	} {
		t.Run(test.name+"/out-of-range", func(t *testing.T) {
			lines := strings.Split(testRuntimeObservations, "\n")
			for i, line := range lines {
				if strings.HasPrefix(line, test.name+" ") {
					lines[i] = test.name + " " + test.value
				}
			}
			if _, err := parseRuntimeMetrics(strings.NewReader(required + strings.Join(lines, "\n"))); err == nil {
				t.Fatal("accepted fractional integer, overflow, underflow or fill ratio above one")
			}
		})
	}
	body := required + strings.Replace(testRuntimeObservations, "lantern_mutation_log_evicted_total 0",
		"lantern_mutation_log_evicted_total 9007199254740993", 1)
	parsed, err := parseRuntimeMetrics(strings.NewReader(body))
	if err != nil || parsed.Metrics.MutationLogEvictedTotal != 9007199254740993 {
		t.Fatalf("exact diagnostic counter = %+v, %v", parsed.Metrics, err)
	}
	raw, err := json.Marshal(parsed)
	if err != nil || !strings.Contains(string(raw), `"lantern_mutation_log_evicted_total":9007199254740993`) {
		t.Fatalf("serialized diagnostic counter lost precision: %s, %v", raw, err)
	}
}

func TestEvaluateReceiptLeakByteBoundaries(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		post, peak int64
		pass       bool
	}{
		{"post-exact", 32 << 20, 0, true},
		{"post-one-byte-over", (32 << 20) + 1, 0, false},
		{"steady-exact", 0, 40 << 20, true},
		{"steady-one-byte-over", 0, (40 << 20) + 1, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pre, post, steady, samples := receiptLeakFixture(t)
			postSamples := testRuntimeSnapshots()
			*postSamples[0].HeapAllocBytes += test.post
			samples.Samples[4].Replicas[0].HeapAllocBytes += test.peak
			mustWriteReceiptArtifact(t, post, postSamples)
			mustWriteReceiptArtifact(t, steady, samples)
			got := evaluateReceiptLeak(pre, post, steady, 45*time.Second, 5*time.Second, 15, 32, 40)
			if (got.Verdict == "pass") != test.pass || got.Replicas[0].HeapAllocDeltaBytes != test.post ||
				got.Replicas[0].HeapAllocPeakDelta != test.peak {
				t.Fatalf("byte boundary verdict/deltas = %+v", got)
			}
		})
	}
}

func TestRunReceiptLeakEvaluationLegacyObservationsKeepFailure(t *testing.T) {
	t.Parallel()
	pre, post, steady, samples := receiptLeakFixture(t)
	postSamples := testRuntimeSnapshots()
	*postSamples[0].HeapAllocBytes += 34352632
	samples.Samples[4].Replicas[0].HeapAllocBytes += 30416336
	mustWriteReceiptArtifact(t, post, postSamples)
	// Nil observations deliberately serialize the old saved steady schema,
	// which remains evaluable without inventing zero-valued historical metrics.
	mustWriteReceiptArtifact(t, steady, samples)
	raw, err := os.ReadFile(steady)
	if err != nil || strings.Contains(string(raw), `"metrics"`) {
		t.Fatalf("legacy fixture unexpectedly contains observations: %s, %v", raw, err)
	}
	out := filepath.Join(t.TempDir(), "gate.json")
	code := runReceiptLeakEvaluation([]string{
		"-pre", pre, "-post", post, "-steady", steady, "-duration", "45s", "-interval", "5s",
		"-max-goroutines", "15", "-max-heap-mb", "32", "-max-steady-heap-mb", "40",
		"-out", out, "-snapshot-protocol", "natural_gc_single",
	})
	if code != 1 {
		t.Fatalf("legacy resource failure exit = %d, want 1", code)
	}
	raw, err = os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	var report receiptLeakReport
	if err := json.Unmarshal(raw, &report); err != nil || report.SnapshotProtocol != "natural_gc_single" ||
		report.Verdict != "fail" || len(report.Failures) != 1 ||
		report.Failures[0] != "localhost:9390 post-cooldown HeapAlloc growth exceeds +32 MiB" ||
		report.Replicas[0].HeapAllocDeltaBytes != 34352632 {
		t.Fatalf("legacy natural-GC resource gate changed: %+v, %v", report, err)
	}
}

type runtimeDiagnosticRoundTripper func(*http.Request) (*http.Response, error)

func (f runtimeDiagnosticRoundTripper) RoundTrip(request *http.Request) (*http.Response, error) {
	return f(request)
}

type runtimeDiagnosticBody struct {
	io.Reader
	closeErr error
}

func (b runtimeDiagnosticBody) Close() error { return b.closeErr }

func TestScrapeRuntimeMetricsDiagnostics(t *testing.T) {
	t.Parallel()
	const valid = "# BODY_SECRET\ngo_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n" + testRuntimeObservations
	for _, test := range []struct {
		name         string
		url          string
		status       int
		body         string
		transportErr error
		readErr      error
		closeErr     error
		cancel       bool
		stage        string
		reason       string
		contextState string
		wantCalls    int
		wantOK       bool
	}{
		{name: "success", stage: "complete", reason: "ok", contextState: "active", wantCalls: 1, wantOK: true},
		{name: "header deadline", transportErr: context.DeadlineExceeded, stage: "http", reason: "deadline_exceeded", contextState: "active", wantCalls: 1},
		{name: "transport failure", transportErr: errors.New("FULL_ERROR_SECRET"), stage: "http", reason: "transport_error", contextState: "active", wantCalls: 1},
		{name: "caller cancellation", cancel: true, stage: "http", reason: "canceled", contextState: "canceled", wantCalls: 1},
		{name: "invalid request", url: "\nURL_SECRET", stage: "request", reason: "invalid_request", contextState: "active"},
		{name: "HTTP status", status: http.StatusServiceUnavailable, stage: "status", reason: "http_status", contextState: "active", wantCalls: 1},
		{name: "HTTP status close failure", status: http.StatusServiceUnavailable, closeErr: errors.New("CLOSE_SECRET"), stage: "status", reason: "http_status_close_error", contextState: "active", wantCalls: 1},
		{name: "malformed metrics", body: "go_goroutines BODY_SECRET\n", stage: "parse", reason: "parse_error", contextState: "active", wantCalls: 1},
		{name: "body read failure", readErr: errors.New("READ_SECRET"), stage: "parse", reason: "parse_error", contextState: "active", wantCalls: 1},
		{name: "body close failure", closeErr: errors.New("CLOSE_SECRET"), stage: "close", reason: "close_error", contextState: "active", wantCalls: 1},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			status := test.status
			if status == 0 {
				status = http.StatusOK
			}
			client := &http.Client{Timeout: 2 * time.Second, Transport: runtimeDiagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
				calls++
				request.Header.Set("Authorization", "Bearer AUTH_SECRET")
				if test.cancel {
					cancel()
					return nil, request.Context().Err()
				}
				if test.transportErr != nil {
					return nil, test.transportErr
				}
				body := test.body
				if body == "" {
					body = valid
				}
				var reader io.Reader = strings.NewReader(body)
				if test.readErr != nil {
					reader = iotest.ErrReader(test.readErr)
				}
				return &http.Response{
					StatusCode: status, Status: "STATUS_SECRET", Request: request,
					Header: http.Header{"X-Private": {"HEADER_SECRET"}},
					Body:   runtimeDiagnosticBody{Reader: reader, closeErr: test.closeErr},
				}, nil
			})}
			address := test.url
			if address == "" {
				address = "http://URL_SECRET@private-metrics.invalid:9390/metrics?credential=QUERY_SECRET"
			}
			var output bytes.Buffer
			value, err := scrapeRuntimeMetricsObserved(ctx, client,
				metricsEndpoint{url: address, endpoint: "ENDPOINT_SECRET"}, time.Now().Add(-40*time.Second), 1, &output)
			if (err == nil) != test.wantOK || calls != test.wantCalls {
				t.Fatalf("scrape result/calls = %+v, %v, %d; want success=%v calls=%d", value, err, calls, test.wantOK, test.wantCalls)
			}
			if test.wantOK && (value.Goroutines != 40 || value.HeapAllocBytes != testHeapBaseline || value.Endpoint != "ENDPOINT_SECRET") {
				t.Fatalf("diagnostics changed the successful value: %+v", value)
			}
			for _, secret := range []string{"SECRET", "private-metrics", "http://", "Authorization", "Bearer"} {
				if strings.Contains(output.String(), secret) {
					t.Fatalf("diagnostics exposed request/response content: %s", output.String())
				}
			}
			lines := strings.Split(strings.TrimSpace(output.String()), "\n")
			if len(lines) != 2 {
				t.Fatalf("diagnostic lines = %d, want exactly 2", len(lines))
			}
			var records [2]runtimeScrapeDiagnostic
			for i, line := range lines {
				if len(line) > 512 {
					t.Fatalf("unbounded diagnostic line: %d bytes", len(line))
				}
				decoder := json.NewDecoder(strings.NewReader(line))
				decoder.DisallowUnknownFields()
				if err := decoder.Decode(&records[i]); err != nil {
					t.Fatal(err)
				}
				if records[i].ReplicaIndex != 1 || records[i].ElapsedMS < 40_000 {
					t.Fatalf("diagnostic identity/start elapsed = %+v", records[i])
				}
				if _, err := time.Parse(time.RFC3339Nano, records[i].UTC); err != nil || !strings.HasSuffix(records[i].UTC, "Z") {
					t.Fatalf("diagnostic UTC = %q, %v", records[i].UTC, err)
				}
			}
			start, finish := records[0], records[1]
			if start.Event != "request_start" || start.Stage != "request" || start.Reason != "started" || start.DurationMS != nil || start.ContextState != "active" {
				t.Fatalf("start diagnostic = %+v", start)
			}
			if finish.Event != "request_finish" || finish.Stage != test.stage || finish.Reason != test.reason || finish.ContextState != test.contextState ||
				finish.DurationMS == nil || *finish.DurationMS < 0 || finish.ElapsedMS < start.ElapsedMS {
				t.Fatalf("finish diagnostic = %+v", finish)
			}
			wantStatus := status
			if calls == 0 || test.cancel || test.transportErr != nil {
				wantStatus = 0
			}
			if finish.HTTPStatus != wantStatus {
				t.Fatalf("diagnostic status = %d, want %d", finish.HTTPStatus, wantStatus)
			}
		})
	}
}

type runtimeDiagnosticFailWriter struct{ calls int }

func (w *runtimeDiagnosticFailWriter) Write([]byte) (int, error) {
	w.calls++
	return 0, errors.New("diagnostic writer unavailable")
}

func TestScrapeRuntimeMetricsDiagnosticWriteFailureKeepsResult(t *testing.T) {
	t.Parallel()
	calls := 0
	client := &http.Client{Transport: runtimeDiagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
		calls++
		return &http.Response{StatusCode: http.StatusOK, Request: request,
			Body: io.NopCloser(strings.NewReader("go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n" + testRuntimeObservations))}, nil
	})}
	var output runtimeDiagnosticFailWriter
	value, err := scrapeRuntimeMetricsObserved(context.Background(), client,
		metricsEndpoint{url: "http://metrics.invalid:9390/metrics", endpoint: "replica"}, time.Now(), 0, &output)
	if err != nil || value.Goroutines != 40 || calls != 1 || output.calls != 2 {
		t.Fatalf("diagnostic failure changed result: %+v, %v; requests=%d writes=%d", value, err, calls, output.calls)
	}
}

func TestValidateSteadyMetricsExactCoverage(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		rounds     int
		durationMS int64
		failure    string
		wantOK     bool
	}{
		{name: "nine complete rounds", rounds: 9, durationMS: 45_000, wantOK: true},
		{name: "optional tenth round", rounds: 10, durationMS: 45_000, wantOK: true},
		{name: "eight recorded rounds", rounds: 8, durationMS: 45_006},
		{name: "failure despite nine rounds", rounds: 9, durationMS: 45_000, failure: "scrape failed"},
		{name: "exact final coverage bound", rounds: 9, durationMS: 47_500, wantOK: true},
		{name: "one ms beyond final coverage bound", rounds: 9, durationMS: 47_501},
	} {
		t.Run(test.name, func(t *testing.T) {
			cfg := steadyMetricsConfig{interval: 5 * time.Second}
			report := steadyMetricsReport{IntervalMS: 5_000, DurationMS: test.durationMS, Failure: test.failure}
			for _, replica := range testRuntimeSnapshots() {
				cfg.endpoints = append(cfg.endpoints, metricsEndpoint{endpoint: replica.Endpoint})
				report.Endpoints = append(report.Endpoints, replica.Endpoint)
			}
			for i := range test.rounds {
				round := metricsRound{ElapsedMS: int64(i) * 5_000}
				for _, replica := range testRuntimeSnapshots() {
					round.Replicas = append(round.Replicas, runtimeMetrics{
						Endpoint: replica.Endpoint, Goroutines: *replica.Goroutines, HeapAllocBytes: *replica.HeapAllocBytes,
					})
				}
				report.Samples = append(report.Samples, round)
			}
			if err := validateSteadyMetrics(report, cfg, 45*time.Second); (err == nil) != test.wantOK {
				t.Fatalf("coverage accepted=%v, want=%v: %v", err == nil, test.wantOK, err)
			}
		})
	}
}
