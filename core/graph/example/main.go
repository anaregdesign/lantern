package main

import (
	"encoding/json"
	"github.com/anaregdesign/lantern/core/graph"
	"log"
)

func main() {
	g := graph.NewGraph[string, string]()
	g.PutVertex("a", "A")
	g.PutVertex("b", "B")
	g.PutVertex("c", "C")
	g.PutVertex("d", "D")
	g.PutEdge("d", "a", 1)
	g.PutEdge("a", "b", 1)
	g.PutEdge("a", "c", 2)
	g.PutEdge("b", "a", 3)
	g.PutEdge("b", "c", 4)
	g.PutEdge("c", "a", 100)
	g.PutEdge("c", "b", 6)

	log.Println("Original graph")
	if jsonText, err := json.MarshalIndent(g, "", "\t"); err == nil {
		log.Println(string(jsonText))
	}
	/*
		{
		        "vertices": {
		                "a": "A",
		                "b": "B",
		                "c": "C",
		                "d": "D"
		        },
		        "edges": {
		                "a": {
		                        "b": 1,
		                        "c": 2
		                },
		                "b": {
		                        "a": 3,
		                        "c": 4
		                },
		                "c": {
		                        "a": 100,
		                        "b": 6
		                },
		                "d": {
		                        "d": 1
		                }
		        }
		}
	*/

	log.Println("Connected graph from vertex 'a'")
	connected := g.ConnectedGraph("a")
	if jsonText, err := json.MarshalIndent(connected, "", "\t"); err == nil {
		log.Println(string(jsonText))
	}

	/*
		 {
		        "vertices": {
		                "a": "A",
		                "b": "B",
		                "c": "C"
		        },
		        "edges": {
		                "a": {
		                        "b": 1,
		                        "c": 2
		                },
		                "b": {
		                        "a": 3,
		                        "c": 4
		                },
		                "c": {
		                        "a": 100,
		                        "b": 6
		                }
		        }
		}
	*/

	log.Println("Minimum Spanning Tree from vertex 'a'")
	mst := g.MinimumSpanningTree("a")
	if jsonText, err := json.MarshalIndent(mst, "", "\t"); err == nil {
		log.Println(string(jsonText))
	}

	/*
		{
		        "vertices": {
		                "a": "A",
		                "b": "B",
		                "c": "C"
		        },
		        "edges": {
		                "a": {
		                        "b": 1,
		                        "c": 2
		                }
		        }
		}


	*/

	log.Println("Shortest Path Tree from vertex 'a'")
	spt := g.ShortestPathTree("a", func(weight float32) float32 { return 1 / weight })
	if jsonText, err := json.MarshalIndent(spt, "", "\t"); err == nil {
		log.Println(string(jsonText))
	}

	/*
		{
		        "vertices": {
		                "a": "A",
		                "b": "B",
		                "c": "C"
		        },
		        "edges": {
		                "a": {
		                        "c": 2
		                },
		                "c": {
		                        "b": 6
		                }
		        }
		}


	*/

}
