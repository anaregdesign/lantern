package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/iotest"
	"time"

	"github.com/anaregdesign/lantern/server/benchheap"
)

const testHeapBaseline = int64(100 << 20)

func TestNaturalReferenceSeparatesAdvisoryPostFromMandatoryChecks(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name       string
		peak       int64
		goroutines int64
		pass       bool
	}{
		{"post above32 is advisory", 0, 0, true},
		{"steady above40 still fails", 41, 0, false},
		{"goroutine growth still fails", 0, 16, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			pre, post, steady, samples := receiptLeakFixture(t)
			values := testRuntimeSnapshots()
			*values[1].HeapAllocBytes += 33 << 20
			mustWriteReceiptArtifact(t, post, values)
			samples.Samples[4].Replicas[2].HeapAllocBytes += test.peak << 20
			samples.Samples[4].Replicas[2].Goroutines += test.goroutines
			mustWriteReceiptArtifact(t, steady, samples)
			got := evaluateReceiptLeakProtocol(pre, post, steady, 45*time.Second, 5*time.Second, 15, 32, 40, "natural_reference_v1")
			if (got.Verdict == "pass") != test.pass || got.NaturalChecksVerdict != got.Verdict || got.NaturalPostHeapObservationVerdict != "fail" || !got.PairRequired || got.RetainedVerdict != "requires_pair" || got.OverallMemoryVerdict != "requires_pair" {
				t.Fatalf("paired scope or mandatory checks changed: %+v", got)
			}
			old := evaluateReceiptLeak(pre, post, steady, 45*time.Second, 5*time.Second, 15, 32, 40)
			if old.Verdict != "fail" || old.PairRequired {
				t.Fatalf("historical natural_gc_single failure changed: %+v", old)
			}
		})
	}
}

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

func TestSteadyMetricsPendingDialOwnership(t *testing.T) {
	// Deliberately not parallel: the sampler control temporarily replaces the
	// process default. Restore it before any parallel top-level tests resume.
	t.Run("request cancellation does not own a reusable pending dial", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
		defer cancel()
		requestCtx, cancelRequest := context.WithCancel(ctx)
		defer cancelRequest()
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts.Add(1)
			_, _ = io.WriteString(w, "go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n"+testRuntimeObservations)
		}))
		base, ok := http.DefaultTransport.(*http.Transport)
		if !ok || base == nil {
			t.Fatal("control requires the standard default transport")
		}
		transport := base.Clone()
		transport.Proxy = nil
		if transport.MaxConnsPerHost != 0 || transport.DialContext == nil {
			t.Fatal("control changed the standard unlimited active connection policy")
		}
		dial := transport.DialContext
		entered, finished := make(chan context.Context, 1), make(chan struct{})
		release := make(chan struct{})
		var releaseOnce sync.Once
		releaseDial := func() { releaseOnce.Do(func() { close(release) }) }
		var dials atomic.Int32
		transport.DialContext = func(dialCtx context.Context, network, address string) (net.Conn, error) {
			if dials.Add(1) == 1 {
				defer close(finished)
				entered <- dialCtx
				select {
				case <-dialCtx.Done():
					return nil, dialCtx.Err()
				case <-release:
					return nil, errors.New("controlled pending dial teardown")
				}
			}
			return dial(dialCtx, network, address)
		}
		defer func() {
			transport.CloseIdleConnections()
			releaseDial()
			server.Close()
			if dials.Load() != 0 {
				select {
				case <-finished:
				case <-time.After(2 * time.Second):
					t.Error("controlled pending dial did not join during teardown")
				}
			}
		}()
		client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
		endpoint := metricsEndpoint{url: server.URL + "/metrics", endpoint: "local control"}
		var output bytes.Buffer
		_, err := scrapeRuntimeMetricsObserved(requestCtx, client, endpoint, time.Now(), 0, &output)
		if !errors.Is(err, context.DeadlineExceeded) || requestCtx.Err() != nil || attempts.Load() != 0 {
			t.Fatalf("pending dial changed the original HTTP deadline or reached a server: err=%v caller=%v requests=%d", err, requestCtx.Err(), attempts.Load())
		}
		var dialCtx context.Context
		select {
		case dialCtx = <-entered:
		case <-ctx.Done():
			t.Fatal("controlled dial did not start", ctx.Err())
		}
		finish := runtimeTraceScrapeRecords(t, output.String())[1]
		trace := finish.HTTPTrace
		if finish.Stage != "http" || finish.Reason != "deadline_exceeded" || finish.ContextState != "active" ||
			trace.GetConn == nil || trace.GetConn.Count != 1 || trace.GotConn != nil || trace.ConnectStart != nil || trace.ConnectDone != nil || trace.WroteRequest != nil || trace.FirstResponseByte != nil {
			t.Fatalf("pre-connect wait did not preserve the GetConn-only observation: %+v", finish)
		}
		cancelRequest()
		if dialCtx.Err() != nil {
			t.Fatal("control did not retain Transport-owned dialing after request cancellation", dialCtx.Err())
		}
		// An abandoned dial is not a default MaxConnsPerHost queue limit.
		// A fresh request can connect while that first dial remains held.
		value, err := scrapeRuntimeMetricsObserved(ctx, client, endpoint, time.Now(), 0, io.Discard)
		if err != nil || value.Goroutines != 40 || attempts.Load() != 1 || dials.Load() != 2 || dialCtx.Err() != nil {
			t.Fatalf("pending dial blocked the next unlimited-connection scrape: value=%+v err=%v requests=%d dials=%d", value, err, attempts.Load(), dials.Load())
		}
		transport.CloseIdleConnections()
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("transport teardown did not cancel its unused pending dial", ctx.Err())
		}
		if !errors.Is(dialCtx.Err(), context.Canceled) {
			t.Fatal("transport teardown did not own dial cancellation", dialCtx.Err())
		}
	})

	t.Run("sampler releases its unused dial before returning", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
		defer cancel()
		original := http.DefaultTransport
		base, ok := original.(*http.Transport)
		if !ok || base == nil {
			t.Fatal("sampler control requires the standard default transport")
		}
		transport := base.Clone()
		transport.Proxy = nil
		entered, finished := make(chan context.Context, 1), make(chan struct{})
		release := make(chan struct{})
		var dials atomic.Int32
		var finishOnce sync.Once
		transport.DialContext = func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			dials.Add(1)
			select {
			case entered <- dialCtx:
			default:
			}
			defer finishOnce.Do(func() { close(finished) })
			select {
			case <-dialCtx.Done():
				return nil, dialCtx.Err()
			case <-release:
				return nil, errors.New("controlled sampler dial teardown")
			}
		}
		http.DefaultTransport = transport
		defer func() {
			http.DefaultTransport = original
			transport.CloseIdleConnections()
			close(release)
			select {
			case <-finished:
			case <-time.After(2 * time.Second):
				t.Error("controlled sampler dial did not join during teardown")
			}
		}()
		report := sampleSteadyMetrics(ctx, steadyMetricsConfig{
			endpoints: []metricsEndpoint{{url: "http://127.0.0.1:1/metrics", endpoint: "held local control"}},
			interval:  5 * time.Second,
		}, time.Now(), make(chan struct{}))
		if report.Failure == "" || len(report.Samples) != 0 || ctx.Err() != nil || dials.Load() != 1 {
			t.Fatalf("sampler changed fail-closed timeout semantics: failure=%t samples=%d caller=%v dials=%d", report.Failure != "", len(report.Samples), ctx.Err(), dials.Load())
		}
		var dialCtx context.Context
		select {
		case dialCtx = <-entered:
		case <-ctx.Done():
			t.Fatal("sampler dial did not enter its controlled wait", ctx.Err())
		}
		// Teardown requests cancellation before return. The Transport's
		// goroutine can observe it afterwards, so join it independently.
		select {
		case <-finished:
		case <-ctx.Done():
			t.Fatal("returned sampler retained an unused pending dial", ctx.Err())
		}
		if !errors.Is(dialCtx.Err(), context.Canceled) {
			t.Fatal("sampler did not close its owned unused dial", dialCtx.Err())
		}
	})
}

