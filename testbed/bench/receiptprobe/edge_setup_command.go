package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"
)

// This preparation command is outside the offered-load phases. It uses the same
// verified transport and credential, and never grants or caches admission.
func runEdgeSetupCommand(args []string) int {
	if err := executeEdgeSetupCommand(args, os.Getenv("LANTERN_BENCH_AUTH_TOKEN")); err != nil {
		fmt.Fprintf(os.Stderr, "prepare protected EdgeAdd fixture: %v\n", err)
		return 1
	}
	return 0
}

func executeEdgeSetupCommand(args []string, token string) error {
	flags := flag.NewFlagSet("prepare-edge-add", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	addresses := flags.String("endpoints", "", "comma-separated Lantern HTTPS endpoints")
	caFile := flags.String("ca-cert", "", "PEM trust roots")
	requestTimeout := flags.Duration("request-timeout", 5*time.Second, "per-RPC timeout")
	output := flags.String("output", "", "new private endpoint-plan file")
	warmupDuration := flags.Duration("warmup-duration", 0, "warmup offered duration")
	warmupRPS := flags.Int("warmup-pair-rps", 0, "warmup operation pairs per second")
	steadyDuration := flags.Duration("steady-duration", 0, "steady offered duration")
	steadyRPS := flags.Int("steady-pair-rps", 0, "steady operation pairs per second")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 0 || *output == "" || *requestTimeout <= 0 || token == "" {
		return fmt.Errorf("endpoint preparation requires output, positive request timeout, and LANTERN_BENCH_AUTH_TOKEN")
	}
	phases := []edgeSetupPhase{
		{Phase: "warmup", Duration: *warmupDuration, PairRPS: *warmupRPS},
		{Phase: "steady", Duration: *steadyDuration, PairRPS: *steadyRPS},
	}
	for _, phase := range phases {
		if _, err := edgeAddPhaseLimit(phase.Duration, phase.PairRPS); err != nil {
			return err
		}
	}
	endpointAddresses, err := parseEndpoints(*addresses)
	if err != nil {
		return err
	}
	httpClient, err := newVerifiedReceiptHTTPClient(*caFile)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	endpoints, err := discoverEndpoints(ctx, httpClient, endpointAddresses, token, receiptEdgeAdd)
	if err != nil {
		return err
	}
	manifest, err := prepareEdgeAdd(ctx, endpoints, token, *requestTimeout, phases)
	if err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return writeEdgeSetupManifest(*output, manifest)
}
