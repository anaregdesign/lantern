package main

import (
	"context"
	"encoding/json"
	"github.com/anaregdesign/lantern/client"
	"time"
)

func main() {
	ctx := context.Background()

	cli, err := client.NewLantern("localhost", 6380)
	if err != nil {
		panic(err)
	}

	if err := cli.AddEdge(ctx, "a", "b", 10, 5*time.Second); err != nil {
		panic(err)
	}
	if err := cli.PutVertex(ctx, "a", "A", 5*time.Second); err != nil {
		panic(err)
	}
	//if err := cli.PutVertex(ctx, "b", "B", time.Now().Add(3*time.Second)); err != nil {
	//	panic(err)
	//}

	if g, err := cli.Illuminate(ctx, "a", 2, 3, true); err == nil {
		jsonText, err := json.MarshalIndent(g, "", "\t")
		if err != nil {

		}
		println(string(jsonText))
	} else {
		panic(err)
	}

}