type runtimeMetricsCloseObservedConn struct {
	net.Conn
	closed chan struct{}
	once   *sync.Once
}

func (c runtimeMetricsCloseObservedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { close(c.closed) })
	return err
}

func runtimeMetricsDefaultTransportForTest(t *testing.T) *http.Transport {
	t.Helper()
	original := http.DefaultTransport
	base, ok := original.(*http.Transport)
	if !ok || base == nil {
		t.Fatal("control requires the standard default transport")
	}
	transport := base.Clone()
	transport.Proxy = nil
	http.DefaultTransport = transport
	t.Cleanup(func() {
		http.DefaultTransport = original
		transport.CloseIdleConnections()
	})
	return transport
}

func TestSteadyMetricsTransportLifecycleControls(t *testing.T) {
	// These controls also own the temporary process default sequentially.
	t.Run("unsupported default transport fails closed", func(t *testing.T) {
		var calls atomic.Int32
		for _, test := range []struct {
			name      string
			transport http.RoundTripper
		}{
			{name: "custom RoundTripper", transport: runtimeDiagnosticRoundTripper(func(*http.Request) (*http.Response, error) {
				calls.Add(1)
				return nil, errors.New("unexpected metrics transport invocation")
			})},
			{name: "typed nil standard Transport", transport: (*http.Transport)(nil)},
		} {
			t.Run(test.name, func(t *testing.T) {
				original := http.DefaultTransport
				http.DefaultTransport = test.transport
				defer func() { http.DefaultTransport = original }()
				stop := make(chan struct{})
				close(stop)
				report := sampleSteadyMetrics(t.Context(), steadyMetricsConfig{interval: time.Second}, time.Now(), stop)
				if report.Failure != "metrics requires a configurable HTTP transport" || len(report.Samples) != 0 || calls.Load() != 0 {
					t.Fatalf("unsupported transport did not fail closed before sampling: failure=%t samples=%d calls=%d", report.Failure != "", len(report.Samples), calls.Load())
				}
			})
		}
	})

	t.Run("normal stop closes its owned idle connection", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
		defer cancel()
		transport := runtimeMetricsDefaultTransportForTest(t)
		dial := transport.DialContext
		closed := make(chan struct{})
		var closeOnce sync.Once
		var dials, requests atomic.Int32
		transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
			dials.Add(1)
			conn, err := dial(ctx, network, address)
			if err != nil {
				return nil, err
			}
			return runtimeMetricsCloseObservedConn{Conn: conn, closed: closed, once: &closeOnce}, nil
		}
		stop := make(chan struct{})
		var stopOnce sync.Once
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			requests.Add(1)
			_, _ = io.WriteString(w, "go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n"+testRuntimeObservations)
			stopOnce.Do(func() { close(stop) })
		}))
		defer server.Close()
		report := sampleSteadyMetrics(ctx, steadyMetricsConfig{
			endpoints: []metricsEndpoint{{url: server.URL + "/metrics", endpoint: "normal local control"}}, interval: 5 * time.Second,
		}, time.Now(), stop)
		if report.Failure != "" || len(report.Samples) != 1 || requests.Load() != 1 || dials.Load() != 1 || ctx.Err() != nil {
			t.Fatalf("normal completion changed metrics sampling: failure=%t rounds=%d requests=%d dials=%d caller=%v", report.Failure != "", len(report.Samples), requests.Load(), dials.Load(), ctx.Err())
		}
		select {
		case <-closed:
		case <-ctx.Done():
			t.Fatal("stopped sampler retained its owned idle connection", ctx.Err())
		}
	})

	t.Run("parent cancellation closes the sampler's pending dial", func(t *testing.T) {
		ownerCtx, cancelOwner := context.WithTimeout(t.Context(), 6*time.Second)
		defer cancelOwner()
		ctx, cancel := context.WithCancel(ownerCtx)
		transport := runtimeMetricsDefaultTransportForTest(t)
		entered, dialDone, samplerDone := make(chan context.Context, 1), make(chan struct{}), make(chan struct{})
		release := make(chan struct{})
		var releaseOnce, doneOnce sync.Once
		var dials atomic.Int32
		transport.DialContext = func(dialCtx context.Context, _, _ string) (net.Conn, error) {
			dials.Add(1)
			select {
			case entered <- dialCtx:
			default:
			}
			defer doneOnce.Do(func() { close(dialDone) })
			select {
			case <-dialCtx.Done():
				return nil, dialCtx.Err()
			case <-release:
				return nil, errors.New("controlled canceled sampler teardown")
			}
		}
		t.Cleanup(func() {
			cancel()
			transport.CloseIdleConnections()
			releaseOnce.Do(func() { close(release) })
			for _, done := range []<-chan struct{}{samplerDone, dialDone} {
				select {
				case <-done:
				case <-time.After(2 * time.Second):
					t.Error("controlled canceled sampler did not join during teardown")
				}
			}
		})
		result := make(chan steadyMetricsReport, 1)
		go func() {
			defer close(samplerDone)
			result <- sampleSteadyMetrics(ctx, steadyMetricsConfig{
				endpoints: []metricsEndpoint{{url: "http://127.0.0.1:1/metrics", endpoint: "canceled local control"}}, interval: 5 * time.Second,
			}, time.Now(), make(chan struct{}))
		}()
		var dialCtx context.Context
		select {
		case dialCtx = <-entered:
		case <-ownerCtx.Done():
			t.Fatal("sampler did not enter its pending dial", ownerCtx.Err())
		}
		cancel()
		select {
		case report := <-result:
			if report.Failure == "" || len(report.Samples) != 0 || !errors.Is(ctx.Err(), context.Canceled) || dials.Load() != 1 {
				t.Fatalf("parent cancellation changed fail-closed sampling: failure=%t rounds=%d caller=%v dials=%d", report.Failure != "", len(report.Samples), ctx.Err(), dials.Load())
			}
		case <-ownerCtx.Done():
			t.Fatal("sampler did not return after parent cancellation", ownerCtx.Err())
		}
		select {
		case <-dialDone:
		case <-ownerCtx.Done():
			t.Fatal("canceled sampler retained its unused dial", ownerCtx.Err())
		}
		if !errors.Is(dialCtx.Err(), context.Canceled) {
			t.Fatal("sampler's pending dial did not receive transport cancellation", dialCtx.Err())
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

type runtimeTraceDiagnosticWriter struct {
	mu     sync.Mutex
	output bytes.Buffer
	writes int
}

func (w *runtimeTraceDiagnosticWriter) Write(data []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.writes++
	return w.output.Write(data)
}

func (w *runtimeTraceDiagnosticWriter) snapshot() (string, int) {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String(), w.writes
}

type runtimeTraceGatedReader struct {
	io.Reader
	entered chan struct{}
	release <-chan struct{}
	once    sync.Once
}

func (r *runtimeTraceGatedReader) Read(data []byte) (int, error) {
	r.once.Do(func() { close(r.entered) })
	<-r.release
	return r.Reader.Read(data)
}

func runtimeTraceScrapeRecords(t *testing.T, output string) [2]runtimeScrapeDiagnostic {
	t.Helper()
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) != 2 {
		t.Fatalf("trace added diagnostic writes: got %d lines", len(lines))
	}
	var records [2]runtimeScrapeDiagnostic
	for i, line := range lines {
		if len(line) > 2048 {
			t.Fatalf("trace diagnostic exceeds fixed schema budget: %d", len(line))
		}
		decoder := json.NewDecoder(strings.NewReader(line))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&records[i]); err != nil {
			t.Fatal(err)
		}
	}
	if records[0].Event != "request_start" || records[0].HTTPTrace != nil ||
		records[1].Event != "request_finish" || records[1].HTTPTrace == nil {
		t.Fatalf("trace must appear only on the final diagnostic: %+v", records)
	}
	return records
}

func TestScrapeRuntimeMetricsHTTPTraceCompositionAndLateCallbacks(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
	defer cancel()
	var parentGetConn, parentDNSStart, parentDNSDone, parentWroteRequest atomic.Int32
	ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
		GetConn:      func(string) { parentGetConn.Add(1) },
		DNSStart:     func(httptrace.DNSStartInfo) { parentDNSStart.Add(1) },
		DNSDone:      func(httptrace.DNSDoneInfo) { parentDNSDone.Add(1) },
		WroteRequest: func(httptrace.WroteRequestInfo) { parentWroteRequest.Add(1) },
	})
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseBody := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(releaseBody)
	var output runtimeTraceDiagnosticWriter
	callbacks := make(chan *httptrace.ClientTrace, 1)
	var requests atomic.Int32
	var writesDuringCallbacks atomic.Int32
	fire := func(trace *httptrace.ClientTrace, reused bool, err error) {
		var wait sync.WaitGroup
		for _, callback := range []func(){
			func() { trace.GetConn("HOST_SECRET:9390") },
			func() { trace.DNSStart(httptrace.DNSStartInfo{Host: "DNS_HOST_SECRET"}) },
			func() { trace.DNSDone(httptrace.DNSDoneInfo{Err: err, Coalesced: reused}) },
			func() { trace.GotConn(httptrace.GotConnInfo{Reused: reused}) },
			func() { trace.ConnectStart("NETWORK_SECRET", "ADDRESS_SECRET") },
			func() { trace.ConnectDone("NETWORK_SECRET", "ADDRESS_SECRET", err) },
			func() { trace.WroteRequest(httptrace.WroteRequestInfo{Err: err}) },
			trace.GotFirstResponseByte,
		} {
			wait.Go(callback)
		}
		wait.Wait()
	}
	client := &http.Client{Timeout: 2 * time.Second, Transport: runtimeDiagnosticRoundTripper(func(request *http.Request) (*http.Response, error) {
		requests.Add(1)
		trace := httptrace.ContextClientTrace(request.Context())
		if trace == nil || trace.GetConn == nil || trace.DNSStart == nil || trace.DNSDone == nil || trace.GotConn == nil || trace.ConnectStart == nil ||
			trace.ConnectDone == nil || trace.WroteRequest == nil || trace.GotFirstResponseByte == nil {
			return nil, errors.New("missing request trace callbacks")
		}
		fire(trace, true, errors.New("CALLBACK_ERROR_SECRET"))
		_, writes := output.snapshot()
		writesDuringCallbacks.Store(int32(writes))
		callbacks <- trace
		request.Header.Set("Authorization", "Bearer AUTH_SECRET")
		return &http.Response{StatusCode: http.StatusOK, Request: request,
			Header: http.Header{"X-Private": {"HEADER_SECRET"}},
			Body: io.NopCloser(&runtimeTraceGatedReader{
				Reader:  strings.NewReader("# BODY_SECRET\ngo_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n" + testRuntimeObservations),
				entered: entered, release: release,
			})}, nil
	})}
	type scrapeResult struct {
		value runtimeMetrics
		err   error
	}
	done := make(chan scrapeResult, 1)
	joined := make(chan struct{})
	go func() {
		defer close(joined)
		value, err := scrapeRuntimeMetricsObserved(ctx, client,
			metricsEndpoint{url: "http://URL_SECRET@metrics.invalid:9390/metrics?secret=QUERY_SECRET", endpoint: "ENDPOINT_SECRET"},
			time.Now(), 0, &output)
		done <- scrapeResult{value, err}
	}()
	t.Cleanup(func() {
		releaseBody()
		cancel()
		select {
		case <-joined:
		case <-time.After(time.Second):
			t.Error("controlled scrape did not join after releasing its body")
		}
	})
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("scrape did not reach parsing after Do returned", ctx.Err())
	}
	trace := <-callbacks
	// The body barrier proves Do has returned. Standard hooks may still arrive
	// concurrently while parsing; they update only the request's private state.
	fire(trace, false, nil)
	if _, writes := output.snapshot(); writes != 1 || writesDuringCallbacks.Load() != 1 {
		t.Fatalf("trace callbacks performed diagnostic I/O: writes=%d before Do=%d", writes, writesDuringCallbacks.Load())
	}
	releaseBody()
	var result scrapeResult
	select {
	case result = <-done:
	case <-ctx.Done():
		t.Fatal("scrape did not join", ctx.Err())
	}
	if result.err != nil || result.value.Goroutines != 40 || result.value.HeapAllocBytes != testHeapBaseline || requests.Load() != 1 {
		t.Fatalf("trace changed the request result: %+v, %v, requests=%d", result.value, result.err, requests.Load())
	}
	text, writes := output.snapshot()
	records := runtimeTraceScrapeRecords(t, text)
	finish := records[1]
	if writes != 2 || finish.Stage != "complete" || finish.Reason != "ok" || finish.HTTPStatus != http.StatusOK {
		t.Fatalf("unexpected trace completion: %+v writes=%d", finish, writes)
	}
	observed := finish.HTTPTrace
	for _, point := range []*runtimeHTTPTracePoint{observed.GetConn, observed.DNSStart, observed.DNSDone, observed.GotConn, observed.ConnectStart,
		observed.ConnectDone, observed.WroteRequest, observed.FirstResponseByte} {
		if point == nil || point.Count != 2 || point.FirstMS < 0 || point.LastMS < point.FirstMS {
			t.Fatalf("concurrent/late hook was lost or reversed: %+v", observed)
		}
	}
	if observed.LastConnReused == nil || *observed.LastConnReused || observed.LastConnectResult != "ok" || observed.LastWriteResult != "ok" ||
		observed.LastDNSCoalesced == nil || *observed.LastDNSCoalesced || observed.LastDNSResult != "ok" ||
		parentGetConn.Load() != 2 || parentDNSStart.Load() != 2 || parentDNSDone.Load() != 2 || parentWroteRequest.Load() != 2 {
		t.Fatalf("trace composition or last result differs: %+v parent=%d/%d", observed, parentGetConn.Load(), parentWroteRequest.Load())
	}
	for _, secret := range []string{"SECRET", "metrics.invalid", "http://", "Authorization", "Bearer"} {
		if strings.Contains(text, secret) {
			t.Fatalf("trace exposed request, response or callback content: %s", text)
		}
	}
	fire(trace, true, errors.New("AFTER_FINISH_SECRET"))
	if after, count := output.snapshot(); after != text || count != writes {
		t.Fatal("callbacks after finish changed emitted diagnostics")
	}
}

