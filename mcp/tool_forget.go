package mcp

import (
	"context"
	"fmt"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type forgetInput struct {
	Key string `json:"key" jsonschema:"Exact key to delete. Idempotent — deleting a key that does not exist returns existed=false without error."`
}

type forgetOutput struct {
	Key     string `json:"key"`
	Existed bool   `json:"existed"`
}

const forgetDescription = "Delete a fact by exact key. Use this only when a fact is now wrong or the user asks you to forget it — normal staleness is handled by TTL decay, so you rarely need to call this. Idempotent: deleting a missing key returns existed=false rather than erroring. Edges incident to the key are NOT cascade-deleted — they will decay naturally on their own TTL."

func registerForget(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "forget",
		Description: forgetDescription,
	}, func(ctx context.Context, _ *mcp.CallToolRequest, in forgetInput) (*mcp.CallToolResult, forgetOutput, error) {
		if in.Key == "" {
			return nil, forgetOutput{}, fmt.Errorf("forget: key must not be empty")
		}
		existed, err := lc.DeleteVertex(ctx, in.Key)
		if err != nil {
			return nil, forgetOutput{}, mapSDKError("forget", err)
		}
		out := forgetOutput{Key: in.Key, Existed: existed}
		text := fmt.Sprintf("Deleted %q.", in.Key)
		if !existed {
			text = fmt.Sprintf("No fact to delete at %q.", in.Key)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: text}},
		}, out, nil
	})
}
