package main

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"math"
	"math/big"
	"net"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const receiptReplicaCount = 3

type metricsEndpoint struct {
	url      string
	endpoint string
}

type steadyMetricsConfig struct {
	endpoints        []metricsEndpoint
	interval         time.Duration
	operatorLoopback bool
}

type runtimeMetrics struct {
	Endpoint       string                     `json:"endpoint"`
	Goroutines     int64                      `json:"goroutines"`
	HeapAllocBytes int64                      `json:"heap_alloc_bytes"`
	Metrics        *runtimeMetricObservations `json:"metrics,omitempty"`
}

// runtimeMetricObservations preserves a fixed, unlabelled set from the same
// scrape as HeapAlloc. These observations explain sample context; they do not
// alter the memory gate or certify a post-GC live heap. Old saved reports may
// omit this object and retain their original HeapAlloc/goroutine evaluation.
type runtimeMetricObservations struct {
	GCCount                 int64   `json:"go_gc_duration_seconds_count"`
	LastGCTimeSeconds       float64 `json:"go_memstats_last_gc_time_seconds"`
	NextGCBytes             int64   `json:"go_memstats_next_gc_bytes"`
	ReceiptEntries          int64   `json:"lantern_receipt_entries"`
	ReceiptBytes            int64   `json:"lantern_receipt_bytes"`
	ReceiptOldestDeadline   float64 `json:"lantern_receipt_oldest_deadline_seconds"`
	MutationLogCapacity     int64   `json:"lantern_mutation_log_capacity"`
	MutationLogFillRatio    float64 `json:"lantern_mutation_log_fill_ratio"`
	MutationLogEvictedTotal int64   `json:"lantern_mutation_log_evicted_total"`
}

type metricsRound struct {
	ElapsedMS int64            `json:"elapsed_ms"`
	Replicas  []runtimeMetrics `json:"replicas"`
}

type steadyMetricsReport struct {
	IntervalMS int64          `json:"interval_ms"`
	DurationMS int64          `json:"duration_ms"`
	Endpoints  []string       `json:"endpoints"`
	Samples    []metricsRound `json:"samples"`
	Failure    string         `json:"failure,omitempty"`
}

func parseRuntimeMetricsEndpoints(raw string) ([]metricsEndpoint, error) {
	parts := strings.Split(raw, ",")
	if len(parts) != receiptReplicaCount {
		return nil, fmt.Errorf("want %d replica metrics URLs, got %d", receiptReplicaCount, len(parts))
	}
	endpoints := make([]metricsEndpoint, 0, receiptReplicaCount)
	seen := make(map[string]bool, receiptReplicaCount)
	for _, part := range parts {
		address := strings.TrimSpace(part)
		parsed, err := url.Parse(address)
		if err != nil {
			return nil, fmt.Errorf("invalid metrics URL %q: %w", address, err)
		}
		if parsed.Scheme != "http" || parsed.Hostname() == "" || parsed.Port() == "" ||
			parsed.Path != "/metrics" || parsed.RawQuery != "" || parsed.Fragment != "" ||
			parsed.User != nil {
			return nil, fmt.Errorf("metrics URL %q must be http://host:port/metrics", address)
		}
		if seen[parsed.Host] {
			return nil, fmt.Errorf("duplicate metrics endpoint %q", parsed.Host)
		}
		seen[parsed.Host] = true
		endpoints = append(endpoints, metricsEndpoint{url: address, endpoint: parsed.Host})
	}
	return endpoints, nil
}

// Only the owned operator bridge is known to listen on IPv4 loopback. Keep the
// logical URL/Host and any proxy selection intact; change only the exact socket
// addresses supplied to this sampler's existing context dialer. Standalone
// collectors, including IPv6-only localhost listeners, keep their normal route.
func operatorMetricsLoopbackRoutes(endpoints []metricsEndpoint) (map[string]string, error) {
	if len(endpoints) != receiptReplicaCount {
		return nil, errors.New("operator metrics require three localhost endpoints")
	}
	routes := make(map[string]string, receiptReplicaCount)
	for _, endpoint := range endpoints {
		u, err := url.Parse(endpoint.url)
		if err != nil {
			return nil, errors.New("operator metrics require canonical localhost HTTP metrics URLs")
		}
		port, err := strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 || u.Port() != strconv.Itoa(port) ||
			u.Scheme != "http" || u.Host != "localhost:"+u.Port() || endpoint.endpoint != u.Host ||
			u.Path != "/metrics" || u.RawPath != "" || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.User != nil || u.Opaque != "" || u.String() != endpoint.url {
			return nil, errors.New("operator metrics require canonical localhost HTTP metrics URLs")
		}
		if _, duplicate := routes[u.Host]; duplicate {
			return nil, errors.New("operator metrics require distinct local ports")
		}
		routes[u.Host] = net.JoinHostPort("127.0.0.1", u.Port())
	}
	return routes, nil
}

