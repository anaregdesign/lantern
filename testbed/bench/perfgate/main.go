// Command perfgate evaluates the steady-state benchmark producers.
//
// The snapshot subcommand captures only the bounded Search terminal counter.
// The evaluate subcommand joins those counter deltas to ghz status totals so
// an explicitly allowed typed lifecycle outcome can be bounded independently
// from the unexpected non-OK budget.
package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"gopkg.in/yaml.v3"
)

const (
	searchCallsMetric      = "lantern_search_calls_total"
	indexIncompleteOutcome = "failed_precondition"
	indexIncompleteStatus  = "FailedPrecondition"
)

var (
	searchIndexIncomplete      = pb.SearchErrorReason_SEARCH_INDEX_INCOMPLETE.String()
	indexIncompleteMetricLabel = strings.ToLower(strings.TrimPrefix(searchIndexIncomplete, "SEARCH_"))
)

var requiredSearchLabels = []string{
	"fuzziness",
	"mode",
	"phrase",
	"prefix_present",
	"prefix_terms",
}

type scenarioDocument struct {
	Target struct {
		Endpoints []string       `yaml:"endpoints"`
		Call      string         `yaml:"call"`
		Calls     []scenarioCall `yaml:"calls"`
	} `yaml:"target"`
	PerfGate      perfGateConfig       `yaml:"perf_gate"`
	LifecycleGate *lifecycleGateConfig `yaml:"lifecycle_gate"`
}

type scenarioCall struct {
	Name string `yaml:"name"`
	Call string `yaml:"call"`
}

type perfGateConfig struct {
	MinSteadyRPSTotal *float64                     `yaml:"min_steady_rps_total"`
	MaxP99MS          *float64                     `yaml:"max_p99_ms"`
	MaxNonOKRatio     *float64                     `yaml:"max_non_ok_ratio"`
	Producers         map[string]producerThreshold `yaml:"producers"`
}

type producerThreshold struct {
	MinSteadyRPS  *float64 `yaml:"min_steady_rps" json:"min_steady_rps"`
	MaxP99MS      *float64 `yaml:"max_p99_ms" json:"max_p99_ms"`
	MaxNonOKRatio *float64 `yaml:"max_non_ok_ratio" json:"max_non_ok_ratio"`
}

type lifecycleGateConfig struct {
	Reason    string                             `yaml:"reason"`
	MaxRatio  *float64                           `yaml:"max_ratio"`
	Producers map[string]lifecycleProducerConfig `yaml:"producers"`
}

type lifecycleProducerConfig struct {
	MetricLabels map[string]string `yaml:"metric_labels"`
}

type ghzSummary struct {
	Count                  uint64         `json:"count"`
	RPS                    float64        `json:"rps"`
	StatusCodeDistribution map[string]int `json:"statusCodeDistribution"`
	LatencyDistribution    []struct {
		Percentage int   `json:"percentage"`
		Latency    int64 `json:"latency"`
	} `json:"latencyDistribution"`
}

type perfGateReport struct {
	Thresholds      aggregateThresholds  `json:"thresholds"`
	Observed        aggregateObservation `json:"observed"`
	LifecycleReason string               `json:"lifecycle_reason,omitempty"`
	ProducerResults []producerResult     `json:"producer_results"`
	Failures        []string             `json:"failures,omitempty"`
	Verdict         string               `json:"verdict"`
}

type aggregateThresholds struct {
	MinSteadyRPSTotal         *float64 `json:"min_steady_rps_total"`
	MaxP99MS                  *float64 `json:"max_p99_ms"`
	MaxNonOKRatio             *float64 `json:"max_non_ok_ratio"`
	MaxExpectedLifecycleRatio *float64 `json:"max_expected_lifecycle_ratio"`
}

type aggregateObservation struct {
	Producers                      int     `json:"producers"`
	SteadyRPSTotal                 float64 `json:"steady_rps_total"`
	P99WorstMS                     float64 `json:"p99_worst_ms"`
	CountTotal                     int64   `json:"count_total"`
	RawNonOKTotal                  int64   `json:"raw_non_ok_total"`
	ExpectedLifecycleTotal         int64   `json:"expected_lifecycle_total"`
	ExpectedLifecycleEligibleTotal int64   `json:"expected_lifecycle_eligible_total"`
	UnexpectedNonOKTotal           int64   `json:"unexpected_non_ok_total"`
	// NonOKTotal and NonOKRatio retain the established artifact fields. When
	// lifecycle classification is configured they intentionally mean
	// unexpected non-OK, which is the value consumed by max_non_ok_ratio.
	NonOKTotal             int64   `json:"non_ok_total"`
	NonOKRatio             float64 `json:"non_ok_ratio"`
	ExpectedLifecycleRatio float64 `json:"expected_lifecycle_ratio"`
}

