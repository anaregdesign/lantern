package main

import (
	"context"
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
		Put vertex
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

	/*
		Get vertices
	*/
	// string value
	if value, err := cli.GetVertex(ctx, "string"); err != nil {
		if v, err := value.StringValue(); err != nil {
			log.Printf("string: %s\n", v)
		}
	}

	// int value
	if value, err := cli.GetVertex(ctx, "int"); err != nil {
		if v, err := value.IntValue(); err != nil {
			log.Printf("int: %d\n", v)
		}
	}

	// float value
	if value, err := cli.GetVertex(ctx, "float"); err != nil {
		if v, err := value.FloatValue(); err != nil {
			log.Printf("float: %f\n", v)
		}
	}

	// bool value
	if value, err := cli.GetVertex(ctx, "bool"); err != nil {
		if v, err := value.BoolValue(); err != nil {
			log.Printf("bool: %t\n", v)
		}
	}

	// time.Time value
	if value, err := cli.GetVertex(ctx, "time"); err != nil {
		if v, err := value.TimeValue(); err != nil {
			log.Printf("time: %s\n", v)
		}
	}

	/*
		Add edge:
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
			* 3 seconds later, second edge is expired
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
	if weight, err := cli.GetEdge(ctx, "a", "b"); err != nil {
		log.Printf("weight at t=1: %d\n", weight)
	} else {
		log.Fatal(err)
	}

	// 2 seconds later, first edge is expired
	time.Sleep(2 * time.Second)

	// weight of edge a->b is 1
	if weight, err := cli.GetEdge(ctx, "a", "b"); err != nil {
		log.Printf("weight at t=3: %d\n", weight)
	} else {
		log.Fatal(err)
	}

	// 3 seconds later, second edge is expired
	time.Sleep(3 * time.Second)

	// weight of edge a->b is 0
	if weight, err := cli.GetEdge(ctx, "a", "b"); err != nil {
		log.Printf("weight at t=6: %d\n", weight)
	} else {
		log.Fatal(err)
	}

}