func TestScrapeRuntimeMetricsDialAndDNSPrefixControls(t *testing.T) {
	for _, dnsStarted := range []bool{false, true} {
		name := "existing dial held before DNS"
		if dnsStarted {
			name = "synthetic DNS hook held before Connect"
		}
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
			defer cancel()
			entered, joined, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			var dials atomic.Int32
			transport := &http.Transport{Proxy: nil, DialContext: func(dialCtx context.Context, network, address string) (net.Conn, error) {
				dials.Add(1)
				defer close(joined)
				if network != "tcp" || address != "metrics-control.invalid:9390" {
					return nil, errors.New("controlled dial arguments differ")
				}
				trace := httptrace.ContextClientTrace(dialCtx)
				// Synthetic hook control, not a real resolver reproduction. All
				// work stays in this owned fake: no sockets or DNS calls are made.
				if dnsStarted {
					trace.DNSStart(httptrace.DNSStartInfo{Host: "DNS_HOST_SECRET"})
				}
				close(entered)
				select {
				case <-dialCtx.Done():
				case <-release:
				}
				if dnsStarted {
					trace.DNSDone(httptrace.DNSDoneInfo{Err: dialCtx.Err()})
				}
				return nil, errors.New("controlled held dial finished")
			}}
			observeRuntimeMetricsDial(transport)
			t.Cleanup(func() {
				transport.CloseIdleConnections()
				releaseOnce.Do(func() { close(release) })
				select {
				case <-joined:
				case <-time.After(time.Second):
					t.Error("owned held dial did not join during teardown")
				}
			})
			client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
			var output bytes.Buffer
			_, err := scrapeRuntimeMetricsObserved(ctx, client,
				metricsEndpoint{url: "http://metrics-control.invalid:9390/metrics", endpoint: "synthetic local control"},
				time.Now(), 0, &output)
			if err == nil || !strings.Contains(err.Error(), "Client.Timeout exceeded while awaiting headers") || ctx.Err() != nil || dials.Load() != 1 {
				t.Fatalf("held dial changed timeout/caller semantics: err=%v caller=%v dials=%d", err, ctx.Err(), dials.Load())
			}
			select {
			case <-entered:
			default:
				t.Fatal("controlled dial did not reach its held phase")
			}
			finish := runtimeTraceScrapeRecords(t, output.String())[1]
			observed := finish.HTTPTrace
			if finish.Stage != "http" || finish.Reason != "deadline_exceeded" || finish.ContextState != "active" ||
				finish.DurationMS == nil || *finish.DurationMS < 1900 || observed.GetConn == nil || observed.DialEnter == nil ||
				observed.DialReturn != nil || observed.LastDialResult != "" || (observed.DNSStart != nil) != dnsStarted ||
				observed.DNSDone != nil || observed.LastDNSCoalesced != nil || observed.LastDNSResult != "" ||
				observed.ConnectStart != nil || observed.ConnectDone != nil || observed.GotConn != nil ||
				observed.WroteRequest != nil || observed.FirstResponseByte != nil {
				t.Fatalf("held dial prefix does not distinguish phases: %+v trace=%+v", finish, observed)
			}
			before := output.String()
			// The request timeout preserves Transport-owned dial lifetime. The
			// sampling owner then closes its own pool and joins this exact fake.
			transport.CloseIdleConnections()
			select {
			case <-joined:
			case <-ctx.Done():
				t.Fatal("owned held dial did not join after CloseIdleConnections", ctx.Err())
			}
			if output.String() != before || strings.Contains(before, "SECRET") || strings.Contains(before, "metrics-control") {
				t.Fatal("late DNS/dial completion changed or exposed diagnostic content")
			}
		})
	}
}

