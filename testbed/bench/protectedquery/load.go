package main

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"connectrpc.com/connect"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
)

type loadConfig struct {
	Family      string        `json:"family"`
	Phase       string        `json:"phase"`
	Count       int           `json:"query_count"`
	RPS         int           `json:"query_rps"`
	WriterRPS   int           `json:"writer_rps"`
	Concurrency int           `json:"concurrency_per_producer"`
	Timeout     time.Duration `json:"rpc_timeout_ns"`
}

func (c loadConfig) duration() time.Duration {
	return time.Duration(c.Count) * time.Second / time.Duration(c.RPS)
}

func (c loadConfig) validate() error {
	if c.Family != "search" && c.Family != "bfs" && c.Family != "ppr" && c.Family != "community" || c.Phase != "first" && c.Phase != "warm" && c.Phase != "update-mixed" || c.Count < 1 || c.Count > 100000 || c.RPS < 1 || c.RPS > 10000 || c.WriterRPS < 0 || c.WriterRPS > 10000 || c.Concurrency < 1 || c.Concurrency > 256 || c.Timeout < time.Millisecond || c.Timeout > 30*time.Second {
		return errors.New("invalid bounded query load")
	}
	if c.duration() > 2*time.Minute || c.Phase == "first" && (c.Count != 1 || c.Concurrency != 1) || c.Phase == "update-mixed" && c.WriterRPS == 0 || c.Phase != "update-mixed" && c.WriterRPS != 0 {
		return errors.New("phase/load mismatch")
	}
	return nil
}

type querySample struct {
	Slot             int           `json:"slot"`
	ScheduleDelay    time.Duration `json:"schedule_delay_ns"`
	RPCAndValidation time.Duration `json:"rpc_and_validation_ns"`
	Status           string        `json:"status"`
}

type producerReport struct {
	Offered int           `json:"offered"`
	Samples []querySample `json:"samples"`
	P50     time.Duration `json:"p50_ns"`
	P99     time.Duration `json:"p99_ns"`
}

type loadReport struct {
	Config   loadConfig     `json:"offered_load"`
	Started  time.Time      `json:"scheduled_start"`
	Reader   producerReport `json:"reader"`
	Writer   producerReport `json:"writer"`
	Interval string         `json:"interval"`
}

func runProducer(ctx context.Context, start time.Time, count, rps, concurrency int, timeout time.Duration, call func(context.Context, int) error) producerReport {
	report := producerReport{Offered: count, Samples: make([]querySample, count)}
	slots := make(chan struct{}, concurrency)
	var pending sync.WaitGroup
	for i := 0; i < count; i++ {
		scheduled := start.Add(time.Duration(i) * time.Second / time.Duration(rps))
		timer := time.NewTimer(time.Until(scheduled))
		select {
		case <-ctx.Done():
			timer.Stop()
			for j := i; j < count; j++ {
				report.Samples[j] = querySample{Slot: j, Status: "cancelled_before_dispatch"}
			}
			pending.Wait()
			return report
		case <-timer.C:
		}
		report.Samples[i] = querySample{Slot: i}
		select {
		case slots <- struct{}{}:
			pending.Add(1)
			go func(index int, due time.Time) {
				defer pending.Done()
				defer func() { <-slots }()
				began := time.Now()
				sample := querySample{Slot: index, ScheduleDelay: began.Sub(due), Status: "ok"}
				if sample.ScheduleDelay > time.Second/time.Duration(rps) {
					sample.Status = "scheduler_late"
				}
				rpcCtx, cancel := context.WithTimeout(ctx, timeout)
				err := call(rpcCtx, index)
				cancel()
				sample.RPCAndValidation = time.Since(began)
				if err != nil {
					sample.Status = connect.CodeOf(err).String()
				}
				report.Samples[index] = sample
			}(i, scheduled)
		default:
			report.Samples[i].Status = "producer_saturated"
		}
	}
	pending.Wait()
	latencies := make([]time.Duration, 0, count)
	for _, sample := range report.Samples {
		if sample.Status == "ok" {
			latencies = append(latencies, sample.RPCAndValidation)
		}
	}
	sort.Slice(latencies, func(i, j int) bool { return latencies[i] < latencies[j] })
	if len(latencies) > 0 {
		report.P50 = latencyQuantile(latencies, 50)
		report.P99 = latencyQuantile(latencies, 99)
	}
	return report
}

// Nearest-rank percentiles preserve the tail even for a short preparation run.
// The caller supplies sorted successful samples; a failed producer cannot qualify.
func latencyQuantile(sorted []time.Duration, percentile int) time.Duration {
	return sorted[(len(sorted)*percentile+99)/100-1]
}

func runLoad(ctx context.Context, endpoint *queryEndpoint, cfg loadConfig) (*loadReport, error) {
	if err := cfg.validate(); err != nil {
		return nil, err
	}
	report := &loadReport{Config: cfg, Started: time.Now().Add(20 * time.Millisecond), Interval: "client_dispatch_through_rpc_and_result_validation; includes transport/encoding/admission/execution/decoding; excludes setup and scheduled wait"}
	var writer sync.WaitGroup
	if cfg.WriterRPS > 0 {
		writerCount := (cfg.Count*cfg.WriterRPS + cfg.RPS - 1) / cfg.RPS
		writer.Add(1)
		go func() {
			defer writer.Done()
			report.Writer = runProducer(ctx, report.Started, writerCount, cfg.WriterRPS, cfg.Concurrency, cfg.Timeout, func(ctx context.Context, slot int) error {
				// Same logical mutation sequence in OFF/ON. No endpoint/topology
				// mutation; hidden same-corpus changes may affect visible ranking.
				text := "shared searchable ordinary document"
				if slot%2 == 1 {
					text = "shared ordinary longer updated searchable document"
				}
				response, err := endpoint.client.PutVertices(ctx, authenticated(endpoint.writer, &pb.PutVerticesRequest{Vertices: []*pb.Vertex{
					{Key: "bench:ranking:a", Value: &pb.Vertex_String_{String_: text}},
					{Key: hiddenHit, Value: &pb.Vertex_String_{String_: text}},
				}}))
				if err != nil {
					return err
				}
				if len(response.Msg.Outcomes) != 2 || response.Msg.Outcomes[0] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE || response.Msg.Outcomes[1] != pb.PutOutcome_PUT_OUTCOME_APPLIED_AND_LIVE {
					return errors.New("writer mutation not applied and live")
				}
				return nil
			})
		}()
	}
	report.Reader = runProducer(ctx, report.Started, cfg.Count, cfg.RPS, cfg.Concurrency, cfg.Timeout, func(ctx context.Context, _ int) error { return queryOnce(ctx, endpoint, cfg.Family) })
	writer.Wait()
	for _, producer := range []producerReport{report.Reader, report.Writer} {
		for _, sample := range producer.Samples {
			if sample.Status != "ok" {
				return report, errors.New("offered load or result contract failed; retain every sample")
			}
		}
	}
	return report, nil
}