func configureOperatorMetricsLoopback(transport *http.Transport, endpoints []metricsEndpoint) error {
	routes, err := operatorMetricsLoopbackRoutes(endpoints)
	if err != nil {
		return err
	}
	if transport.DialContext == nil {
		return errors.New("operator metrics require an existing context dialer")
	}
	dial := transport.DialContext
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network == "tcp" || network == "tcp4" {
			if loopback, ok := routes[address]; ok {
				address = loopback
			}
		}
		return dial(ctx, network, address)
	}
	return nil
}

func sampleSteadyMetrics(
	ctx context.Context,
	cfg steadyMetricsConfig,
	start time.Time,
	stop <-chan struct{},
) steadyMetricsReport {
	report := steadyMetricsReport{
		IntervalMS: cfg.interval.Milliseconds(),
		Endpoints:  make([]string, 0, len(cfg.endpoints)),
		Samples:    make([]metricsRound, 0),
	}
	for _, endpoint := range cfg.endpoints {
		report.Endpoints = append(report.Endpoints, endpoint.endpoint)
	}
	baseTransport, ok := http.DefaultTransport.(*http.Transport)
	if !ok || baseTransport == nil {
		report.Failure = "metrics requires a configurable HTTP transport"
		return report
	}
	// Own only this sampling flight's pool and unused dials. Request timeout
	// alone permits a Transport-owned dial to continue for a future request.
	transport := baseTransport.Clone()
	if cfg.operatorLoopback {
		if err := configureOperatorMetricsLoopback(transport, cfg.endpoints); err != nil {
			report.Failure = err.Error()
			return report
		}
	}
	observeRuntimeMetricsDial(transport)
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport}
	ticker := time.NewTicker(cfg.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return report
		case <-ctx.Done():
			report.Failure = fmt.Sprintf("sampling context ended: %v", ctx.Err())
			return report
		default:
		}

		round := metricsRound{
			ElapsedMS: time.Since(start).Milliseconds(),
			Replicas:  make([]runtimeMetrics, 0, len(cfg.endpoints)),
		}
		for i, endpoint := range cfg.endpoints {
			value, err := scrapeRuntimeMetricsObserved(ctx, client, endpoint, start, i, os.Stderr)
			if err != nil {
				report.Failure = err.Error()
				return report
			}
			round.Replicas = append(round.Replicas, value)
		}
		report.Samples = append(report.Samples, round)
		select {
		case <-stop:
			return report
		case <-ctx.Done():
			report.Failure = fmt.Sprintf("sampling context ended: %v", ctx.Err())
			return report
		case <-ticker.C:
		}
	}
}

func scrapeRuntimeMetrics(ctx context.Context, client *http.Client, endpoint metricsEndpoint) (runtimeMetrics, error) {
	return scrapeRuntimeMetricsObserved(ctx, client, endpoint, time.Now(), -1, os.Stderr)
}

// runtimeScrapeDiagnostic contains only bounded, content-free fields. The
// replica index identifies the configured ordering without exposing its URL.
// These stderr observations do not change the saved steady report or verdict.
type runtimeScrapeDiagnostic struct {
	Event        string                      `json:"event"`
	ReplicaIndex int                         `json:"replica_index"`
	UTC          string                      `json:"utc"`
	ElapsedMS    int64                       `json:"elapsed_ms"`
	DurationMS   *int64                      `json:"duration_ms,omitempty"`
	Stage        string                      `json:"stage"`
	Reason       string                      `json:"reason"`
	ContextState string                      `json:"context_state"`
	HTTPStatus   int                         `json:"http_status,omitempty"`
	HTTPTrace    *runtimeHTTPTraceDiagnostic `json:"http_trace,omitempty"`
}

