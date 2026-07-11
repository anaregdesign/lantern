// preflight seeds and validates benchmark topologies that cannot be expressed
// safely as incidental steady-state ghz mutation traffic.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/testbed/bench/topology"
)

func main() {
	endpointsFlag := flag.String("endpoints", "http://localhost:6380", "comma-separated Lantern h2c endpoints")
	reportPath := flag.String("report", "", "write the verified topology report to this path")
	flag.Parse()

	endpoints := strings.Split(*endpointsFlag, ",")
	if len(endpoints) == 0 || endpoints[0] == "" {
		fatalf("at least one endpoint is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	writer, err := client.NewLantern(endpoints[0])
	if err != nil {
		fatalf("dial writer: %v", err)
	}
	defer func() { _ = writer.Close() }()

	report, err := topology.SeedBroadIlluminate(ctx, writer, time.Now().Add(30*time.Minute))
	if err != nil {
		fatalf("seed/verify writer: %v", err)
	}
	for _, endpoint := range endpoints[1:] {
		lantern, err := client.NewLantern(endpoint)
		if err != nil {
			fatalf("dial replica %s: %v", endpoint, err)
		}
		if err := waitForReplica(ctx, lantern); err != nil {
			_ = lantern.Close()
			fatalf("replica %s: %v", endpoint, err)
		}
		replicaReport, err := topology.VerifyBroadIlluminate(ctx, lantern)
		_ = lantern.Close()
		if err != nil {
			fatalf("verify replica %s: %v", endpoint, err)
		}
		if replicaReport != report {
			fatalf("replica %s topology report differs: got %+v, writer %+v", endpoint, replicaReport, report)
		}
	}

	encoded, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		fatalf("encode report: %v", err)
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, append(encoded, '\n'), 0o644); err != nil {
			fatalf("write report: %v", err)
		}
	}
	fmt.Println(string(encoded))
}

func waitForReplica(ctx context.Context, lantern *client.Lantern) error {
	for {
		if _, err := lantern.GetVertex(ctx, topology.WalkSeed); err == nil {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("timed out waiting for %s: %w", topology.WalkSeed, err)
		}
		time.Sleep(250 * time.Millisecond)
	}
}

func fatalf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "preflight: "+format+"\n", args...)
	os.Exit(1)
}
