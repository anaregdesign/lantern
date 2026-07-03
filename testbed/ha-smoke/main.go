// Compose HA smoke test: hammer all 3 replicas via SDK round-robin LB,
// then verify replication by hitting each per-replica endpoint and
// confirming vertex/edge state agrees.
package main

import (
	"context"
	"fmt"
	"log"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func main() {
	ctx := context.Background()
	endpoints := []string{"http://localhost:6380", "http://localhost:6381", "http://localhost:6382"}

	// 1) Client pinned to the first replica. The SDK has no built-in
	//    round-robin LB — Connect has no transport-level LB concept;
	//    use a reverse proxy / k8s Service for production fan-out. For
	//    the smoke test we drive one replica and verify the others
	//    converge via replication.
	rr, err := client.NewLantern(endpoints[0])
	if err != nil {
		log.Fatalf("NewLantern(%s): %v", endpoints[0], err)
	}
	defer func() { _ = rr.Close() }()

	// 2) Write vertices of every supported kind via round-robin LB.
	now := time.Now()
	type kv struct {
		key string
		val any
	}
	puts := []kv{
		{"str", "hello"},
		{"int", 42},
		{"uint", uint(7)},
		{"float", 3.14},
		{"bool", true},
		{"bytes", []byte("xyz")},
		{"time", now},
		{"dur", 5 * time.Second},
		{"nil", nil},
	}
	for _, p := range puts {
		if err := rr.PutVertex(ctx, p.key, p.val, 1*time.Minute); err != nil {
			log.Fatalf("PutVertex %q: %v", p.key, err)
		}
	}
	fmt.Printf("✓ PutVertex × %d via round-robin\n", len(puts))

	// 3) Build a small graph: A -> B -> C, plus an additive duplicate A -> B.
	for _, k := range []string{"A", "B", "C"} {
		if err := rr.PutVertex(ctx, k, k, 1*time.Minute); err != nil {
			log.Fatalf("PutVertex %q: %v", k, err)
		}
	}
	if _, err := rr.AddEdge(ctx, "A", "B", 1.0, 1*time.Minute); err != nil {
		log.Fatalf("AddEdge A->B: %v", err)
	}
	if _, err := rr.AddEdge(ctx, "A", "B", 1.0, 1*time.Minute); err != nil {
		log.Fatalf("AddEdge A->B (2): %v", err)
	}
	if _, err := rr.AddEdge(ctx, "B", "C", 2.5, 1*time.Minute); err != nil {
		log.Fatalf("AddEdge B->C: %v", err)
	}
	fmt.Printf("✓ AddEdge A->B (×2 additive) + B->C\n")

	// 4) Round-trip read via LB.
	v, err := rr.GetVertex(ctx, "str")
	if err != nil {
		log.Fatalf("GetVertex str: %v", err)
	}
	if got, _ := client.StringValue(v); got != "hello" {
		log.Fatalf("GetVertex str: want %q got %q", "hello", got)
	}
	fmt.Printf("✓ GetVertex str = hello\n")

	// 5) Illuminate from A — should walk A,B,C.
	g, err := rr.Illuminate(ctx, "A", client.WithBFS(client.BFSOpts{Step: 5, FanOut: 100}))
	if err != nil {
		log.Fatalf("Illuminate: %v", err)
	}
	fmt.Printf("✓ Illuminate(A): %d vertices, %d source-rows of edges\n", len(g.Vertices), len(g.Edges))
	for src, dsts := range g.Edges {
		for dst, w := range dsts {
			fmt.Printf("    %s -> %s  weight=%g\n", src, dst, w)
		}
	}

	// 6) Give the replication pump a beat to converge.
	time.Sleep(1500 * time.Millisecond)

	// 7) Verify EACH replica sees the same state, by talking to them
	//    directly (no LB) and confirming our writes are present.
	for _, ep := range endpoints {
		direct, err := client.NewLantern(ep)
		if err != nil {
			log.Fatalf("NewLantern(%s): %v", ep, err)
		}
		// Read the canonical "str" vertex.
		v, err := direct.GetVertex(ctx, "str")
		if err != nil {
			log.Fatalf("[%s] GetVertex str: %v", ep, err)
		}
		s, _ := client.StringValue(v)

		// Illuminate from A and assert A,B,C reachable + A->B weight is additive.
		g, err := direct.Illuminate(ctx, "A", client.WithBFS(client.BFSOpts{Step: 5, FanOut: 100}))
		if err != nil {
			log.Fatalf("[%s] Illuminate: %v", ep, err)
		}
		abWeight := float32(-1)
		if dsts, ok := g.Edges["A"]; ok {
			abWeight = dsts["B"]
		}
		_, hasC := g.Vertices["C"]
		fmt.Printf("✓ %s: str=%q vertices=%d  A->B=%g  hasC=%v\n",
			ep, s, len(g.Vertices), abWeight, hasC)

		if s != "hello" {
			log.Fatalf("[%s] replication missed 'str' vertex", ep)
		}
		if !hasC {
			log.Fatalf("[%s] replication missed C vertex via Illuminate", ep)
		}
		if abWeight < 1.99 { // additive: 1.0 + 1.0 = 2.0
			log.Fatalf("[%s] A->B weight = %g, want ~2.0 (additive)", ep, abWeight)
		}
		_ = direct.Close()
	}

	// 8) Delete via LB; verify delete propagates.
	if _, err := rr.DeleteVertex(ctx, "str"); err != nil {
		log.Fatalf("DeleteVertex str: %v", err)
	}
	time.Sleep(1500 * time.Millisecond)
	for _, ep := range endpoints {
		direct, _ := client.NewLantern(ep)
		_, err := direct.GetVertex(ctx, "str")
		if err == nil {
			log.Fatalf("[%s] delete did not propagate: vertex still present", ep)
		}
		fmt.Printf("✓ %s: 'str' delete propagated (got expected error: %v)\n", ep, err)
		_ = direct.Close()
	}

	fmt.Println("\nALL CHECKS PASSED")
}
