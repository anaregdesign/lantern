// metricgate evaluates scenario-declared pre/post Prometheus metric contracts.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"math"
	"os"
	"sort"

	"gopkg.in/yaml.v3"
)

type threshold struct {
	MaxIncrease *float64 `json:"max_increase,omitempty" yaml:"max_increase"`
	MaxRatio    *float64 `json:"max_ratio,omitempty" yaml:"max_ratio"`
	MinIncrease *float64 `json:"min_increase,omitempty" yaml:"min_increase"`
	MinPost     *float64 `json:"min_post,omitempty" yaml:"min_post"`
	MaxPost     *float64 `json:"max_post,omitempty" yaml:"max_post"`
}

type scenarioDocument struct {
	MetricGate struct {
		Metrics map[string]threshold `yaml:"metrics"`
	} `yaml:"metric_gate"`
}

type snapshotReplica struct {
	Endpoint string              `json:"endpoint"`
	Metrics  map[string]*float64 `json:"metrics"`
}

type result struct {
	Endpoint   string    `json:"endpoint"`
	Metric     string    `json:"metric"`
	Thresholds threshold `json:"thresholds"`
	Pre        *float64  `json:"pre,omitempty"`
	Post       *float64  `json:"post,omitempty"`
	Delta      *float64  `json:"delta,omitempty"`
	Ratio      *float64  `json:"ratio,omitempty"`
	Failures   []string  `json:"failures,omitempty"`
	Verdict    string    `json:"verdict"`
}

type report struct {
	Verdict string   `json:"verdict"`
	Results []result `json:"results"`
}

func main() {
	scenarioPath := flag.String("scenario", "", "scenario YAML containing metric_gate.metrics")
	prePath := flag.String("pre", "", "pre-run runtime snapshot JSON")
	postPath := flag.String("post", "", "post-run runtime snapshot JSON")
	outPath := flag.String("out", "", "write the metric-gate report to this path")
	flag.Parse()

	if *scenarioPath == "" || *prePath == "" || *postPath == "" || *outPath == "" {
		fatalf("-scenario, -pre, -post, and -out are required")
	}
	thresholds, err := loadThresholds(*scenarioPath)
	if err != nil {
		fatalf("load scenario: %v", err)
	}
	pre, err := loadSnapshot(*prePath)
	if err != nil {
		fatalf("load pre snapshot: %v", err)
	}
	post, err := loadSnapshot(*postPath)
	if err != nil {
		fatalf("load post snapshot: %v", err)
	}

	rep := evaluate(thresholds, pre, post)
	encoded, err := json.MarshalIndent(rep, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	if err := os.WriteFile(*outPath, append(encoded, '\n'), 0o644); err != nil {
		fatalf("write report: %v", err)
	}
	if rep.Verdict != "pass" {
		os.Exit(1)
	}
}

func loadThresholds(path string) (map[string]threshold, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var doc scenarioDocument
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if len(doc.MetricGate.Metrics) == 0 {
		return nil, errors.New("metric_gate.metrics is empty")
	}
	for name, t := range doc.MetricGate.Metrics {
		if name == "" {
			return nil, errors.New("metric name must not be empty")
		}
		if err := validateThreshold(t); err != nil {
			return nil, fmt.Errorf("metric %s: %w", name, err)
		}
	}
	return doc.MetricGate.Metrics, nil
}

func validateThreshold(t threshold) error {
	if t.MaxIncrease == nil && t.MaxRatio == nil && t.MinIncrease == nil && t.MinPost == nil && t.MaxPost == nil {
		return errors.New("at least one threshold is required")
	}
	for name, value := range map[string]*float64{
		"max_increase": t.MaxIncrease,
		"max_ratio":    t.MaxRatio,
		"min_increase": t.MinIncrease,
		"min_post":     t.MinPost,
		"max_post":     t.MaxPost,
	} {
		if value != nil && (math.IsNaN(*value) || math.IsInf(*value, 0)) {
			return fmt.Errorf("%s must be finite", name)
		}
	}
	if t.MaxIncrease != nil && *t.MaxIncrease < 0 {
		return errors.New("max_increase must be non-negative")
	}
	if t.MaxRatio != nil && *t.MaxRatio < 1 {
		return errors.New("max_ratio must be at least 1")
	}
	if t.MinIncrease != nil && *t.MinIncrease < 0 {
		return errors.New("min_increase must be non-negative")
	}
	if t.MinPost != nil && t.MaxPost != nil && *t.MinPost > *t.MaxPost {
		return errors.New("min_post must not exceed max_post")
	}
	return nil
}

func loadSnapshot(path string) ([]snapshotReplica, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snapshot []snapshotReplica
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, err
	}
	if len(snapshot) == 0 {
		return nil, errors.New("snapshot has no replicas")
	}
	return snapshot, nil
}

