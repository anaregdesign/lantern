// Command createprobe exercises standalone conditional Edge creation. It never
// calls peer APIs or infers a cluster-wide absence guarantee.
package main

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	client "github.com/anaregdesign/lantern/sdks/go"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"
)

type sample struct {
	Cycles         int     `json:"cycles"`
	ElapsedSeconds float64 `json:"elapsed_seconds"`
	CycleRPS       float64 `json:"cycle_rps"`
	CreateP50MS    float64 `json:"create_p50_ms"`
	CreateP99MS    float64 `json:"create_p99_ms"`
	LookupP99MS    float64 `json:"lookup_p99_ms,omitempty"`
}

func percentile(values []float64, p float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := slices.Clone(values)
	slices.Sort(ordered)
	return ordered[int(float64(len(ordered)-1)*p)]
}
func expectedOutcomes() []client.CreateEdgeOutcome {
	return []client.CreateEdgeOutcome{client.CreateEdgeCreatedAndLive, client.CreateEdgeExists, client.CreateEdgeEndpointNotLive, client.CreateEdgeExpired}
}
func run(ctx context.Context, sdk *client.Lantern, capability client.ReceiptCapability, receipt bool, duration time.Duration, rps int) (sample, error) {
	var report sample
	if duration <= 0 || duration > 5*time.Minute || rps < 1 || rps > 1000 {
		return report, errors.New("bounded duration/rate required")
	}
	inputs := []client.EdgeInput{
		{Tail: "bench:source:a", Head: "bench:target:b", Weight: 2},
		{Tail: "bench:source:a", Head: "bench:target:b", Weight: 9},
		{Tail: "bench:source:a", Head: "bench:target:missing", Weight: 1},
		{Tail: "bench:source:a", Head: "bench:target:b", Weight: 1, Expiration: time.Unix(0, 1)},
	}
	latency, lookup := []float64{}, []float64{}
	clock := time.NewTicker(time.Second / time.Duration(rps))
	defer clock.Stop()
	began := time.Now()
	deadline := began.Add(duration)
	for time.Now().Before(deadline) {
		select {
		case <-ctx.Done():
			return report, ctx.Err()
		case <-clock.C:
		}
		createMS, lookupMS, err := cycle(ctx, sdk, capability, receipt, inputs)
		if err != nil {
			return report, err
		}
		latency = append(latency, createMS)
		if receipt {
			lookup = append(lookup, lookupMS)
		}
		report.Cycles++
	}
	report.ElapsedSeconds = time.Since(began).Seconds()
	report.CycleRPS = float64(report.Cycles) / report.ElapsedSeconds
	report.CreateP50MS = percentile(latency, .5)
	report.CreateP99MS = percentile(latency, .99)
	report.LookupP99MS = percentile(lookup, .99)
	return report, nil
}
func cycle(ctx context.Context, sdk *client.Lantern, capability client.ReceiptCapability, receipt bool, inputs []client.EdgeInput) (float64, float64, error) {
	var rc client.ReceiptContext
	var err error
	if receipt {
		rc, err = sdk.NewReceiptContext(capability, client.ReceiptMutationCreateEdge, len(inputs))
		if err != nil {
			return 0, 0, err
		}
	}
	started := time.Now()
	var got []client.CreateEdgeOutcome
	if receipt {
		got, err = sdk.CreateEdgesWithReceipt(ctx, inputs, rc)
	} else {
		got, err = sdk.CreateEdges(ctx, inputs)
	}
	createMS := float64(time.Since(started)) / float64(time.Millisecond)
	lookupMS := 0.0
	if err != nil {
		return 0, 0, err
	}
	if !slices.Equal(got, expectedOutcomes()) {
		return 0, 0, errors.New("create original outcome contract failed")
	}
	if receipt {
		started = time.Now()
		statuses, err := sdk.GetReceiptStatuses(ctx, rc.OperationIDs)
		lookupMS = float64(time.Since(started)) / float64(time.Millisecond)
		if err != nil {
			return 0, 0, err
		}
		if len(statuses) != len(got) {
			return 0, 0, errors.New("receipt alignment failed")
		}
		for i, status := range statuses {
			if status.State != client.ReceiptConfirmed || status.Receipt == nil {
				return 0, 0, errors.New("receipt confirmation failed")
			}
			original, ok := status.Receipt.OriginalResult.(client.ReceiptCreateEdgeResult)
			if !ok || original.Outcome != got[i] {
				return 0, 0, errors.New("create original receipt result failed")
			}
		}
	}
	existed, err := sdk.DeleteEdge(ctx, inputs[0].Tail, inputs[0].Head)
	if err != nil {
		return 0, 0, err
	}
	if !existed {
		return 0, 0, errors.New("accepted Edge not live after Create")
	}
	return createMS, lookupMS, nil
}

