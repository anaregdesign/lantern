package main

import (
	"context"
	"encoding/json"
	"github.com/anaregdesign/lantern/core/graphcache"
	"time"
)

func main() {
	ctx := context.Background()
	c := graphcache.NewGraphCache[string, string](1 * time.Minute)
	go c.Watch(ctx, 1*time.Second)

	c.AddEdge("a", "b", 1)
	c.AddEdge("b", "c", 1)
	c.AddEdge("c", "d", 1)
	c.AddEdge("a", "b", 1)
	c.AddEdge("a", "c", 1)
	c.AddEdge("a", "d", 1)
	c.AddEdge("a", "e", 1)

	start := time.Now()
	g := c.Neighbor("a", 3, 3, graphcache.WeightingTFIDF, false, nil)
	end := time.Now()

	if jsonText, err := json.MarshalIndent(g, "", "\t"); err == nil {
		println(string(jsonText))
	}

	println("Time: ", end.Sub(start).String())

	/*
			## Output:
			{
		        "vertices": {
		                "a": "",
		                "b": "",
		                "c": "",
		                "d": "",
		                "e": ""
		        },
		        "edges": {
		                "a": {
		                        "b": 2,
		                        "c": 0.63092977,
		                        "e": 1
		                },
		                "b": {
		                        "c": 0.63092977
		                },
		                "c": {
		                        "d": 0.63092977
		                }
		        }
		}
		Time:  106.926µs
	*/
}
