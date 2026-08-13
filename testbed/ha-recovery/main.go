// Compose HA failure-recovery test:
//  1. seed baseline data via round-robin LB
//  2. stop compose-lantern-1-1 (one of three replicas)
//  3. write new data while it is down (only 2 replicas accept)
//  4. start compose-lantern-1-1 again
//  5. wait for catch-up via Subscribe replay / Snapshot
//  6. assert the restarted replica has every vertex/edge
package main

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"strings"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// Since #435 the canonical compose declares three explicit lantern-{0,1,2}
// services, so Compose names containers `${PROJECT}-${SERVICE}-1` (the
// trailing `-1` is the per-service replica index, always 1). When you run
// `docker compose up -d` from deploy/compose/, the project defaults to the
// directory name `compose`, giving these container names.
const victimContainer = "compose-lantern-1-1"

var containers = []string{"compose-lantern-0-1", "compose-lantern-1-1", "compose-lantern-2-1"}

func docker(args ...string) {
	out, err := exec.Command("docker", args...).CombinedOutput()
	if err != nil {
		log.Fatalf("docker %s: %v\n%s", strings.Join(args, " "), err, out)
	}
}

// hostPort returns the http:// URL mapped to the container's :6380.
func hostPort(container string) string {
	out, err := exec.Command("docker", "port", container, "6380/tcp").Output()
	if err != nil {
		log.Fatalf("docker port %s: %v", container, err)
	}
	// e.g. "0.0.0.0:6384\n[::]:6384\n"
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if i := strings.LastIndex(line, ":"); i >= 0 {
			return "http://localhost:" + strings.TrimSpace(line[i+1:])
		}
	}
	log.Fatalf("docker port %s: no mapping in %q", container, string(out))
	return ""
}

func waitReady(ep string, timeout time.Duration) {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		cli, err := client.NewLantern(ep)
		if err == nil {
			if outcome, err := cli.PutVertex(ctx, "__probe__", 1, 5*time.Second); err == nil && outcome == client.PutOutcomeAppliedAndLive {
				_, _ = cli.DeleteVertex(ctx, "__probe__")
				_ = cli.Close()
				return
			}
			_ = cli.Close()
		}
		time.Sleep(300 * time.Millisecond)
	}
	log.Fatalf("waitReady(%s): timeout after %s", ep, timeout)
}

func seedBaseline(ctx context.Context, c *client.Lantern) {
	for _, k := range []string{"v1", "v2", "v3"} {
		outcome, err := c.PutVertex(ctx, k, k+"-baseline", 5*time.Minute)
		if err != nil {
			log.Fatalf("seed PutVertex %q: %v", k, err)
		}
		requireApplied("seed PutVertex "+k, outcome)
	}
	if _, err := c.AddEdge(ctx, "v1", "v2", 1.0, 5*time.Minute); err != nil {
		log.Fatalf("seed AddEdge v1->v2: %v", err)
	}
	if _, err := c.AddEdge(ctx, "v2", "v3", 2.0, 5*time.Minute); err != nil {
		log.Fatalf("seed AddEdge v2->v3: %v", err)
	}
}

func writeDuringOutage(ctx context.Context, eps []string) {
	// The SDK has no built-in client-side LB — Connect leaves fan-out
	// to the deployment (reverse proxy / k8s Service). For this test
	// we pin to the first survivor; the outage scenario tests
	// *replication*, not client-side failover.
	cli, err := client.NewLantern(eps[0])
	if err != nil {
		log.Fatalf("survivor client: %v", err)
	}
	defer func() { _ = cli.Close() }()
	for _, k := range []string{"new1", "new2", "new3", "new4", "new5"} {
		outcome, err := cli.PutVertex(ctx, k, k+"-during-outage", 5*time.Minute)
		if err != nil {
			log.Fatalf("outage-write PutVertex %q: %v", k, err)
		}
		requireApplied("outage-write PutVertex "+k, outcome)
	}
	if _, err := cli.AddEdge(ctx, "v3", "new1", 3.0, 5*time.Minute); err != nil {
		log.Fatalf("outage-write AddEdge v3->new1: %v", err)
	}
	if _, err := cli.AddEdge(ctx, "new1", "new2", 4.0, 5*time.Minute); err != nil {
		log.Fatalf("outage-write AddEdge new1->new2: %v", err)
	}
	// Mutate a baseline vertex during outage to check converging replace.
	outcome, err := cli.PutVertex(ctx, "v1", "v1-mutated", 5*time.Minute)
	if err != nil {
		log.Fatalf("outage-write PutVertex v1 mutate: %v", err)
	}
	requireApplied("outage-write PutVertex v1 mutate", outcome)
}

func requireApplied(operation string, outcome client.PutOutcome) {
	if outcome != client.PutOutcomeAppliedAndLive {
		log.Fatalf("%s returned %s", operation, outcome)
	}
}