func main() {
	endpoint := flag.String("endpoint", "", "owned standalone HTTPS endpoint")
	caFile := flag.String("ca-file", "", "private fixture CA")
	tokenFile := flag.String("token-file", "", "private raw machine credential")
	out := flag.String("out", "", "content-free report path")
	duration := flag.Duration("duration", 60*time.Second, "steady duration")
	warmup := flag.Duration("warmup", 10*time.Second, "warmup duration")
	rate := flag.Int("rps", 100, "sequential cycle arrival rate")
	receipt := flag.Bool("receipt", false, "include atomic receipt Create and aligned status lookup")
	flag.Parse()
	if err := execute(*endpoint, *caFile, *tokenFile, *out, *warmup, *duration, *rate, *receipt); err != nil {
		fmt.Fprintln(os.Stderr, "createprobe: standalone contract or transport failed")
		os.Exit(1)
	}
}
func execute(endpoint, caFile, tokenFile, out string, warmup, duration time.Duration, rate int, receipt bool) error {
	if !strings.HasPrefix(endpoint, "https://localhost:") || out == "" {
		return errors.New("owned HTTPS fixture endpoint and report required")
	}
	ca, err := os.ReadFile(caFile)
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(ca) {
		return errors.New("invalid CA")
	}
	raw, err := os.ReadFile(tokenFile)
	if err != nil {
		return err
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}, ForceAttemptHTTP2: true}
	defer transport.CloseIdleConnections()
	sdk, err := client.NewLantern(endpoint, client.WithHTTPClient(&http.Client{Transport: transport}), client.WithAuthToken(strings.TrimSpace(string(raw))))
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), warmup+duration+time.Minute)
	defer cancel()
	_, err = sdk.PutVertices(ctx, []client.VertexInput{{Key: "bench:source:a", Value: "source"}, {Key: "bench:target:b", Value: "target"}})
	if err != nil {
		return err
	}
	cap, err := sdk.GetReceiptCapability(ctx)
	if err != nil {
		return err
	}
	if !cap.Enabled || !cap.Supports(client.ReceiptMutationCreateEdge) {
		return errors.New("standalone Create receipt capability missing")
	}
	firstCreate, firstLookup, err := cycle(ctx, sdk, cap, receipt, []client.EdgeInput{
		{Tail: "bench:source:a", Head: "bench:target:b", Weight: 2},
		{Tail: "bench:source:a", Head: "bench:target:b", Weight: 9},
		{Tail: "bench:source:a", Head: "bench:target:missing", Weight: 1},
		{Tail: "bench:source:a", Head: "bench:target:b", Weight: 1, Expiration: time.Unix(0, 1)},
	})
	if err != nil {
		return err
	}
	if warmup > 0 {
		if _, err := run(ctx, sdk, cap, receipt, warmup, rate); err != nil {
			return err
		}
	}
	firstStart := time.Now()
	// First Create follows required endpoint setup/capability preflight and is
	// separate from steady results; it is not a cold Principal/JWT measurement.
	result, err := run(ctx, sdk, cap, receipt, duration, rate)
	if err != nil {
		return err
	}
	if _, err := sdk.GetVertex(ctx, "bench:target:missing"); !errors.Is(err, client.ErrNotFound) {
		return errors.New("create fabricated missing endpoint")
	}
	raw, err = json.MarshalIndent(struct {
		FirstCreateMS     float64 `json:"first_create_after_setup_ms"`
		FirstLookupMS     float64 `json:"first_lookup_ms,omitempty"`
		Schema            int     `json:"schema"`
		Family            string  `json:"family"`
		RPS               int     `json:"target_cycle_rps"`
		Steady            sample  `json:"steady"`
		IncludesPreflight bool    `json:"receipt_create_latency_includes_preflight"`
		Started           string  `json:"started_at"`
	}{firstCreate, firstLookup, 1, map[bool]string{false: "plain", true: "receipt"}[receipt], rate, result, receipt, firstStart.UTC().Format(time.RFC3339Nano)}, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(out, raw, 0600)
}