// scrapeRuntimeMetricsObserved records one start and one finish for the same
// request. Injecting the diagnostic writer keeps tests independent of stderr;
// writer failures are deliberately excluded from the existing gate result.
func scrapeRuntimeMetricsObserved(
	ctx context.Context,
	client *http.Client,
	endpoint metricsEndpoint,
	steadyStart time.Time,
	replicaIndex int,
	diagnostics io.Writer,
) (runtimeMetrics, error) {
	if replicaIndex < 0 || replicaIndex >= receiptReplicaCount {
		replicaIndex = -1
	}
	started := time.Now()
	trace := &runtimeHTTPTrace{started: started}
	var traceSummary *runtimeHTTPTraceDiagnostic
	stage, reason, status := "request", "invalid_request", 0
	write := func(event string, at time.Time, duration *int64) {
		state := "active"
		if errors.Is(ctx.Err(), context.Canceled) {
			state = "canceled"
		} else if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			state = "deadline_exceeded"
		}
		_ = json.NewEncoder(diagnostics).Encode(runtimeScrapeDiagnostic{
			Event: event, ReplicaIndex: replicaIndex,
			UTC: at.UTC().Format(time.RFC3339Nano), ElapsedMS: max(0, at.Sub(steadyStart).Milliseconds()),
			DurationMS: duration, Stage: stage, Reason: reason, ContextState: state, HTTPStatus: status,
			HTTPTrace: traceSummary,
		})
	}
	stage, reason = "request", "started"
	write("request_start", started, nil)
	reason = "invalid_request"
	defer func() {
		traceSummary = trace.finish()
		finished := time.Now()
		duration := max(0, finished.Sub(started).Milliseconds())
		write("request_finish", finished, &duration)
	}()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
	if err != nil {
		return runtimeMetrics{}, err
	}
	stage, reason = "http", "transport_error"
	traceContext := context.WithValue(request.Context(), runtimeHTTPTraceContextKey{}, trace)
	request = request.WithContext(httptrace.WithClientTrace(traceContext, trace.callbacks()))
	response, err := client.Do(request)
	if err != nil {
		reason = runtimeScrapeErrorReason(err, reason)
		return runtimeMetrics{}, fmt.Errorf("scrape %s: %w", endpoint.endpoint, err)
	}
	status = response.StatusCode
	if response.StatusCode != http.StatusOK {
		stage, reason = "status", "http_status"
		closeErr := response.Body.Close()
		if closeErr != nil {
			reason = "http_status_close_error"
			return runtimeMetrics{}, fmt.Errorf("scrape %s: HTTP %s; close body: %w", endpoint.endpoint, response.Status, closeErr)
		}
		return runtimeMetrics{}, fmt.Errorf("scrape %s: HTTP %s", endpoint.endpoint, response.Status)
	}
	stage, reason = "parse", "parse_error"
	value, parseErr := parseRuntimeMetrics(response.Body)
	closeErr := response.Body.Close()
	if parseErr != nil {
		reason = runtimeScrapeErrorReason(parseErr, reason)
		return runtimeMetrics{}, fmt.Errorf("scrape %s: %w", endpoint.endpoint, parseErr)
	}
	if closeErr != nil {
		stage, reason = "close", runtimeScrapeErrorReason(closeErr, "close_error")
		return runtimeMetrics{}, fmt.Errorf("close %s metrics: %w", endpoint.endpoint, closeErr)
	}
	stage, reason = "complete", "ok"
	value.Endpoint = endpoint.endpoint
	return value, nil
}

func runtimeScrapeErrorReason(err error, fallback string) string {
	if errors.Is(err, context.Canceled) {
		return "canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return "timeout"
	}
	return fallback
}