func TestScrapeRuntimeMetricsHTTPTraceOverLocalHTTP(t *testing.T) {
	const body = "go_goroutines 40\ngo_memstats_heap_alloc_bytes 104857600\n" + testRuntimeObservations
	t.Run("fresh then reused connection", func(t *testing.T) {
		var attempts atomic.Int32
		wrote := make(chan struct{}, 2)
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			attempts.Add(1)
			// Observe the write hook before replying, without assuming an order
			// between the Transport's writing and response-reading goroutines.
			select {
			case <-wrote:
			case <-request.Context().Done():
				return
			}
			_, _ = io.WriteString(w, body)
		}))
		defer server.Close()
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		observeRuntimeMetricsDial(transport)
		defer transport.CloseIdleConnections()
		client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
		ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
		defer cancel()
		ctx = httptrace.WithClientTrace(ctx, &httptrace.ClientTrace{
			WroteRequest: func(httptrace.WroteRequestInfo) { wrote <- struct{}{} },
		})
		for attempt := range 2 {
			var output bytes.Buffer
			value, err := scrapeRuntimeMetricsObserved(ctx, client,
				metricsEndpoint{url: server.URL + "/metrics", endpoint: "local replica"}, time.Now(), 0, &output)
			if err != nil || value.Goroutines != 40 || value.HeapAllocBytes != testHeapBaseline {
				t.Fatalf("local scrape %d = %+v, %v", attempt, value, err)
			}
			records := runtimeTraceScrapeRecords(t, output.String())
			trace := records[1].HTTPTrace
			if trace.GetConn == nil || trace.GotConn == nil || trace.WroteRequest == nil || trace.FirstResponseByte == nil ||
				trace.LastConnReused == nil || *trace.LastConnReused != (attempt == 1) || trace.LastWriteResult != "ok" {
				t.Fatalf("fresh/reused connection evidence %d: %+v", attempt, trace)
			}
			if attempt == 0 && (trace.ConnectStart == nil || trace.ConnectDone == nil || trace.LastConnectResult != "ok") ||
				attempt == 1 && (trace.ConnectStart != nil || trace.ConnectDone != nil || trace.LastConnectResult != "") {
				t.Fatalf("dial observations were reused across requests: %+v", trace)
			}
			if attempt == 0 && (trace.DialEnter == nil || trace.DialReturn == nil || trace.LastDialResult != "ok") ||
				attempt == 1 && (trace.DialEnter != nil || trace.DialReturn != nil || trace.LastDialResult != "") {
				t.Fatalf("originating dial prefix was reused across requests: %+v", trace)
			}
			// WroteRequest is not peer-receipt/flush proof; first byte is not
			// complete headers. These are observations of the standard hooks.
		}
		if attempts.Load() != 2 {
			t.Fatalf("trace added local requests: %d", attempts.Load())
		}
	})

	t.Run("two second timeout before response headers", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(t.Context(), 6*time.Second)
		defer cancel()
		release, joined := make(chan struct{}), make(chan struct{})
		var attempts atomic.Int32
		server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
			attempts.Add(1)
			defer close(joined)
			select {
			case <-request.Context().Done():
			case <-release:
			}
		}))
		defer func() { close(release); server.Close() }()
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.Proxy = nil
		observeRuntimeMetricsDial(transport)
		defer transport.CloseIdleConnections()
		client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
		var output bytes.Buffer
		_, err := scrapeRuntimeMetricsObserved(ctx, client,
			metricsEndpoint{url: server.URL + "/metrics", endpoint: "held local headers"}, time.Now(), 0, &output)
		if err == nil || !strings.Contains(err.Error(), "Client.Timeout exceeded while awaiting headers") || ctx.Err() != nil || attempts.Load() != 1 {
			t.Fatalf("unchanged header timeout/caller = %v, %v, attempts=%d", err, ctx.Err(), attempts.Load())
		}
		finish := runtimeTraceScrapeRecords(t, output.String())[1]
		trace := finish.HTTPTrace
		if finish.Stage != "http" || finish.Reason != "deadline_exceeded" || finish.ContextState != "active" || finish.HTTPStatus != 0 ||
			finish.DurationMS == nil || *finish.DurationMS < 1900 || trace.GetConn == nil || trace.GotConn == nil ||
			trace.ConnectStart == nil || trace.ConnectDone == nil || trace.LastConnectResult != "ok" ||
			trace.DialEnter == nil || trace.DialReturn == nil || trace.LastDialResult != "ok" ||
			trace.WroteRequest == nil || trace.LastWriteResult != "ok" || trace.FirstResponseByte != nil {
			t.Fatalf("before-headers timeout observations = %+v trace=%+v", finish, trace)
		}
		select {
		case <-joined:
		case <-ctx.Done():
			t.Fatal("timed-out local HTTP handler did not join", ctx.Err())
		}
	})
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