type producerResult struct {
	Name            string              `json:"name"`
	Thresholds      producerThresholds  `json:"thresholds"`
	Observed        producerObservation `json:"observed"`
	LifecycleReason string              `json:"lifecycle_reason,omitempty"`
	Failures        []string            `json:"failures,omitempty"`
	Verdict         string              `json:"verdict"`
}

type producerThresholds struct {
	MinSteadyRPS              *float64 `json:"min_steady_rps"`
	MaxP99MS                  *float64 `json:"max_p99_ms"`
	MaxNonOKRatio             *float64 `json:"max_non_ok_ratio"`
	MaxExpectedLifecycleRatio *float64 `json:"max_expected_lifecycle_ratio"`
}

type producerObservation struct {
	SteadyRPS              float64        `json:"steady_rps"`
	P99MS                  float64        `json:"p99_ms"`
	Count                  int64          `json:"count"`
	RawNonOK               int64          `json:"raw_non_ok"`
	ExpectedLifecycle      int64          `json:"expected_lifecycle"`
	UnexpectedNonOK        int64          `json:"unexpected_non_ok"`
	NonOK                  int64          `json:"non_ok"`
	NonOKRatio             float64        `json:"non_ok_ratio"`
	ExpectedLifecycleRatio float64        `json:"expected_lifecycle_ratio"`
	StatusCodeDistribution map[string]int `json:"status_code_distribution"`
}

func evaluate(scenarioPath, runDir, prePath, postPath string) (perfGateReport, error) {
	doc, err := loadScenario(scenarioPath)
	if err != nil {
		return perfGateReport{}, err
	}
	if err := validateScenario(doc); err != nil {
		return perfGateReport{}, err
	}

	var before, after searchCounterSnapshot
	if doc.LifecycleGate != nil {
		if prePath == "" || postPath == "" {
			return perfGateReport{}, errors.New("lifecycle gate requires pre and post snapshots")
		}
		if err := readJSON(prePath, &before); err != nil {
			return perfGateReport{}, fmt.Errorf("read lifecycle pre snapshot: %w", err)
		}
		if err := readJSON(postPath, &after); err != nil {
			return perfGateReport{}, fmt.Errorf("read lifecycle post snapshot: %w", err)
		}
		if before.Metric != searchCallsMetric || after.Metric != searchCallsMetric {
			return perfGateReport{}, fmt.Errorf("lifecycle snapshots must contain %s", searchCallsMetric)
		}
	}

	report := perfGateReport{
		Thresholds: aggregateThresholds{
			MinSteadyRPSTotal: doc.PerfGate.MinSteadyRPSTotal,
			MaxP99MS:          doc.PerfGate.MaxP99MS,
			MaxNonOKRatio:     doc.PerfGate.MaxNonOKRatio,
		},
		Verdict: "pass",
	}
	if doc.LifecycleGate != nil {
		report.LifecycleReason = doc.LifecycleGate.Reason
		report.Thresholds.MaxExpectedLifecycleRatio = doc.LifecycleGate.MaxRatio
	}

	producerNames := make([]string, len(doc.Target.Calls))
	if len(doc.Target.Calls) == 0 {
		producerNames = []string{"primary"}
	} else {
		for i, call := range doc.Target.Calls {
			producerNames[i] = call.Name
			if producerNames[i] == "" {
				producerNames[i] = "producer-" + strconv.Itoa(i)
			}
		}
	}

	for i, name := range producerNames {
		summary, err := loadProducerSummary(runDir, i, len(doc.Target.Calls) > 0)
		if err != nil {
			return perfGateReport{}, fmt.Errorf("producer %s: %w", name, err)
		}
		result := classifyProducer(doc, before, after, i, name, summary)
		report.ProducerResults = append(report.ProducerResults, result)
		report.Observed.Producers++
		report.Observed.SteadyRPSTotal += result.Observed.SteadyRPS
		report.Observed.P99WorstMS = max(report.Observed.P99WorstMS, result.Observed.P99MS)
		report.Observed.CountTotal += result.Observed.Count
		report.Observed.RawNonOKTotal += result.Observed.RawNonOK
		report.Observed.ExpectedLifecycleTotal += result.Observed.ExpectedLifecycle
		report.Observed.UnexpectedNonOKTotal += result.Observed.UnexpectedNonOK
		if result.LifecycleReason != "" {
			report.Observed.ExpectedLifecycleEligibleTotal += result.Observed.Count
		}
		if result.Verdict == "fail" {
			report.Verdict = "fail"
		}
	}
	report.Observed.NonOKTotal = report.Observed.UnexpectedNonOKTotal
	report.Observed.NonOKRatio = ratio(report.Observed.UnexpectedNonOKTotal, report.Observed.CountTotal)
	report.Observed.ExpectedLifecycleRatio = ratio(report.Observed.ExpectedLifecycleTotal, report.Observed.ExpectedLifecycleEligibleTotal)

	if below(report.Observed.SteadyRPSTotal, report.Thresholds.MinSteadyRPSTotal) ||
		above(report.Observed.P99WorstMS, report.Thresholds.MaxP99MS) ||
		above(report.Observed.NonOKRatio, report.Thresholds.MaxNonOKRatio) ||
		above(report.Observed.ExpectedLifecycleRatio, report.Thresholds.MaxExpectedLifecycleRatio) {
		report.Verdict = "fail"
	}
	return report, nil
}