func assertHasAll(ep string, vertices []string) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cli, err := client.NewLantern(ep)
	if err != nil {
		log.Fatalf("NewLantern(%s): %v", ep, err)
	}
	defer func() { _ = cli.Close() }()
	for _, k := range vertices {
		v, err := cli.GetVertex(ctx, k)
		if err != nil {
			log.Fatalf("[%s] missing %q after recovery: %v", ep, k, err)
		}
		_ = v
	}
	// Check the mutated value is the new one.
	v, err := cli.GetVertex(ctx, "v1")
	if err != nil {
		log.Fatalf("[%s] missing v1: %v", ep, err)
	}
	s, _ := client.StringValue(v)
	if s != "v1-mutated" {
		log.Fatalf("[%s] v1 = %q, want %q (mutation did not converge)", ep, s, "v1-mutated")
	}
	// Check edge weight.
	g, err := cli.Illuminate(ctx, "v1", client.WithBFS(client.BFSOpts{Step: 5, FanOut: 100}))
	if err != nil {
		log.Fatalf("[%s] Illuminate: %v", ep, err)
	}
	if _, ok := g.Vertices["new2"]; !ok {
		log.Fatalf("[%s] new2 not reachable from v1 after recovery (vertices=%v)", ep, keys(g.Vertices))
	}
}

func keys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

func metricsFor(container string) string {
	out, err := exec.Command("docker", "exec", container, "wget", "-qO-", "http://localhost:9090/metrics").Output()
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, line := range strings.Split(string(out), "\n") {
		if strings.HasPrefix(line, "lantern_vertices ") ||
			strings.HasPrefix(line, "lantern_edges ") ||
			strings.HasPrefix(line, "lantern_replication_applied_total") ||
			strings.HasPrefix(line, "lantern_replication_lag_seq") ||
			strings.HasPrefix(line, "lantern_anti_entropy_cycles_total") ||
			strings.HasPrefix(line, "lantern_subscribe_active_streams") {
			b.WriteString("    " + line + "\n")
		}
	}
	return b.String()
}

func main() {
	ctx := context.Background()

	// Resolve host-port mappings. Since #435 these are pinned in the
	// canonical compose (6380/6381/6382), but we still query at runtime
	// so a bench override that hops back to dynamic ports would keep
	// working.
	endpointByContainer := map[string]string{}
	for _, c := range containers {
		endpointByContainer[c] = hostPort(c)
	}
	allEndpoints := []string{
		endpointByContainer["compose-lantern-0-1"],
		endpointByContainer["compose-lantern-1-1"],
		endpointByContainer["compose-lantern-2-1"],
	}
	victimEndpoint := endpointByContainer[victimContainer]
	survivorEndpoints := []string{}
	for _, c := range containers {
		if c != victimContainer {
			survivorEndpoints = append(survivorEndpoints, endpointByContainer[c])
		}
	}
	fmt.Printf("endpoints: all=%v victim=%s survivors=%v\n", allEndpoints, victimEndpoint, survivorEndpoints)

	// 0) Wait for all 3 replicas to be ready (caller is expected to have
	//    already run `docker compose up -d`).
	for _, ep := range allEndpoints {
		waitReady(ep, 30*time.Second)
	}
	fmt.Println("✓ all 3 replicas reachable")

	// 1) Seed baseline via the first replica. Same rationale as
	//    writeDuringOutage: client-side fan-out / LB has moved out of
	//    the SDK since #367.
	rr, err := client.NewLantern(allEndpoints[0])
	if err != nil {
		log.Fatal(err)
	}
	seedBaseline(ctx, rr)
	_ = rr.Close()
	time.Sleep(500 * time.Millisecond)
	fmt.Println("✓ baseline seeded (v1,v2,v3 + 2 edges)")

	// 2) Stop one replica.
	fmt.Printf("\n--- stopping %s ---\n", victimContainer)
	docker("stop", victimContainer)
	// Give the LB a moment to evict the dead subchannel.
	time.Sleep(1 * time.Second)
	fmt.Printf("✓ %s stopped\n", victimContainer)

	// 3) Write new data via the surviving 2 replicas only.
	writeDuringOutage(ctx, survivorEndpoints)
	fmt.Println("✓ wrote new1..new5 + 2 edges + mutated v1 while victim was down")

	// 4) Confirm survivors converged on the new state.
	for _, ep := range survivorEndpoints {
		assertHasAll(ep, []string{"v1", "v2", "v3", "new1", "new2", "new3", "new4", "new5"})
		fmt.Printf("✓ survivor %s has all expected state\n", ep)
	}

	// 5) Restart the victim.
	fmt.Printf("\n--- starting %s ---\n", victimContainer)
	docker("start", victimContainer)
	// Ports are pinned in the canonical compose, but re-resolve in case a
	// bench override has switched back to dynamic mode:host ranges.
	victimEndpoint = hostPort(victimContainer)
	fmt.Printf("    (post-restart host port: %s)\n", victimEndpoint)
	waitReady(victimEndpoint, 60*time.Second)
	fmt.Printf("✓ %s back online\n", victimContainer)

	// 6) Allow catch-up time for snapshot/subscribe replay.
	fmt.Println("... waiting 5s for catch-up via snapshot/subscribe replay ...")
	time.Sleep(5 * time.Second)

	// 7) Assert the recovered replica has every write made during outage.
	assertHasAll(victimEndpoint, []string{"v1", "v2", "v3", "new1", "new2", "new3", "new4", "new5"})
	fmt.Printf("✓ recovered replica %s caught up to full state\n", victimEndpoint)

	// 8) Per-replica metric dump.
	fmt.Println("\n--- per-replica metrics ---")
	for _, c := range containers {
		fmt.Printf("=== %s ===\n%s", c, metricsFor(c))
	}

	fmt.Println("\nFAILURE-RECOVERY TEST PASSED")
}