func operatorMetricsEndpointsForTest() []metricsEndpoint {
	return []metricsEndpoint{
		{url: "http://localhost:9390/metrics", endpoint: "localhost:9390"},
		{url: "http://localhost:9391/metrics", endpoint: "localhost:9391"},
		{url: "http://localhost:9392/metrics", endpoint: "localhost:9392"},
	}
}

func TestOperatorMetricsLoopbackRoutes(t *testing.T) {
	t.Parallel()
	endpoints := operatorMetricsEndpointsForTest()
	routes, err := operatorMetricsLoopbackRoutes(endpoints)
	if err != nil || len(routes) != receiptReplicaCount || routes["localhost:9390"] != "127.0.0.1:9390" || routes["localhost:9392"] != "127.0.0.1:9392" {
		t.Fatalf("owned routes = %v, %v", routes, err)
	}
	for _, test := range []struct {
		name string
		url  string
	}{
		{"remote name", "http://node.invalid:9390/metrics"},
		{"hostname case", "http://LOCALHOST:9390/metrics"},
		{"hostname suffix", "http://localhost.:9390/metrics"},
		{"IPv4 standalone", "http://127.0.0.1:9390/metrics"},
		{"IPv6 standalone", "http://[::1]:9390/metrics"},
		{"TLS standalone", "https://localhost:9390/metrics"},
		{"zero port", "http://localhost:0/metrics"},
		{"oversized port", "http://localhost:65536/metrics"},
		{"noncanonical port", "http://localhost:09390/metrics"},
		{"missing port", "http://localhost/metrics"},
		{"wrong path", "http://localhost:9390/readyz"},
		{"escaped path", "http://localhost:9390/%6detrics"},
		{"query", "http://localhost:9390/metrics?x=y"},
		{"empty query", "http://localhost:9390/metrics?"},
		{"fragment", "http://localhost:9390/metrics#x"},
		{"userinfo", "http://user@localhost:9390/metrics"},
		{"opaque URL", "http:localhost:9390/metrics"},
		{"invalid URL", "http://localhost:bad/metrics"},
	} {
		t.Run(test.name, func(t *testing.T) {
			endpoints := operatorMetricsEndpointsForTest()
			endpoints[0].url = test.url
			if _, err := operatorMetricsLoopbackRoutes(endpoints); err == nil {
				t.Fatal("accepted a non-owned or noncanonical route")
			}
		})
	}
	t.Run("report identity mismatch", func(t *testing.T) {
		endpoints := operatorMetricsEndpointsForTest()
		endpoints[0].endpoint = "localhost:9391"
		if _, err := operatorMetricsLoopbackRoutes(endpoints); err == nil {
			t.Fatal("accepted a different reported node")
		}
	})
	t.Run("duplicate port", func(t *testing.T) {
		endpoints := operatorMetricsEndpointsForTest()
		endpoints[1] = endpoints[0]
		if _, err := operatorMetricsLoopbackRoutes(endpoints); err == nil {
			t.Fatal("accepted duplicate local destinations")
		}
	})
	for _, count := range []int{0, 1, 2, 4} {
		t.Run(fmt.Sprintf("replica count %d", count), func(t *testing.T) {
			endpoints := append(operatorMetricsEndpointsForTest(), metricsEndpoint{})[:count]
			if _, err := operatorMetricsLoopbackRoutes(endpoints); err == nil {
				t.Fatal("accepted an incomplete or expanded cohort")
			}
		})
	}
}