func classifyProducer(doc scenarioDocument, before, after searchCounterSnapshot, index int, name string, summary ghzSummary) producerResult {
	gate := doc.PerfGate.Producers[name]
	count := int64(summary.Count)
	rawNonOK := int64(0)
	for status, value := range summary.StatusCodeDistribution {
		if status != "OK" {
			rawNonOK += int64(value)
		}
	}
	result := producerResult{
		Name: name,
		Thresholds: producerThresholds{
			MinSteadyRPS:  gate.MinSteadyRPS,
			MaxP99MS:      gate.MaxP99MS,
			MaxNonOKRatio: gate.MaxNonOKRatio,
		},
		Observed: producerObservation{
			SteadyRPS:              summary.RPS,
			P99MS:                  float64(percentile(summary, 99)) / 1e6,
			Count:                  count,
			RawNonOK:               rawNonOK,
			UnexpectedNonOK:        rawNonOK,
			StatusCodeDistribution: summary.StatusCodeDistribution,
		},
		Verdict: "pass",
	}
	if doc.LifecycleGate != nil {
		if config, ok := doc.LifecycleGate.Producers[name]; ok {
			result.LifecycleReason = doc.LifecycleGate.Reason
			result.Thresholds.MaxExpectedLifecycleRatio = doc.LifecycleGate.MaxRatio
			expected, err := lifecycleDelta(before, after, index%len(doc.Target.Endpoints), config.MetricLabels)
			if err != nil {
				result.Failures = append(result.Failures, err.Error())
			} else if expected > int64(summary.StatusCodeDistribution[indexIncompleteStatus]) {
				result.Failures = append(result.Failures, fmt.Sprintf("typed lifecycle count %d exceeds %s status count %d", expected, indexIncompleteStatus, summary.StatusCodeDistribution[indexIncompleteStatus]))
			} else {
				result.Observed.ExpectedLifecycle = expected
				result.Observed.UnexpectedNonOK = rawNonOK - expected
			}
		}
	}
	result.Observed.NonOK = result.Observed.UnexpectedNonOK
	result.Observed.NonOKRatio = ratio(result.Observed.UnexpectedNonOK, count)
	result.Observed.ExpectedLifecycleRatio = ratio(result.Observed.ExpectedLifecycle, count)
	if len(result.Failures) > 0 ||
		below(result.Observed.SteadyRPS, result.Thresholds.MinSteadyRPS) ||
		above(result.Observed.P99MS, result.Thresholds.MaxP99MS) ||
		above(result.Observed.NonOKRatio, result.Thresholds.MaxNonOKRatio) ||
		above(result.Observed.ExpectedLifecycleRatio, result.Thresholds.MaxExpectedLifecycleRatio) {
		result.Verdict = "fail"
	}
	return result
}

