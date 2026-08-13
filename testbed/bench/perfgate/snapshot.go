package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"sort"

	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/prometheus/common/model"
)

type searchCounterSnapshot struct {
	Metric   string            `json:"metric"`
	Replicas []snapshotReplica `json:"replicas"`
}

type snapshotReplica struct {
	Index    int              `json:"index"`
	Endpoint string           `json:"endpoint"`
	Series   []snapshotSeries `json:"series"`
}

type snapshotSeries struct {
	Labels map[string]string `json:"labels"`
	Value  float64           `json:"value"`
}

func captureSearchCounters(ctx context.Context, client *http.Client, endpoints []string) (searchCounterSnapshot, error) {
	if len(endpoints) == 0 {
		return searchCounterSnapshot{}, errors.New("no metrics endpoints")
	}
	snapshot := searchCounterSnapshot{Metric: searchCallsMetric, Replicas: make([]snapshotReplica, 0, len(endpoints))}
	for i, endpoint := range endpoints {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
		if err != nil {
			return searchCounterSnapshot{}, fmt.Errorf("replica %d: %w", i, err)
		}
		resp, err := client.Do(req)
		if err != nil {
			return searchCounterSnapshot{}, fmt.Errorf("scrape replica %d: %w", i, err)
		}
		if resp.StatusCode != http.StatusOK {
			if err := resp.Body.Close(); err != nil {
				return searchCounterSnapshot{}, fmt.Errorf("close replica %d metrics: %w", i, err)
			}
			return searchCounterSnapshot{}, fmt.Errorf("scrape replica %d: HTTP %s", i, resp.Status)
		}
		parser := expfmt.NewTextParser(model.UTF8Validation)
		families, parseErr := parser.TextToMetricFamilies(io.LimitReader(resp.Body, 16<<20))
		closeErr := resp.Body.Close()
		if parseErr != nil {
			return searchCounterSnapshot{}, fmt.Errorf("parse replica %d metrics: %w", i, parseErr)
		}
		if closeErr != nil {
			return searchCounterSnapshot{}, fmt.Errorf("close replica %d metrics: %w", i, closeErr)
		}
		family := families[searchCallsMetric]
		if family == nil || family.GetType() != dto.MetricType_COUNTER {
			return searchCounterSnapshot{}, fmt.Errorf("replica %d missing counter %s", i, searchCallsMetric)
		}
		replica := snapshotReplica{Index: i, Endpoint: endpoint}
		for _, metric := range family.GetMetric() {
			labels := make(map[string]string, len(metric.GetLabel()))
			for _, pair := range metric.GetLabel() {
				labels[pair.GetName()] = pair.GetValue()
			}
			value := metric.GetCounter().GetValue()
			if math.IsNaN(value) || math.IsInf(value, 0) || value < 0 {
				return searchCounterSnapshot{}, fmt.Errorf("replica %d has invalid %s counter %v", i, searchCallsMetric, value)
			}
			replica.Series = append(replica.Series, snapshotSeries{Labels: labels, Value: value})
		}
		sort.Slice(replica.Series, func(a, b int) bool {
			return labelKey(replica.Series[a].Labels) < labelKey(replica.Series[b].Labels)
		})
		snapshot.Replicas = append(snapshot.Replicas, replica)
	}
	return snapshot, nil
}