func TestConfigureOperatorMetricsLoopbackPreservesDialOwnership(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	conn, peer := net.Pipe()
	t.Cleanup(func() {
		if err := conn.Close(); err != nil {
			t.Error(err)
		}
		if err := peer.Close(); err != nil {
			t.Error(err)
		}
	})
	wantErr := errors.New("original dial result")
	var gotCtx context.Context
	var gotNetwork, gotAddress string
	var calls int
	request, err := http.NewRequest(http.MethodGet, "http://localhost:9390/metrics", nil)
	if err != nil {
		t.Fatal(err)
	}
	var proxyRequest *http.Request
	transport := &http.Transport{
		Proxy: func(r *http.Request) (*url.URL, error) { proxyRequest = r; return nil, nil },
		DialContext: func(ctx context.Context, network, address string) (net.Conn, error) {
			calls++
			gotCtx, gotNetwork, gotAddress = ctx, network, address
			return conn, wantErr
		},
	}
	endpoints := operatorMetricsEndpointsForTest()
	if err := configureOperatorMetricsLoopback(transport, endpoints); err != nil {
		t.Fatal(err)
	}
	// The installed routing table does not retain the caller's mutable slice.
	endpoints[0] = metricsEndpoint{url: "http://elsewhere.invalid:1/metrics", endpoint: "elsewhere.invalid:1"}
	for _, test := range []struct{ network, address, want string }{
		{"tcp", "localhost:9390", "127.0.0.1:9390"},
		{"tcp4", "localhost:9391", "127.0.0.1:9391"},
		{"tcp", "localhost:9392", "127.0.0.1:9392"},
		{"tcp6", "localhost:9390", "localhost:9390"},
		{"udp", "localhost:9390", "localhost:9390"},
		{"tcp", "localhost:9393", "localhost:9393"},
		{"tcp", "LOCALHOST:9390", "LOCALHOST:9390"},
		{"tcp", "proxy.invalid:3128", "proxy.invalid:3128"},
		{"tcp", "127.0.0.1:9390", "127.0.0.1:9390"},
		{"tcp", "[::1]:9390", "[::1]:9390"},
	} {
		before := calls
		gotConn, err := transport.DialContext(ctx, test.network, test.address)
		if calls != before+1 || gotCtx != ctx || gotNetwork != test.network || gotAddress != test.want || gotConn != conn || err != wantErr || ctx.Err() != context.Canceled {
			t.Fatalf("dial ownership changed for %s/%s: calls=%d ctx=%t network=%s address=%s conn=%t err=%v", test.network, test.address, calls-before, gotCtx == ctx, gotNetwork, gotAddress, gotConn == conn, err)
		}
	}
	if _, err := transport.Proxy(request); err != nil || proxyRequest != request || request.URL.String() != "http://localhost:9390/metrics" || request.Host != "localhost:9390" {
		t.Fatal("operator route changed proxy callback or logical request identity")
	}
	t.Run("missing context dialer is not replaced", func(t *testing.T) {
		var legacyCalls int
		legacy := &http.Transport{Dial: func(_, _ string) (net.Conn, error) { legacyCalls++; return conn, wantErr }} //nolint:staticcheck // SA1019: intentional legacy-Dial regression control.
		if err := configureOperatorMetricsLoopback(legacy, operatorMetricsEndpointsForTest()); err == nil || legacy.DialContext != nil {
			t.Fatal("installed a new dialer instead of rejecting an unsupported owned route")
		}
		gotConn, err := legacy.Dial("tcp", "localhost:9390") //nolint:staticcheck // SA1019: verify the rejected configuration preserves the legacy fallback.
		if legacyCalls != 1 || gotConn != conn || err != wantErr {
			t.Fatal("configuration failure mutated the legacy dialer")
		}
	})
}

