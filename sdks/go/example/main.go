package main

import (
	"context"
	"encoding/json"
	"log"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func main() {
	ctx := context.Background()

	cli, err := client.NewLantern("http://localhost:6380")
	if err != nil {
		panic(err)
	}

	// Opt-in retry + static-endpoint failover + idempotent adds (#849).
	retryAndFailoverExample(ctx)

	/*
		PutVertex:
		    Value can be string, int, float, bool, time.Time, []byte or nil
	*/
	// string value
	if err := cli.PutVertex(ctx, "string", "A", 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	// int value
	if err := cli.PutVertex(ctx, "int", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	// float value
	if err := cli.PutVertex(ctx, "float", 1.1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	// bool value
	if err := cli.PutVertex(ctx, "bool", true, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	// time.Time value
	if err := cli.PutVertex(ctx, "time", time.Now(), 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	// []byte value
	if err := cli.PutVertex(ctx, "[]byte", []byte("A"), 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	// nil value
	if err := cli.PutVertex(ctx, "nil", nil, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	/*
		GetVertex:
	*/
	// string value
	if vertex, err := cli.GetVertex(ctx, "string"); err == nil {
		if v, err := client.StringValue(vertex); err == nil {
			log.Printf("%s: %s\n", vertex.Key, v)
		}
	}

	// int value
	if vertex, err := cli.GetVertex(ctx, "int"); err == nil {
		if v, err := client.IntValue(vertex); err == nil {
			log.Printf("%s: %d\n", vertex.Key, v)
		}
	}

	// float value
	if vertex, err := cli.GetVertex(ctx, "float"); err == nil {
		if v, err := client.FloatValue(vertex); err == nil {
			log.Printf("%s: %f\n", vertex.Key, v)
		}
	}

	// bool value
	if vertex, err := cli.GetVertex(ctx, "bool"); err == nil {
		if v, err := client.BoolValue(vertex); err == nil {
			log.Printf("%s: %t\n", vertex.Key, v)
		}
	}

	// time.Time value
	if value, err := cli.GetVertex(ctx, "time"); err == nil {
		if v, err := client.TimeValue(value); err == nil {
			log.Printf("%s: %s\n", value.Key, v)
		}
	}

	// []byte value
	if vertex, err := cli.GetVertex(ctx, "[]byte"); err == nil {
		if v, err := client.BytesValue(vertex); err == nil {
			log.Printf("%s: %s\n", vertex.Key, v)
		}
	}

	// nil value
	if vertex, err := cli.GetVertex(ctx, "nil"); err == nil {
		log.Printf("%s: %t\n", vertex.Key, client.IsNil(vertex))
	}

	/*
		DeleteVertex:
	*/

	if existed, err := cli.DeleteVertex(ctx, "string"); err != nil {
		log.Fatal(err)
	} else {
		log.Printf("DeleteVertex(\"string\"): existed=%v\n", existed)
	}

	if _, err := cli.GetVertex(ctx, "string"); err != nil {
		log.Printf("string vertex is deleted: %s\n", err)
	}

	/*
		AddEdge:
			In Lantern, all edges are additive. So this method is not idempotent.
			For example, if you add an edge with a weight of 1 between A and B twice,
			the weight of the edge will be 2.
		    But each weight will expire with TTL independently.

			ex)
			* add edge a->b with a weight 1 and TTL 3 seconds
			* 1 second later
			* add edge a->b with a weight 1 and TTL 3 seconds
			* weight of edge a->b is 2
			* 2 seconds later, first edge is expired
			* weight of edge a->b is 1
			* 1 seconds later, second edge is expired
			* weight of edge a->b is 0
	*/

	// add edge a->b with a weight 1 and TTL 3 seconds
	if err := cli.AddEdge(ctx, "a", "b", 1, 3*time.Second); err != nil {
		log.Fatal(err)
	}

	// 1 second later
	time.Sleep(1 * time.Second)

	// add edge a->b with a weight 1 and TTL 3 seconds
	if err := cli.AddEdge(ctx, "a", "b", 1, 3*time.Second); err != nil {
		log.Fatal(err)
	}

	// weight of edge a->b is 2
	if weight, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight at t=1: %f\n", weight.Weight)
	}

	// 2 seconds later, first edge is expired
	time.Sleep(2 * time.Second)

	// weight of edge a->b is 1
	if weight, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight at t=3: %f\n", weight.Weight)
	}

	// 1 seconds later, second edge is expired
	time.Sleep(1 * time.Second)

	// weight of edge a->b is 0
	if weight, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight at t=4: %f\n", weight.Weight)
	}

	/*
		DeleteEdge:
			DeleteEdge deletes an edge between two head and tail.
	*/

	if err := cli.AddEdge(ctx, "a", "b", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	if w, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight of a->b: %f\n", w.Weight)
	}

	if existed, err := cli.DeleteEdge(ctx, "a", "b"); err != nil {
		log.Fatal(err)
	} else {
		log.Printf("DeleteEdge(\"a\", \"b\"): existed=%v\n", existed)
	}

	// If edge is deleted, weight of edge is 0
	if w, err := cli.GetEdge(ctx, "a", "b"); err != nil {
		log.Printf("Error: %s\n", err)
	} else {
		log.Printf("weight of a->b: %f\n", w.Weight)
	}

	/*
		PutEdge:
			PutEdge is idempotent version of AddEdge.
	*/
	// put edge a->b with a weight 1 and TTL 1 second twice
	if err := cli.PutEdge(ctx, "a", "b", 1, 1*time.Second); err != nil {
		log.Fatal(err)
	}
	if err := cli.PutEdge(ctx, "a", "b", 1, 1*time.Second); err != nil {
		log.Fatal(err)
	}

	// weight of edge a->b is 1
	if weight, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight at t=1: %f\n", weight.Weight)
	}

	time.Sleep(1 * time.Second)

	/*
		Illuminate:
			Illuminate returns a subgraph rooted at `seed`. The walk runs
			server-side; select the traversal family with a typed per-family
			option (#846) and combine with the shared axes:

				- WithBFS(client.BFSOpts{Step, FanOut, Objective, Reduction})
				  Reduction: ReductionNone | ReductionMinimumSpanningTree | ReductionShortestPathTree
				  Objective: ObjectiveMinimize | ObjectiveMaximize
				- WithPPR(client.PPROpts{TopN, RestartProb, Epsilon})
				- WithWeighting: WeightingRaw | WeightingTFIDF | WeightingBM25
				- WithVertexPrefix: frontier prefix filter

			ex)
			a -> b -> c -> d
			|    +--> e
			+--> f -> g
			+--> h
			+--> i

			A fourth, orthogonal axis scopes the traversal frontier:

				- WithVertexPrefix: restrict the walk to vertices whose key
				  shares the given prefix (the seed is always kept as the
				  anchor; empty = no filter). The filter is applied before
				  per-hop top-k and before any MST/SPT reduction, so the
				  result is the prefix-induced subgraph.

	*/
	// Add edges
	if err := cli.AddEdge(ctx, "a", "b", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}
	if err := cli.AddEdge(ctx, "b", "c", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}
	if err := cli.AddEdge(ctx, "c", "d", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}
	if err := cli.AddEdge(ctx, "b", "e", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}
	if err := cli.AddEdge(ctx, "a", "f", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}
	if err := cli.AddEdge(ctx, "f", "g", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	/*
		Prefix scan: enumerate, count, and bulk-delete vertices whose key
		starts with a common prefix. Requires the server to have the prefix
		index enabled (production wiring does this automatically).

		IMPORTANT: when seeding vertices for a scan, ALWAYS pass a non-zero
		Expiration (or use the helper Lantern.PutVertex with a positive TTL).
		Without it, the proto Expiration defaults to the zero timestamp
		(1970-01-01) and the cache treats every vertex as born-expired —
		CountVerticesByPrefix will return a non-zero count (radix only) while
		ScanVertices silently yields nothing.
	*/
	exp := time.Now().Add(1 * time.Hour)
	prefixSeed := []client.VertexInput{
		{Key: "users/alice", Value: "alice", Expiration: exp},
		{Key: "users/bob", Value: "bob", Expiration: exp},
		{Key: "users/carol", Value: "carol", Expiration: exp},
		{Key: "orders/1", Value: 1, Expiration: exp},
		{Key: "orders/2", Value: 2, Expiration: exp},
	}
	if err := cli.PutVertices(ctx, prefixSeed); err != nil {
		log.Fatal(err)
	}

	if n, err := cli.CountVerticesByPrefix(ctx, "users/"); err == nil {
		log.Printf("count users/ = %d\n", n) // 3
	}

	// Single page scan with explicit cursor.
	if vs, next, err := cli.ScanVertices(ctx, "users/", client.WithScanLimit(2)); err == nil {
		log.Printf("first page returned %d vertices; more=%v", len(vs), len(next) > 0)
	}

	// Iterator covering every page.
	total := 0
	for batch, err := range cli.ScanVerticesAll(ctx, "users/", 2) {
		if err != nil {
			log.Fatal(err)
		}
		total += len(batch)
	}
	log.Printf("iterator total = %d\n", total) // 3

	// Dry-run delete first, then real delete.
	if matched, err := cli.DeleteVerticesByPrefix(ctx, "orders/", client.WithDryRun()); err == nil {
		log.Printf("would delete %d orders/ vertices\n", matched)
	}
	if deleted, err := cli.DeleteVerticesByPrefix(ctx, "orders/"); err == nil {
		log.Printf("deleted %d orders/ vertices\n", deleted)
	}

	// illuminate from a with step 2 and fan-out 2
	if graph, err := cli.Illuminate(ctx, "a", client.WithBFS(client.BFSOpts{Step: 2, FanOut: 2})); err == nil {
		if jsonString, err := json.MarshalIndent(graph, "", "\t"); err == nil {
			log.Printf("%s\n", jsonString)
			/*
				 {
				        "vertices": {
				                "a": {
				                        "Value": {
				                                "Nil": true
				                        }
				                },
				                "b": {
				                        "Value": {
				                                "Nil": true
				                        }
				                },
				                "c": {
				                        "Value": {
				                                "Nil": true
				                        }
				                },
				                "e": {
				                        "Value": {
				                                "Nil": true
				                        }
				                },
				                "f": {
				                        "Value": {
				                                "Nil": true
				                        }
				                },
				                "g": {
				                        "Value": {
				                                "Nil": true
				                        }
				                }
				        },
				        "edges": {
				                "a": {
				                        "b": 1,
				                        "f": 1
				                },
				                "b": {
				                        "c": 1,
				                        "e": 1
				                },
				                "f": {
				                        "g": 1
				                }
				        }
				}
			*/
		}
	}
}

// retryAndFailoverExample shows the opt-in retry policy (#849) composed with
// static-endpoint failover and idempotent adds. Retries are OFF unless
// WithRetry is passed, and even then apply ONLY to RPCs that are idempotent
// under the client's configuration: reads, Put*/Delete*, and — because
// WithIdempotentAdds stamps a stable per-edge ContribID — AddEdge(s). A
// deterministic error (NotFound, InvalidArgument) or an exhausted deadline is
// never retried.
func retryAndFailoverExample(ctx context.Context) {
	// Single endpoint with a bounded, full-jitter exponential backoff. A
	// transient Unavailable (rolling update, load-balancer flap) is retried
	// transparently up to MaxAttempts; anything deterministic surfaces at once.
	retrying, err := client.NewLantern(
		"http://localhost:6380",
		client.WithIdempotentAdds(),
		client.WithRetry(client.RetryPolicy{
			MaxAttempts: 4,
			BaseDelay:   50 * time.Millisecond,
			MaxDelay:    2 * time.Second,
		}),
	)
	if err != nil {
		log.Printf("retry example: dial: %v", err)
		return
	}
	defer func() { _ = retrying.Close() }()

	// AddEdges is retried safely: WithIdempotentAdds stamps a stable ContribID
	// per edge, so a re-sent chunk records each weight exactly once. (Distinct
	// keys keep this demo out of the Illuminate graph printed above.)
	if err := retrying.AddEdges(ctx, []client.EdgeInput{
		{Tail: "retry-demo:a", Head: "retry-demo:b", Weight: 1},
		{Tail: "retry-demo:b", Head: "retry-demo:c", Weight: 1},
	}); err != nil {
		log.Printf("retry example: AddEdges: %v", err)
	}

	// Failover across a fixed replica set. The retry loop drives the ring
	// walk: an Unavailable endpoint is retried against its siblings with
	// backoff, so MaxAttempts is the cross-replica budget — no second rotation
	// mechanism. Construction is lazy (no dial here); swap in real replicas.
	failover, err := client.NewLanternFailover(
		[]string{
			"http://localhost:6380",
			"http://localhost:6381",
			"http://localhost:6382",
		},
		client.WithIdempotentAdds(),
		client.WithRetry(client.RetryPolicy{MaxAttempts: 6}),
	)
	if err != nil {
		log.Printf("retry example: failover: %v", err)
		return
	}
	defer func() { _ = failover.Close() }()
}