func parseRuntimeMetrics(body io.Reader) (runtimeMetrics, error) {
	limited := &io.LimitedReader{R: body, N: (16 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	result := runtimeMetrics{Metrics: &runtimeMetricObservations{}}
	seen := make(map[string]bool, 11)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		name, _, labelled := strings.Cut(fields[0], "{")
		switch name {
		case "go_goroutines", "go_memstats_heap_alloc_bytes",
			"go_gc_duration_seconds_count", "go_memstats_next_gc_bytes",
			"lantern_receipt_entries", "lantern_receipt_bytes",
			"lantern_mutation_log_capacity", "lantern_mutation_log_evicted_total",
			"go_memstats_last_gc_time_seconds", "lantern_receipt_oldest_deadline_seconds",
			"lantern_mutation_log_fill_ratio":
			if labelled || len(fields) != 2 {
				return runtimeMetrics{}, fmt.Errorf("malformed %s sample", name)
			}
			if seen[name] {
				return runtimeMetrics{}, fmt.Errorf("duplicate %s sample", name)
			}
			seen[name] = true
			if err := result.setMetricObservation(name, fields[1]); err != nil {
				return runtimeMetrics{}, fmt.Errorf("invalid %s sample: %w", name, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return runtimeMetrics{}, fmt.Errorf("read metrics: %w", err)
	}
	if limited.N == 0 {
		return runtimeMetrics{}, errors.New("metrics body exceeds 16 MiB")
	}
	if len(seen) != 11 {
		return runtimeMetrics{}, errors.New("missing required runtime or receipt diagnostic metric")
	}
	return result, nil
}

func (r *runtimeMetrics) setMetricObservation(name, raw string) error {
	var integer *int64
	var fraction *float64
	switch name {
	case "go_goroutines":
		value, err := parseMetricValue(raw)
		r.Goroutines = value
		return err
	case "go_memstats_heap_alloc_bytes":
		value, err := parseMetricValue(raw)
		r.HeapAllocBytes = value
		return err
	case "go_gc_duration_seconds_count":
		integer = &r.Metrics.GCCount
	case "go_memstats_next_gc_bytes":
		integer = &r.Metrics.NextGCBytes
	case "lantern_receipt_entries":
		integer = &r.Metrics.ReceiptEntries
	case "lantern_receipt_bytes":
		integer = &r.Metrics.ReceiptBytes
	case "lantern_mutation_log_capacity":
		integer = &r.Metrics.MutationLogCapacity
	case "lantern_mutation_log_evicted_total":
		integer = &r.Metrics.MutationLogEvictedTotal
	case "go_memstats_last_gc_time_seconds":
		fraction = &r.Metrics.LastGCTimeSeconds
	case "lantern_receipt_oldest_deadline_seconds":
		fraction = &r.Metrics.ReceiptOldestDeadline
	case "lantern_mutation_log_fill_ratio":
		fraction = &r.Metrics.MutationLogFillRatio
	default:
		return errors.New("unknown runtime metric")
	}
	if integer != nil {
		value, err := parseNonnegativeMetricValue(raw)
		*integer = value
		return err
	}
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 ||
		(value == 0 && !zeroMetricMantissa(raw)) || (name == "lantern_mutation_log_fill_ratio" && value > 1) {
		return errors.New("expected a finite nonnegative timestamp or a fill ratio in [0,1]")
	}
	if name == "lantern_mutation_log_fill_ratio" {
		exact, ok := new(big.Rat).SetString(raw)
		if !ok || exact.Sign() < 0 || exact.Cmp(big.NewRat(1, 1)) > 0 {
			return errors.New("expected a fill ratio in [0,1]")
		}
	}
	*fraction = value
	return nil
}

func parseNonnegativeMetricValue(raw string) (int64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
		return 0, errors.New("expected a finite nonnegative integer")
	}
	if value == 0 {
		// Do not send an underflowing exponent to big.Rat. Only an exactly zero
		// mantissa represents zero; a positive fraction rounded to zero is invalid.
		if zeroMetricMantissa(raw) {
			return 0, nil
		}
		return 0, errors.New("expected an exact nonnegative integer")
	}
	exact, ok := new(big.Rat).SetString(raw)
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, errors.New("expected an exact nonnegative integer")
	}
	return exact.Num().Int64(), nil
}

func zeroMetricMantissa(raw string) bool {
	mantissa, _, _ := strings.Cut(strings.ToLower(raw), "e")
	return strings.Trim(mantissa, "+-0.") == ""
}

func parseMetricValue(raw string) (int64, error) {
	value, err := strconv.ParseFloat(raw, 64)
	if err != nil || math.IsNaN(value) || math.IsInf(value, 0) ||
		value <= 0 {
		return 0, fmt.Errorf("expected a finite positive integer, got %q", raw)
	}
	// Float parsing can round away a fractional suffix before the integer check.
	exact, ok := new(big.Rat).SetString(raw)
	if !ok || !exact.IsInt() || !exact.Num().IsInt64() {
		return 0, fmt.Errorf("expected a finite positive integer, got %q", raw)
	}
	return exact.Num().Int64(), nil
}

func validateSteadyMetrics(report steadyMetricsReport, cfg steadyMetricsConfig, duration time.Duration) error {
	if report.Failure != "" {
		return errors.New(report.Failure)
	}
	if cfg.interval <= 0 || duration < cfg.interval || len(cfg.endpoints) != receiptReplicaCount ||
		report.IntervalMS != cfg.interval.Milliseconds() ||
		report.DurationMS < duration.Milliseconds() ||
		report.DurationMS > (duration+2*cfg.interval).Milliseconds() {
		return errors.New("incomplete or unbounded steady sampling interval")
	}
	if len(report.Endpoints) != receiptReplicaCount {
		return errors.New("steady sampling omitted a replica endpoint")
	}
	for i, endpoint := range cfg.endpoints {
		if report.Endpoints[i] != endpoint.endpoint {
			return fmt.Errorf("steady sampling endpoint %d differs from baseline", i)
		}
	}
	if len(report.Samples) < int(duration/cfg.interval) {
		return fmt.Errorf("steady sampling recorded %d rounds, want at least %d",
			len(report.Samples), int(duration/cfg.interval))
	}
	maxGapMS := (cfg.interval + cfg.interval/2).Milliseconds()
	previous := int64(0)
	for i, round := range report.Samples {
		if round.ElapsedMS < 0 || round.ElapsedMS > report.DurationMS+maxGapMS ||
			(i > 0 && round.ElapsedMS <= previous) || round.ElapsedMS-previous > maxGapMS {
			return fmt.Errorf("steady sampling round %d exceeds the interval coverage bound", i)
		}
		if len(round.Replicas) != receiptReplicaCount {
			return fmt.Errorf("steady sampling round %d has %d replicas, want %d", i, len(round.Replicas), receiptReplicaCount)
		}
		for j, value := range round.Replicas {
			if value.Endpoint != cfg.endpoints[j].endpoint || value.Goroutines <= 0 || value.HeapAllocBytes <= 0 {
				return fmt.Errorf("steady sampling round %d replica %d is missing or invalid", i, j)
			}
		}
		previous = round.ElapsedMS
	}
	if report.DurationMS-previous > maxGapMS {
		return errors.New("steady sampling ended before the measured window completed")
	}
	return nil
}

type runtimeSnapshot struct {
	Endpoint           string `json:"endpoint"`
	Goroutines         *int64 `json:"goroutines"`
	HeapInuseBytes     int64  `json:"heap_inuse_bytes"`
	HeapAllocBytes     *int64 `json:"heap_alloc_bytes"`
	HeapObjects        int64  `json:"heap_objects"`
	VertexHLCEntries   int64  `json:"vertex_hlc_entries"`
	VertexHLCHighWater int64  `json:"vertex_hlc_high_water"`
}

type receiptLeakReplica struct {
	Endpoint             string `json:"endpoint"`
	GoroutinesPre        int64  `json:"goroutines_pre"`
	GoroutinesPost       int64  `json:"goroutines_post"`
	GoroutineDelta       int64  `json:"goroutine_delta"`
	GoroutinesPeak       int64  `json:"goroutines_peak"`
	GoroutinePeakDelta   int64  `json:"goroutine_peak_delta"`
	HeapInusePreBytes    int64  `json:"heap_inuse_pre_bytes"`
	HeapInusePostBytes   int64  `json:"heap_inuse_post_bytes"`
	HeapInuseDeltaBytes  int64  `json:"heap_inuse_delta_bytes"`
	HeapAllocPreBytes    int64  `json:"heap_alloc_pre_bytes"`
	HeapAllocPostBytes   int64  `json:"heap_alloc_post_bytes"`
	HeapAllocDeltaBytes  int64  `json:"heap_alloc_delta_bytes"`
	HeapAllocPeakBytes   int64  `json:"heap_alloc_peak_bytes"`
	HeapAllocPeakDelta   int64  `json:"heap_alloc_peak_delta_bytes"`
	HeapObjectsPre       int64  `json:"heap_objects_pre"`
	HeapObjectsPost      int64  `json:"heap_objects_post"`
	HeapObjectsDelta     int64  `json:"heap_objects_delta"`
	VertexHLCEntriesPre  int64  `json:"vertex_hlc_entries_pre"`
	VertexHLCEntriesPost int64  `json:"vertex_hlc_entries_post"`
	VertexHLCHighPre     int64  `json:"vertex_hlc_high_water_pre"`
	VertexHLCHighPost    int64  `json:"vertex_hlc_high_water_post"`
}

type receiptLeakReport struct {
	SnapshotProtocol string `json:"snapshot_protocol,omitempty"`
	Thresholds       struct {
		GoroutineMaxDelta         int `json:"goroutine_max_delta"`
		HeapAllocMaxDeltaMB       int `json:"heap_alloc_max_delta_mb"`
		SteadyHeapAllocMaxDeltaMB int `json:"steady_heap_alloc_max_delta_mb"`
	} `json:"thresholds"`
	SteadySampleInterval string               `json:"steady_sample_interval"`
	SteadySampleCount    int                  `json:"steady_sample_count"`
	Replicas             []receiptLeakReplica `json:"replicas"`
	Failures             []string             `json:"failures,omitempty"`
	Verdict              string               `json:"verdict"`
}

func runReceiptLeakEvaluation(args []string) int {
	fs := flag.NewFlagSet("evaluate-leak", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	pre := fs.String("pre", "", "post-warmup runtime snapshot")
	post := fs.String("post", "", "post-cooldown runtime snapshot")
	steady := fs.String("steady", "", "unforced steady runtime samples")
	duration := fs.Duration("duration", 0, "offered steady duration")
	interval := fs.Duration("interval", 0, "steady sampling interval")
	maxGoroutines := fs.Int("max-goroutines", 0, "maximum per-replica goroutine delta")
	maxHeapMB := fs.Int("max-heap-mb", 0, "maximum per-replica post-cooldown HeapAlloc delta in MiB")
	maxSteadyHeapMB := fs.Int("max-steady-heap-mb", 0, "maximum per-replica unforced steady heap_alloc peak delta in MiB")
	out := fs.String("out", "", "leak gate JSON output path")
	protocol := fs.String("snapshot-protocol", "forced_gc_minimum", "forced_gc_minimum | natural_gc_single")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pre == "" || *post == "" || *steady == "" || *out == "" {
		fs.Usage()
		return 2
	}
	if *protocol != "forced_gc_minimum" && *protocol != "natural_gc_single" {
		return 2
	}
	report := evaluateReceiptLeak(*pre, *post, *steady, *duration, *interval, *maxGoroutines, *maxHeapMB, *maxSteadyHeapMB)
	report.SnapshotProtocol = *protocol
	if err := writeReceiptArtifact(*out, report); err != nil {
		fmt.Fprintf(os.Stderr, "receipt leak gate: write report: %v\n", err)
		return 1
	}
	if report.Verdict != "pass" {
		fmt.Fprintf(os.Stderr, "receipt leak gate: %s\n", strings.Join(report.Failures, "; "))
		return 1
	}
	return 0
}

func evaluateReceiptLeak(
	prePath, postPath, steadyPath string,
	duration, interval time.Duration,
	maxGoroutines, maxHeapMB, maxSteadyHeapMB int,
) receiptLeakReport {
	report := receiptLeakReport{Verdict: "fail", SteadySampleInterval: interval.String()}
	report.Thresholds.GoroutineMaxDelta = maxGoroutines
	report.Thresholds.HeapAllocMaxDeltaMB = maxHeapMB
	report.Thresholds.SteadyHeapAllocMaxDeltaMB = maxSteadyHeapMB
	if maxGoroutines <= 0 || maxHeapMB <= 0 || maxSteadyHeapMB <= 0 ||
		int64(maxHeapMB) >= math.MaxInt64/(1<<20) || int64(maxSteadyHeapMB) >= math.MaxInt64/(1<<20) {
		report.Failures = append(report.Failures, "missing or invalid receipt leak thresholds")
		return report
	}
	pre, err := readRuntimeSnapshot(prePath)
	if err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("pre snapshot: %v", err))
		return report
	}
	post, err := readRuntimeSnapshot(postPath)
	if err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("post snapshot: %v", err))
		return report
	}
	data, err := os.ReadFile(steadyPath)
	if err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("steady samples: %v", err))
		return report
	}
	var steady steadyMetricsReport
	if err := json.Unmarshal(data, &steady); err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("parse steady samples: %v", err))
		return report
	}
	report.SteadySampleCount = len(steady.Samples)
	cfg := steadyMetricsConfig{interval: interval}
	for _, replica := range pre {
		cfg.endpoints = append(cfg.endpoints, metricsEndpoint{endpoint: replica.Endpoint})
	}
	if err := validateSteadyMetrics(steady, cfg, duration); err != nil {
		report.Failures = append(report.Failures, fmt.Sprintf("steady samples: %v", err))
		return report
	}
	for i := range pre {
		if pre[i].Endpoint != post[i].Endpoint {
			report.Failures = append(report.Failures, fmt.Sprintf("post snapshot replica %d differs from baseline", i))
			return report
		}
		p, q := pre[i], post[i]
		replica := receiptLeakReplica{
			Endpoint:             p.Endpoint,
			GoroutinesPre:        *p.Goroutines,
			GoroutinesPost:       *q.Goroutines,
			GoroutineDelta:       *q.Goroutines - *p.Goroutines,
			HeapInusePreBytes:    p.HeapInuseBytes,
			HeapInusePostBytes:   q.HeapInuseBytes,
			HeapInuseDeltaBytes:  q.HeapInuseBytes - p.HeapInuseBytes,
			HeapAllocPreBytes:    *p.HeapAllocBytes,
			HeapAllocPostBytes:   *q.HeapAllocBytes,
			HeapAllocDeltaBytes:  *q.HeapAllocBytes - *p.HeapAllocBytes,
			HeapObjectsPre:       p.HeapObjects,
			HeapObjectsPost:      q.HeapObjects,
			HeapObjectsDelta:     q.HeapObjects - p.HeapObjects,
			VertexHLCEntriesPre:  p.VertexHLCEntries,
			VertexHLCEntriesPost: q.VertexHLCEntries,
			VertexHLCHighPre:     p.VertexHLCHighWater,
			VertexHLCHighPost:    q.VertexHLCHighWater,
		}
		for _, round := range steady.Samples {
			value := round.Replicas[i]
			replica.GoroutinesPeak = max(replica.GoroutinesPeak, value.Goroutines)
			replica.HeapAllocPeakBytes = max(replica.HeapAllocPeakBytes, value.HeapAllocBytes)
		}
		replica.GoroutinePeakDelta = replica.GoroutinesPeak - replica.GoroutinesPre
		replica.HeapAllocPeakDelta = replica.HeapAllocPeakBytes - replica.HeapAllocPreBytes
		report.Replicas = append(report.Replicas, replica)
		if replica.GoroutineDelta > int64(maxGoroutines) || replica.GoroutinePeakDelta > int64(maxGoroutines) {
			report.Failures = append(report.Failures, fmt.Sprintf("%s goroutine post/steady growth exceeds +%d", p.Endpoint, maxGoroutines))
		}
		if replica.HeapAllocDeltaBytes > int64(maxHeapMB)<<20 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s post-cooldown HeapAlloc growth exceeds +%d MiB", p.Endpoint, maxHeapMB))
		}
		if replica.HeapAllocPeakDelta > int64(maxSteadyHeapMB)<<20 {
			report.Failures = append(report.Failures, fmt.Sprintf("%s steady heap_alloc peak growth exceeds +%d MiB", p.Endpoint, maxSteadyHeapMB))
		}
	}
	if len(report.Failures) == 0 {
		report.Verdict = "pass"
	}
	return report
}

func readRuntimeSnapshot(path string) ([]runtimeSnapshot, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var replicas []runtimeSnapshot
	if err := json.Unmarshal(data, &replicas); err != nil {
		return nil, err
	}
	if len(replicas) != receiptReplicaCount {
		return nil, fmt.Errorf("want %d replicas, got %d", receiptReplicaCount, len(replicas))
	}
	seen := make(map[string]bool, receiptReplicaCount)
	for i, replica := range replicas {
		if replica.Endpoint == "" || seen[replica.Endpoint] ||
			replica.Goroutines == nil || *replica.Goroutines <= 0 ||
			replica.HeapAllocBytes == nil || *replica.HeapAllocBytes <= 0 {
			return nil, fmt.Errorf("replica %d has missing, duplicate, or invalid runtime metrics", i)
		}
		seen[replica.Endpoint] = true
	}
	return replicas, nil
}

func writeReceiptArtifact(path string, value any) error {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