func TestSampleSteadyMetricsOperatorLoopbackWithoutNameResolution(t *testing.T) {
	// This sequential control owns the process default. No external resolver is
	// called: the original dialer rejects names and accepts literal loopback.
	transport := runtimeMetricsDefaultTransportForTest(t)
	var mu sync.Mutex
	var dialAddresses []string
	var proxyURLs []string
	var badRequest atomic.Bool
	transport.Proxy = func(request *http.Request) (*url.URL, error) {
		mu.Lock()
		defer mu.Unlock()
		proxyURLs = append(proxyURLs, request.URL.String())
		return nil, nil
	}
	var dialer net.Dialer
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		mu.Lock()
		dialAddresses = append(dialAddresses, address)
		mu.Unlock()
		host, _, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			return nil, errors.New("controlled name resolution unavailable")
		}
		return dialer.DialContext(ctx, network, address)
	}
	var endpoints []metricsEndpoint
	var addresses []string
	var requests [receiptReplicaCount]atomic.Int32
	stop := make(chan struct{})
	for i := range receiptReplicaCount {
		var logicalHost string
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
			if request.Method != http.MethodGet || request.URL.Path != "/metrics" || request.Host != logicalHost || request.Header.Get("Authorization") != "" {
				badRequest.Store(true)
			}
			// The owned Python bridge closes every HTTP/1.0 connection, so
			// exercise the literal route again on each sampling round.
			w.Header().Set("Connection", "close")
			_, _ = fmt.Fprintf(w, "go_goroutines %d\ngo_memstats_heap_alloc_bytes %d\n%s", 40+i, testHeapBaseline+int64(i), testRuntimeObservations)
			if requests[i].Add(1) == 2 && i == receiptReplicaCount-1 {
				close(stop)
			}
		}))
		t.Cleanup(server.Close)
		address := strings.TrimPrefix(server.URL, "http://")
		host, port, err := net.SplitHostPort(address)
		if err != nil || host != "127.0.0.1" {
			t.Fatal("control must own an IPv4 loopback listener", err)
		}
		logicalHost = net.JoinHostPort("localhost", port)
		endpoints = append(endpoints, metricsEndpoint{url: "http://" + logicalHost + "/metrics", endpoint: logicalHost})
		addresses = append(addresses, address)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	cfg := steadyMetricsConfig{endpoints: endpoints, interval: 10 * time.Millisecond}
	negative := sampleSteadyMetrics(ctx, cfg, time.Now(), stop)
	if negative.Failure == "" || len(negative.Samples) != 0 || requests[0].Load() != 0 || requests[1].Load() != 0 || requests[2].Load() != 0 {
		t.Fatal("default collector bypassed the controlled unavailable name route")
	}
	mu.Lock()
	if len(dialAddresses) != 1 || dialAddresses[0] != endpoints[0].endpoint {
		t.Fatalf("default collector changed its resolver destination: %v", dialAddresses)
	}
	dialAddresses, proxyURLs = nil, nil
	mu.Unlock()
	cfg.operatorLoopback = true
	report := sampleSteadyMetrics(ctx, cfg, time.Now(), stop)
	if report.Failure != "" || len(report.Samples) != 2 || badRequest.Load() || ctx.Err() != nil {
		t.Fatalf("owned loopback control: failure=%t rounds=%d identityChanged=%t caller=%v", report.Failure != "", len(report.Samples), badRequest.Load(), ctx.Err())
	}
	for i, endpoint := range endpoints {
		if report.Endpoints[i] != endpoint.endpoint || negative.Endpoints[i] != endpoint.endpoint || requests[i].Load() != 2 {
			t.Fatalf("configured node %d identity/count changed", i)
		}
		for _, round := range report.Samples {
			value := round.Replicas[i]
			if value.Endpoint != endpoint.endpoint || value.Goroutines != int64(40+i) || value.HeapAllocBytes != testHeapBaseline+int64(i) || value.Metrics == nil || value.Metrics.GCCount != 7 {
				t.Fatalf("configured node %d measurement changed: %+v", i, value)
			}
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(dialAddresses) != 6 || len(proxyURLs) != 6 {
		t.Fatalf("fresh connections or proxy callbacks changed: dials=%d proxy=%d", len(dialAddresses), len(proxyURLs))
	}
	for i := range dialAddresses {
		if dialAddresses[i] != addresses[i%receiptReplicaCount] || proxyURLs[i] != endpoints[i%receiptReplicaCount].url {
			t.Fatalf("request %d reached a different node or logical URL", i)
		}
	}
}

func retainedHeapFixture(t *testing.T) (string, string, []receiptLeakReplica) {
	t.Helper()
	if !retainedEvidenceSupported {
		t.Skip("new retained artifact I/O requires Linux or Darwin; unsupported measurement refusal is tested separately")
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	paths := []string{filepath.Join(root, "retained_heap_pre.json"), filepath.Join(root, "retained_heap_post.json")}
	endpoints := []receiptLeakReplica{{Endpoint: "localhost:9390"}, {Endpoint: "localhost:9391"}, {Endpoint: "localhost:9392"}}
	for phaseIndex, phase := range []string{"baseline", "post"} {
		count := int64(1000)
		if phaseIndex == 1 {
			count = 5500
		}
		cut := retainedHeapCut{Schema: 1, Protocol: benchheap.Protocol, Phase: phase, Status: "complete", ControllerStartNS: int64(phaseIndex+1) * 100_000_000_000, ControllerEndNS: int64(phaseIndex+1)*100_000_000_000 + 1_000_000_000}
		cut.Context = retainedHeapContext{RunID: "owned-case", SourceCommit: strings.Repeat("a", 40), SourceTree: strings.Repeat("b", 40), ImageID: "sha256:" + strings.Repeat("c", 64), ScenarioSHA256: strings.Repeat("d", 64), RuntimeUser: "501:20", CPUSet: "0-3"}
		cut.Context.Workload.Admission, cut.Context.Workload.Lookup = count, count
		for i, endpoint := range endpoints {
			dir := filepath.Join(root, "heap-control-evidence", fmt.Sprintf("lantern-%d", i))
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
			session := benchheap.Session{Schema: 1, Protocol: benchheap.Protocol, RunNonce: strings.Repeat("e", 64), SourceCommit: cut.Context.SourceCommit, SourceTree: cut.Context.SourceTree, ImageID: cut.Context.ImageID, ContainerID: fmt.Sprintf("%064x", i+1), BinarySHA256: strings.Repeat("f", 64), UID: 501}
			identity := benchheap.Identity{Session: session, PID: 1, StartNonce: fmt.Sprintf("%064x", i+10)}
			request := benchheap.Request{Schema: 1, Protocol: benchheap.Protocol, Identity: identity, Phase: phase, Ordinal: phaseIndex + 1, CutNonce: fmt.Sprintf("%064x", phaseIndex*3+i+100), ExpectedReceipts: count}
			native := benchheap.Native{Generation: uint64(count), LogSequence: uint64(count), LogLength: int(count), LogCapacity: 10000, ReceiptEntries: int(count), ReceiptBytes: uint64(count * 256), ReceiptHighWaterMS: 1000, ReceiptOldestDeadlineMS: 3_601_000, Public: benchheap.Counter{Begun: uint64(count * 2), Completed: uint64(count * 2)}, PeerSubscribe: benchheap.Counter{Begun: 2, Active: 2}}
			for origin := 1; origin <= 3; origin++ {
				native.Origins = append(native.Origins, benchheap.Origin{ID: fmt.Sprintf("%032x", origin), Sequence: uint64(count), WallNS: 1793000000000000000})
			}
			memory := func(gc, forced uint32) benchheap.Memory {
				return benchheap.Memory{HeapAlloc: uint64(100+phaseIndex) * 1 << 20, HeapObjects: 500, HeapInuse: 110 << 20, NumGC: gc, NumForcedGC: forced, LastGC: uint64(gc) * 1_000_000, NextGC: 200 << 20, MarkedLive: 90 << 20, MetricCycles: uint64(gc), MetricForced: uint64(forced), CycleAfter: gc}
			}
			gc := uint32(10 + phaseIndex*10)
			forced := uint32(phaseIndex * 2)
			completion := benchheap.Completion{Schema: 1, Protocol: benchheap.Protocol, Request: request, Status: "complete", ElapsedNS: 900_000_000, Before: native, After: native,
				Collections: []benchheap.GC{{StartedNS: 300_000_000, ReturnedNS: 400_000_000, Before: memory(gc, forced), After: memory(gc+1, forced+1)}, {StartedNS: 500_000_000, ReturnedNS: 600_000_000, Before: memory(gc+1, forced+1), After: memory(gc+2, forced+2)}}, Memory: memory(gc+2, forced+2)}
			node := retainedHeapNode{Endpoint: endpoint.Endpoint, Completion: completion}
			node.MountBindingFile, node.MountInspectBeforeFile, node.MountInspectAfterFile = retainedMountFixture(t, root, phase, i, cut.Context, session)
			for _, file := range []struct {
				name  string
				value any
				ref   *retainedHeapReference
			}{
				{benchheap.SessionFile, session, &node.SessionFile}, {benchheap.ReadyFile, benchheap.Ready{Schema: 1, Protocol: benchheap.Protocol, Identity: identity}, &node.ReadyFile}, {benchheap.RequestFile(phase), request, &node.RequestFile}, {benchheap.StartedFile(phase), benchheap.Started{Request: request}, &node.StartedFile}, {benchheap.CompletionFile(phase), completion, &node.CompletionFile},
			} {
				path := filepath.Join(dir, file.name)
				mustWriteReceiptArtifact(t, path, file.value)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatal(err)
				}
				*file.ref = retainedReference(path, raw)
			}
			cut.Replicas = append(cut.Replicas, node)
		}
		mustWriteReceiptArtifact(t, paths[phaseIndex], cut)
	}
	return paths[0], paths[1], endpoints
}

func TestRetainedHeapRequiresSymmetricCompletedIdentityBoundCuts(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name   string
		damage func(*retainedHeapCut)
		raw    func(*testing.T, string)
		want   string
	}{
		{name: "complete", want: "pass"},
		{name: "expired controller", damage: func(c *retainedHeapCut) { c.ControllerEndNS = c.ControllerStartNS + 31_000_000_000 }, want: "incomplete"},
		{name: "partial controller", damage: func(c *retainedHeapCut) { c.Status = "incomplete" }, want: "incomplete"},
		{name: "missing replica", damage: func(c *retainedHeapCut) { c.Replicas = c.Replicas[:2] }, want: "incomplete"},
		{name: "wrong phase", damage: func(c *retainedHeapCut) { c.Phase = "baseline" }, want: "incomplete"},
		{name: "only one collection", damage: func(c *retainedHeapCut) {
			c.Replicas[0].Completion.Collections = c.Replicas[0].Completion.Collections[:1]
		}, want: "incomplete"},
		{name: "GC return missing", damage: func(c *retainedHeapCut) { c.Replicas[0].Completion.Collections[1].ReturnedNS = 0 }, want: "incomplete"},
		{name: "extra forced collection", damage: func(c *retainedHeapCut) { c.Replicas[0].Completion.Collections[1].Before.NumForcedGC++ }, want: "incomplete"},
		{name: "cycle drift", damage: func(c *retainedHeapCut) { c.Replicas[0].Completion.Memory.CycleAfter++ }, want: "incomplete"},
		{name: "native pending", damage: func(c *retainedHeapCut) { c.Replicas[0].Completion.Before.PendingCount = 1 }, want: "incomplete"},
		{name: "active response", damage: func(c *retainedHeapCut) { c.Replicas[0].Completion.Before.Public.Active = 1 }, want: "incomplete"},
		{name: "work not replicated", damage: func(c *retainedHeapCut) { c.Replicas[0].Completion.Before.ReceiptEntries = 1 }, want: "incomplete"},
		{name: "mixed image", damage: func(c *retainedHeapCut) { c.Context.ImageID = "sha256:" + strings.Repeat("a", 64) }, want: "incomplete"},
		{name: "mixed source", damage: func(c *retainedHeapCut) { c.Context.SourceTree = strings.Repeat("c", 40) }, want: "incomplete"},
		{name: "restarted target", damage: func(c *retainedHeapCut) {
			c.Replicas[0].Completion.Request.Identity.StartNonce = strings.Repeat("a", 64)
		}, want: "incomplete"},
		{name: "raw evidence changed", raw: func(t *testing.T, root string) {
			mustWriteReceiptArtifact(t, filepath.Join(root, "heap-control-evidence/lantern-0/post.started.json"), map[string]any{"request": "changed"})
		}, want: "incomplete"},
		{name: "refusal after completion", raw: func(t *testing.T, root string) {
			mustWriteReceiptArtifact(t, filepath.Join(root, "heap-control-evidence/lantern-0/refusal.json"), map[string]any{"reason": "extra_signal"})
		}, want: "incomplete"},
		{name: "raw evidence link", raw: func(t *testing.T, root string) {
			path := filepath.Join(root, "heap-control-evidence/lantern-0/post.started.json")
			if err := os.Rename(path, path+".held"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(path+".held", path); err != nil {
				t.Fatal(err)
			}
		}, want: "incomplete"},
	} {
		t.Run(test.name, func(t *testing.T) {
			pre, post, endpoints := retainedHeapFixture(t)
			if test.damage != nil {
				var cut retainedHeapCut
				data, err := os.ReadFile(post)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(data, &cut); err != nil {
					t.Fatal(err)
				}
				test.damage(&cut)
				mustWriteReceiptArtifact(t, post, cut)
			}
			if test.raw != nil {
				test.raw(t, filepath.Dir(post))
			}
			report := receiptLeakReport{SnapshotProtocol: benchheap.Protocol, Replicas: endpoints, NaturalChecksVerdict: "pass", NaturalPostHeapObservationVerdict: "pass", PairRequired: true, OverallMemoryVerdict: "requires_pair"}
			evaluateRetainedHeap(&report, filepath.Dir(pre), pre, post, 32)
			if report.RetainedVerdict != test.want || report.OverallMemoryVerdict != "requires_pair" || (report.Verdict == "pass") != (test.want == "pass") {
				t.Fatalf("unexpected retained scope/result: %+v", report)
			}
		})
	}
}

func TestRetainedHeapUnchanged32MiBBoundary(t *testing.T) {
	t.Parallel()
	for _, delta := range []int64{32 << 20, (32 << 20) + 1} {
		t.Run(fmt.Sprint(delta), func(t *testing.T) {
			pre, post, endpoints := retainedHeapFixture(t)
			var cut retainedHeapCut
			raw, err := os.ReadFile(post)
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(raw, &cut); err != nil {
				t.Fatal(err)
			}
			node := &cut.Replicas[0]
			node.Completion.Memory.HeapAlloc = uint64((100 << 20) + delta)
			node.Completion.Memory.HeapInuse = node.Completion.Memory.HeapAlloc + (10 << 20)
			node.Completion.Collections[1].After = node.Completion.Memory
			mustWriteReceiptArtifact(t, node.CompletionFile.Path, node.Completion)
			raw, err = os.ReadFile(node.CompletionFile.Path)
			if err != nil {
				t.Fatal(err)
			}
			node.CompletionFile = retainedReference(node.CompletionFile.Path, raw)
			mustWriteReceiptArtifact(t, post, cut)
			report := receiptLeakReport{SnapshotProtocol: benchheap.Protocol, Replicas: endpoints, NaturalChecksVerdict: "pass", NaturalPostHeapObservationVerdict: "pass", PairRequired: true, OverallMemoryVerdict: "requires_pair"}
			evaluateRetainedHeap(&report, filepath.Dir(pre), pre, post, 32)
			want := "pass"
			if delta > 32<<20 {
				want = "fail"
			}
			if report.RetainedVerdict != want || len(report.RetainedReplicas) != 3 || report.RetainedReplicas[0].HeapAllocDeltaBytes != delta {
				t.Fatalf("retained32 boundary changed: %+v", report)
			}
		})
	}
}

func TestPairedProtocolRejectsChangedThresholdsAndMissingGCInputs(t *testing.T) {
	t.Parallel()
	pre, post, steady, _ := receiptLeakFixture(t)
	for _, thresholds := range [][3]int{{16, 32, 40}, {15, 33, 40}, {15, 32, 41}} {
		got := evaluateReceiptLeakProtocol(pre, post, steady, 45*time.Second, 5*time.Second, thresholds[0], thresholds[1], thresholds[2], "natural_reference_v1")
		if got.Verdict != "fail" {
			t.Fatalf("changed paired limits accepted: %+v", got)
		}
	}
	out := filepath.Join(t.TempDir(), "missing-retained.json")
	code := runReceiptLeakEvaluation([]string{"-pre", pre, "-post", post, "-steady", steady, "-duration", "45s", "-interval", "5s", "-max-goroutines", "15", "-max-heap-mb", "32", "-max-steady-heap-mb", "40", "-snapshot-protocol", benchheap.Protocol, "-out", out})
	if code != 1 {
		t.Fatalf("missing GC receipts returned%d", code)
	}
}