func evaluate(thresholds map[string]threshold, pre, post []snapshotReplica) report {
	rep := report{Verdict: "pass"}
	postByEndpoint := make(map[string]snapshotReplica, len(post))
	for _, replica := range post {
		postByEndpoint[replica.Endpoint] = replica
	}
	metricNames := make([]string, 0, len(thresholds))
	for name := range thresholds {
		metricNames = append(metricNames, name)
	}
	sort.Strings(metricNames)
	preSorted := append([]snapshotReplica(nil), pre...)
	sort.Slice(preSorted, func(i, j int) bool { return preSorted[i].Endpoint < preSorted[j].Endpoint })

	for _, before := range preSorted {
		after, ok := postByEndpoint[before.Endpoint]
		for _, name := range metricNames {
			row := result{Endpoint: before.Endpoint, Metric: name, Thresholds: thresholds[name], Verdict: "pass"}
			if !ok {
				row.Failures = append(row.Failures, "replica missing from post snapshot")
			} else {
				row.Pre = before.Metrics[name]
				row.Post = after.Metrics[name]
				row.evaluate()
			}
			if len(row.Failures) > 0 {
				row.Verdict = "fail"
				rep.Verdict = "fail"
			}
			rep.Results = append(rep.Results, row)
		}
	}
	if len(postByEndpoint) != len(preSorted) {
		preEndpoints := make(map[string]bool, len(preSorted))
		for _, replica := range preSorted {
			preEndpoints[replica.Endpoint] = true
		}
		for endpoint := range postByEndpoint {
			if !preEndpoints[endpoint] {
				rep.Verdict = "fail"
				rep.Results = append(rep.Results, result{
					Endpoint: endpoint,
					Metric:   "*",
					Failures: []string{"replica missing from pre snapshot"},
					Verdict:  "fail",
				})
			}
		}
	}
	return rep
}

func (r *result) evaluate() {
	if r.Pre == nil {
		r.Failures = append(r.Failures, "metric missing from pre snapshot")
	}
	if r.Post == nil {
		r.Failures = append(r.Failures, "metric missing from post snapshot")
	}
	if r.Pre == nil || r.Post == nil {
		return
	}
	if !finite(*r.Pre) || !finite(*r.Post) {
		r.Failures = append(r.Failures, "metric sample must be finite")
		return
	}
	delta := *r.Post - *r.Pre
	r.Delta = &delta
	if *r.Pre == 0 {
		if *r.Post == 0 {
			ratio := 1.0
			r.Ratio = &ratio
		}
	} else {
		ratio := *r.Post / *r.Pre
		r.Ratio = &ratio
	}
	if t := r.Thresholds.MaxIncrease; t != nil && delta > *t {
		r.Failures = append(r.Failures, fmt.Sprintf("increase %.6g exceeds %.6g", delta, *t))
	}
	if t := r.Thresholds.MinIncrease; t != nil && delta < *t {
		r.Failures = append(r.Failures, fmt.Sprintf("increase %.6g is below %.6g", delta, *t))
	}
	if t := r.Thresholds.MaxRatio; t != nil {
		if r.Ratio == nil {
			r.Failures = append(r.Failures, "ratio is unbounded from a zero baseline")
		} else if *r.Ratio > *t {
			r.Failures = append(r.Failures, fmt.Sprintf("ratio %.6g exceeds %.6g", *r.Ratio, *t))
		}
	}
	if t := r.Thresholds.MinPost; t != nil && *r.Post < *t {
		r.Failures = append(r.Failures, fmt.Sprintf("post %.6g is below %.6g", *r.Post, *t))
	}
	if t := r.Thresholds.MaxPost; t != nil && *r.Post > *t {
		r.Failures = append(r.Failures, fmt.Sprintf("post %.6g exceeds %.6g", *r.Post, *t))
	}
}

func finite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "metricgate: "+format+"\n", args...)
	os.Exit(2)
}
