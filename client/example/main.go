package main

import (
	"context"
	"encoding/json"
	"github.com/anaregdesign/lantern/client"
	"log"
	"time"
)

func main() {
	ctx := context.Background()

	cli, err := client.NewLantern("localhost", 6380)
	if err != nil {
		panic(err)
	}

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
		if v, err := vertex.StringValue(); err == nil {
			log.Printf("%s: %s\n", vertex.Key, v)
		}
	}

	// int value
	if vertex, err := cli.GetVertex(ctx, "int"); err == nil {
		if v, err := vertex.IntValue(); err == nil {
			log.Printf("%s: %d\n", vertex.Key, v)
		}
	}

	// float value
	if vertex, err := cli.GetVertex(ctx, "float"); err == nil {
		if v, err := vertex.FloatValue(); err == nil {
			log.Printf("%s: %f\n", vertex.Key, v)
		}
	}

	// bool value
	if vertex, err := cli.GetVertex(ctx, "bool"); err == nil {
		if v, err := vertex.BoolValue(); err == nil {
			log.Printf("%s: %t\n", vertex.Key, v)
		}
	}

	// time.Time value
	if value, err := cli.GetVertex(ctx, "time"); err == nil {
		if v, err := value.TimeValue(); err == nil {
			log.Printf("%s: %s\n", value.Key, v)
		}
	}

	// []byte value
	if vertex, err := cli.GetVertex(ctx, "[]byte"); err == nil {
		if v, err := vertex.BytesValue(); err == nil {
			log.Printf("%s: %s\n", vertex.Key, v)
		}
	}

	// nil value
	if vertex, err := cli.GetVertex(ctx, "nil"); err == nil {
		log.Printf("%s: %t\n", vertex.Key, vertex.IsNil())
	}

	/*
		DeleteVertex:
	*/

	if err := cli.DeleteVertex(ctx, "string"); err != nil {
		log.Fatal(err)
	}

	if _, err := cli.GetVertex(ctx, "string"); err != nil {
		log.Printf("string vertex is deleted: %s\n", err)
	}

	/*
		AddEdge:
			In Lantern, all edges are additive.
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
		log.Printf("weight at t=1: %f\n", weight)
	}

	// 2 seconds later, first edge is expired
	time.Sleep(2 * time.Second)

	// weight of edge a->b is 1
	if weight, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight at t=3: %f\n", weight)
	}

	// 1 seconds later, second edge is expired
	time.Sleep(1 * time.Second)

	// weight of edge a->b is 0
	if weight, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight at t=4: %f\n", weight)
	}

	/*
		DeleteEdge:
			DeleteEdge deletes an edge between two head and tail.
	*/

	if err := cli.AddEdge(ctx, "a", "b", 1, 1*time.Minute); err != nil {
		log.Fatal(err)
	}

	if w, err := cli.GetEdge(ctx, "a", "b"); err == nil {
		log.Printf("weight of a->b: %f\n", w)
	}

	if err := cli.DeleteEdge(ctx, "a", "b"); err != nil {
		log.Fatal(err)
	}

	// If edge is deleted, weight of edge is 0
	if w, err := cli.GetEdge(ctx, "a", "b"); err != nil {
		log.Printf("Error: %s\n", err)
	} else {
		log.Printf("weight of a->b: %f\n", w)
	}

	/*
		Illuminate:
			Illuminate is a function that returns neighbor graph of a vertex.
			seed is a vertex to start illuminate.
			step is a number of step from seed.
			k is a number of edges from each vertex.
			tfidf is a flag to use tfidf or not. If tfidf is true, weight of edge is calculated by tfidf.
			Else, weight of edge is calculated by weights of edges.

			ex)
			a -> b -> c -> d
			|    +--> e
			+--> f -> g
			+--> h
			+--> i

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

	// illuminate from a with step 2 and k 2
	if graph, err := cli.Illuminate(ctx, "a", 2, 2, false); err == nil {
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
