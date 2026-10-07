package main

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

func TestOfferedLoadPreservesFailureAndIndependentSlots(t *testing.T) {
	for _, failure := range []bool{false, true} {
		result := runProducer(t.Context(), time.Now().Add(time.Millisecond), 3, 100, 2, time.Second, func(context.Context, int) error {
			if failure {
				return errors.New("raw regression failure")
			}
			return nil
		})
		if result.Offered != 3 || len(result.Samples) != 3 {
			t.Fatal("offered count was reduced")
		}
		for i, sample := range result.Samples {
			if sample.Slot != i || sample.Status == "" || failure && sample.Status == "ok" {
				t.Fatal("failure/slot lost", result)
			}
		}
	}
}

func TestLatencyQuantileRetainsSmallSampleTail(t *testing.T) {
	samples := []time.Duration{1, 1000}
	if latencyQuantile(samples, 99) != 1000 || latencyQuantile(samples, 50) != 1 {
		t.Fatal("small-sample tail was understated")
	}
}

type loadWriterClient struct {
	queryResultClient
	fail  bool
	calls atomic.Int64
}

func (c *loadWriterClient) PutVertices(context.Context, *connect.Request[pb.PutVerticesRequest]) (*connect.Response[pb.PutVerticesResponse], error) {
	c.calls.Add(1)
	if c.fail {
		return nil, connect.NewError(connect.CodeUnavailable, errors.New("writer unavailable"))
	}
	return connect.NewResponse(&pb.PutVerticesResponse{Outcomes: []pb.PutOutcome{pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE, pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE}}), nil
}

func TestWriterFailureCannotBeMaskedBySuccessfulReader(t *testing.T) {
	for _, failure := range []bool{false, true} {
		client := &loadWriterClient{fail: failure, queryResultClient: queryResultClient{hits: []*pb.SearchHit{
			{Key: "bench:ranking:a", Vertex: &pb.Vertex{Key: "bench:ranking:a"}},
			{Key: "bench:ranking:b", Vertex: &pb.Vertex{Key: "bench:ranking:b"}},
		}}}
		cfg := loadConfig{Family: "search", Phase: "update-mixed", Count: 2, RPS: 10, WriterRPS: 10, Concurrency: 2, Timeout: time.Second}
		result, err := runLoad(t.Context(), &queryEndpoint{mode: "oidc", client: client}, cfg)
		if (err != nil) != failure || client.calls.Load() != 2 || result.Reader.Offered != 2 || result.Writer.Offered != 2 {
			t.Fatal("independent writer result lost", err, result)
		}
		for _, sample := range result.Writer.Samples {
			if failure && sample.Status != "unavailable" {
				t.Fatal("raw writer failure lost", sample)
			}
		}
	}
}

func TestOfferedLoadFailsSaturationWithoutSilentClosedLoop(t *testing.T) {
	result := runProducer(t.Context(), time.Now().Add(time.Millisecond), 3, 1000, 1, time.Second, func(context.Context, int) error {
		time.Sleep(20 * time.Millisecond)
		return nil
	})
	if result.Samples[1].Status != "producer_saturated" || result.Samples[2].Status != "producer_saturated" || result.Offered != 3 {
		t.Fatal("saturation reduced offered load", result)
	}
}

func TestQueryLoadBoundaries(t *testing.T) {
	base := loadConfig{Family: "search", Phase: "first", Count: 1, RPS: 1, Concurrency: 1, Timeout: time.Second}
	if err := base.validate(); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*loadConfig){
		func(c *loadConfig) { c.Count = 0 }, func(c *loadConfig) { c.RPS = 0 },
		func(c *loadConfig) { c.Phase = "update-mixed" }, func(c *loadConfig) { c.WriterRPS = 1 },
		func(c *loadConfig) { c.Count = 2 }, func(c *loadConfig) { c.Family = "export" },
		func(c *loadConfig) { c.Phase = "warm"; c.Count = 121 },
	} {
		invalid := base
		mutate(&invalid)
		if invalid.validate() == nil {
			t.Fatal("invalid load accepted", invalid)
		}
	}
	base.Phase, base.WriterRPS = "update-mixed", 1
	if base.validate() != nil {
		t.Fatal("paired update load rejected")
	}
}