func lifecycleDelta(before, after searchCounterSnapshot, replicaIndex int, configured map[string]string) (int64, error) {
	labels := make(map[string]string, len(configured)+2)
	for key, value := range configured {
		labels[key] = value
	}
	labels["outcome"] = indexIncompleteOutcome
	labels["reason"] = indexIncompleteMetricLabel
	pre, err := counterValue(before, replicaIndex, labels)
	if err != nil {
		return 0, fmt.Errorf("pre snapshot: %w", err)
	}
	post, err := counterValue(after, replicaIndex, labels)
	if err != nil {
		return 0, fmt.Errorf("post snapshot: %w", err)
	}
	delta := post - pre
	if delta < 0 || math.Trunc(delta) != delta || delta > math.MaxInt64 {
		return 0, fmt.Errorf("invalid lifecycle counter delta %v", delta)
	}
	return int64(delta), nil
}

func counterValue(snapshot searchCounterSnapshot, replicaIndex int, labels map[string]string) (float64, error) {
	for _, replica := range snapshot.Replicas {
		if replica.Index != replicaIndex {
			continue
		}
		for _, series := range replica.Series {
			if labelKey(series.Labels) == labelKey(labels) {
				return series.Value, nil
			}
		}
		// CounterVec creates a series lazily for option combinations that have
		// never occurred. An absent exact selector is therefore the canonical
		// zero value. Unknown label names/values are rejected from the scenario
		// before evaluation, so treating absence as zero cannot hide a typo.
		return 0, nil
	}
	return 0, fmt.Errorf("missing replica %d", replicaIndex)
}

func validateScenario(doc scenarioDocument) error {
	for name, value := range map[string]*float64{
		"perf_gate.min_steady_rps_total": doc.PerfGate.MinSteadyRPSTotal,
		"perf_gate.max_p99_ms":           doc.PerfGate.MaxP99MS,
		"perf_gate.max_non_ok_ratio":     doc.PerfGate.MaxNonOKRatio,
	} {
		if err := validateThreshold(name, value, strings.Contains(name, "ratio")); err != nil {
			return err
		}
	}
	known := map[string]bool{}
	producerIndexes := map[string]int{}
	producerCalls := map[string]string{}
	if len(doc.Target.Calls) == 0 {
		known["primary"] = true
		producerIndexes["primary"] = 0
		producerCalls["primary"] = doc.Target.Call
	} else {
		for i, call := range doc.Target.Calls {
			name := call.Name
			if name == "" {
				name = "producer-" + strconv.Itoa(i)
			}
			if known[name] {
				return fmt.Errorf("duplicate producer name %q", name)
			}
			known[name] = true
			producerIndexes[name] = i
			producerCalls[name] = call.Call
		}
	}
	for name, gate := range doc.PerfGate.Producers {
		if !known[name] {
			return fmt.Errorf("perf gate references unknown producer %q", name)
		}
		for field, value := range map[string]*float64{
			"min_steady_rps":   gate.MinSteadyRPS,
			"max_p99_ms":       gate.MaxP99MS,
			"max_non_ok_ratio": gate.MaxNonOKRatio,
		} {
			if err := validateThreshold("perf_gate.producers."+name+"."+field, value, strings.Contains(field, "ratio")); err != nil {
				return err
			}
		}
	}
	if doc.LifecycleGate == nil {
		return nil
	}
	if doc.LifecycleGate.Reason != searchIndexIncomplete {
		return fmt.Errorf("unsupported lifecycle reason %q", doc.LifecycleGate.Reason)
	}
	if err := validateThreshold("lifecycle_gate.max_ratio", doc.LifecycleGate.MaxRatio, true); err != nil {
		return err
	}
	if doc.LifecycleGate.MaxRatio == nil {
		return errors.New("lifecycle_gate.max_ratio is required")
	}
	if len(doc.Target.Endpoints) == 0 {
		return errors.New("lifecycle gate requires target endpoints")
	}
	if len(doc.LifecycleGate.Producers) == 0 {
		return errors.New("lifecycle gate requires producer selectors")
	}
	seenSelectors := map[string]string{}
	for name, config := range doc.LifecycleGate.Producers {
		if !known[name] {
			return fmt.Errorf("lifecycle gate references unknown producer %q", name)
		}
		if !strings.HasSuffix(producerCalls[name], "/SearchVertices") {
			return fmt.Errorf("lifecycle producer %q is not a SearchVertices call", name)
		}
		if len(config.MetricLabels) != len(requiredSearchLabels) {
			return fmt.Errorf("lifecycle producer %q must declare exactly %d bounded request labels", name, len(requiredSearchLabels))
		}
		for _, label := range requiredSearchLabels {
			if _, ok := config.MetricLabels[label]; !ok {
				return fmt.Errorf("lifecycle producer %q missing metric label %q", name, label)
			}
		}
		allowed := map[string]map[string]bool{
			"mode":           {"server": true, "any": true, "all": true, "min_should": true},
			"phrase":         {"no": true, "yes": true},
			"fuzziness":      {"0": true, "1": true, "2": true},
			"prefix_terms":   {"no": true, "yes": true},
			"prefix_present": {"no": true, "yes": true},
		}
		for label, values := range allowed {
			if !values[config.MetricLabels[label]] {
				return fmt.Errorf("lifecycle producer %q has invalid %s label %q", name, label, config.MetricLabels[label])
			}
		}
		signature := fmt.Sprintf("replica=%d;%s", producerIndexes[name]%len(doc.Target.Endpoints), labelKey(config.MetricLabels))
		if previous := seenSelectors[signature]; previous != "" {
			return fmt.Errorf("lifecycle producers %q and %q share one replica/selector", previous, name)
		}
		seenSelectors[signature] = name
	}
	return nil
}

