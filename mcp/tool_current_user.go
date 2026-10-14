package mcp

import (
	"context"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type currentUserInput struct{}
type currentUserOutput struct {
	UserID *string `json:"user_id"`
}

func registerCurrentUser(srv *mcp.Server, lc lanternClient) {
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "current_user",
		Description: "Read the configured Lantern client's authenticated user_id. Null means an authenticated UID-less or machine Principal; missing authentication is an error. This is the backend credential's identity, not the MCP caller or an announced agent. Requires no data/admin Role and grants no authority.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ currentUserInput) (*mcp.CallToolResult, currentUserOutput, error) {
		id, err := lc.GetCurrentUserID(ctx)
		if err != nil {
			return nil, currentUserOutput{}, mapSDKError("current_user", err)
		}
		return nil, currentUserOutput{UserID: id}, nil
	})
}
