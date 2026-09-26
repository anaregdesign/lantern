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
	"net/http"
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
	endpoints []metricsEndpoint
	interval  time.Duration
}

type runtimeMetrics struct {
	Endpoint       string `json:"endpoint"`
	Goroutines     int64  `json:"goroutines"`
	HeapAllocBytes int64  `json:"heap_alloc_bytes"`
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
	client := &http.Client{Timeout: 2 * time.Second}
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
		for _, endpoint := range cfg.endpoints {
			value, err := scrapeRuntimeMetrics(ctx, client, endpoint)
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
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.url, nil)
	if err != nil {
		return runtimeMetrics{}, err
	}
	response, err := client.Do(request)
	if err != nil {
		return runtimeMetrics{}, fmt.Errorf("scrape %s: %w", endpoint.endpoint, err)
	}
	if response.StatusCode != http.StatusOK {
		closeErr := response.Body.Close()
		if closeErr != nil {
			return runtimeMetrics{}, fmt.Errorf("scrape %s: HTTP %s; close body: %w", endpoint.endpoint, response.Status, closeErr)
		}
		return runtimeMetrics{}, fmt.Errorf("scrape %s: HTTP %s", endpoint.endpoint, response.Status)
	}
	value, parseErr := parseRuntimeMetrics(response.Body)
	closeErr := response.Body.Close()
	if parseErr != nil {
		return runtimeMetrics{}, fmt.Errorf("scrape %s: %w", endpoint.endpoint, parseErr)
	}
	if closeErr != nil {
		return runtimeMetrics{}, fmt.Errorf("close %s metrics: %w", endpoint.endpoint, closeErr)
	}
	value.Endpoint = endpoint.endpoint
	return value, nil
}

func parseRuntimeMetrics(body io.Reader) (runtimeMetrics, error) {
	limited := &io.LimitedReader{R: body, N: (16 << 20) + 1}
	scanner := bufio.NewScanner(limited)
	scanner.Buffer(make([]byte, 4096), 256<<10)
	var result runtimeMetrics
	seenGoroutines, seenHeap := false, false
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 {
			continue
		}
		switch fields[0] {
		case "go_goroutines", "go_memstats_heap_alloc_bytes":
			if len(fields) != 2 {
				return runtimeMetrics{}, fmt.Errorf("malformed %s sample", fields[0])
			}
			value, err := parseMetricValue(fields[1])
			if err != nil {
				return runtimeMetrics{}, fmt.Errorf("invalid %s sample: %w", fields[0], err)
			}
			if fields[0] == "go_goroutines" {
				if seenGoroutines {
					return runtimeMetrics{}, errors.New("duplicate go_goroutines sample")
				}
				seenGoroutines, result.Goroutines = true, value
			} else {
				if seenHeap {
					return runtimeMetrics{}, errors.New("duplicate go_memstats_heap_alloc_bytes sample")
				}
				seenHeap, result.HeapAllocBytes = true, value
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return runtimeMetrics{}, fmt.Errorf("read metrics: %w", err)
	}
	if limited.N == 0 {
		return runtimeMetrics{}, errors.New("metrics body exceeds 16 MiB")
	}
	if !seenGoroutines || !seenHeap {
		return runtimeMetrics{}, errors.New("missing go_goroutines or go_memstats_heap_alloc_bytes")
	}
	return result, nil
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
	Thresholds struct {
		GoroutineMaxDelta   int `json:"goroutine_max_delta"`
		HeapAllocMaxDeltaMB int `json:"heap_alloc_max_delta_mb"`
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
	maxHeapMB := fs.Int("max-heap-mb", 0, "maximum per-replica heap_alloc delta in MiB")
	out := fs.String("out", "", "leak gate JSON output path")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *pre == "" || *post == "" || *steady == "" || *out == "" {
		fs.Usage()
		return 2
	}
	report := evaluateReceiptLeak(*pre, *post, *steady, *duration, *interval, *maxGoroutines, *maxHeapMB)
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
	maxGoroutines, maxHeapMB int,
) receiptLeakReport {
	report := receiptLeakReport{Verdict: "fail", SteadySampleInterval: interval.String()}
	report.Thresholds.GoroutineMaxDelta = maxGoroutines
	report.Thresholds.HeapAllocMaxDeltaMB = maxHeapMB
	if maxGoroutines <= 0 || maxHeapMB <= 0 || int64(maxHeapMB) >= math.MaxInt64/(1<<20) {
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
		maxHeapBytes := int64(maxHeapMB) << 20
		if replica.HeapAllocDeltaBytes > maxHeapBytes || replica.HeapAllocPeakDelta > maxHeapBytes {
			report.Failures = append(report.Failures, fmt.Sprintf("%s heap_alloc post/steady growth exceeds +%d MiB", p.Endpoint, maxHeapMB))
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