func validateThreshold(name string, value *float64, ratioValue bool) error {
	if value == nil {
		return nil
	}
	if math.IsNaN(*value) || math.IsInf(*value, 0) || *value < 0 {
		return fmt.Errorf("%s must be finite and non-negative", name)
	}
	if ratioValue && *value > 1 {
		return fmt.Errorf("%s must be at most 1", name)
	}
	return nil
}

func loadScenario(path string) (scenarioDocument, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return scenarioDocument{}, err
	}
	var doc scenarioDocument
	if err := yaml.Unmarshal(b, &doc); err != nil {
		return scenarioDocument{}, err
	}
	return doc, nil
}

func loadProducerSummary(dir string, index int, fanout bool) (ghzSummary, error) {
	pattern := "ghz_steady_*.json"
	if fanout {
		pattern = fmt.Sprintf("ghz_steady_%d_*.json", index)
	}
	matches, err := filepath.Glob(filepath.Join(dir, pattern))
	if err != nil {
		return ghzSummary{}, err
	}
	if len(matches) != 1 {
		return ghzSummary{}, fmt.Errorf("matched %d files with %s", len(matches), pattern)
	}
	var summary ghzSummary
	if err := readJSON(matches[0], &summary); err != nil {
		return ghzSummary{}, err
	}
	if summary.Count > math.MaxInt64 {
		return ghzSummary{}, errors.New("ghz count exceeds int64")
	}
	statusTotal := int64(0)
	for status, count := range summary.StatusCodeDistribution {
		if status == "" || count < 0 {
			return ghzSummary{}, fmt.Errorf("invalid status distribution entry %q=%d", status, count)
		}
		if uint64(count) > uint64(math.MaxInt64-statusTotal) {
			return ghzSummary{}, errors.New("status distribution total exceeds int64")
		}
		statusTotal += int64(count)
	}
	if statusTotal != int64(summary.Count) {
		return ghzSummary{}, fmt.Errorf("status distribution total %d does not equal count %d", statusTotal, summary.Count)
	}
	if math.IsNaN(summary.RPS) || math.IsInf(summary.RPS, 0) || summary.RPS < 0 {
		return ghzSummary{}, fmt.Errorf("invalid rps %v", summary.RPS)
	}
	return summary, nil
}

func percentile(summary ghzSummary, percentage int) int64 {
	for _, point := range summary.LatencyDistribution {
		if point.Percentage == percentage {
			return point.Latency
		}
	}
	return 0
}

func labelKey(labels map[string]string) string {
	keys := make([]string, 0, len(labels))
	for key := range labels {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, key := range keys {
		b.WriteString(strconv.Quote(key))
		b.WriteByte('=')
		b.WriteString(strconv.Quote(labels[key]))
		b.WriteByte(';')
	}
	return b.String()
}

func splitNonEmpty(value string) []string {
	var result []string
	for _, item := range strings.Split(value, ",") {
		if item = strings.TrimSpace(item); item != "" {
			result = append(result, item)
		}
	}
	return result
}

func below(observed float64, threshold *float64) bool {
	return threshold != nil && observed < *threshold
}

func above(observed float64, threshold *float64) bool {
	return threshold != nil && observed > *threshold
}

func ratio(numerator, denominator int64) float64 {
	if denominator <= 0 {
		return 0
	}
	return float64(numerator) / float64(denominator)
}

func readJSON(path string, dst any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, dst)
}

func writeJSON(path string, value any) error {
	b, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')
	return os.WriteFile(path, b, 0o644)
}
