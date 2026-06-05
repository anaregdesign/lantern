package mcp

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"testing"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// newTestHarness wires a fake-backed MCP server to an in-memory MCP
// client session. Tests call h.call(name, args) and assert on the
// returned CallToolResult plus the fake's recorded state. The harness
// returns the active *fakeLantern so tests can pre-program responses
// and inspect calls.
func newTestHarness(t *testing.T) *testHarness {
	t.Helper()
	fake := &fakeLantern{}
	r := mustDefaultResolver(t)
	srv := newServer(fake, r, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())

	// Server.Connect returns once the session is established and runs the
	// server loop on a background goroutine. We capture the returned
	// session so the test can shut it down explicitly.
	serverSession, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		cancel()
		t.Fatalf("server.Connect: %v", err)
	}

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	clientSession, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		cancel()
		t.Fatalf("client.Connect: %v", err)
	}

	t.Cleanup(func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
		cancel()
	})

	return &testHarness{
		ctx:    ctx,
		fake:   fake,
		client: clientSession,
	}
}

func mustDefaultResolver(t *testing.T) *ttl.Resolver {
	t.Helper()
	r, err := ttl.LoadFromEnv()
	if err != nil {
		t.Fatalf("ttl.LoadFromEnv: %v", err)
	}
	return r
}

type testHarness struct {
	ctx    context.Context
	fake   *fakeLantern
	client *mcp.ClientSession
}

// call invokes a tool and returns the parsed structured content alongside
// the raw result. The args value must be JSON-marshalable; the harness
// sends it through the wire path the production LLM client uses.
func (h *testHarness) call(t *testing.T, name string, args any) *mcp.CallToolResult {
	t.Helper()
	cctx, cancel := context.WithTimeout(h.ctx, 5*time.Second)
	defer cancel()
	res, err := h.client.CallTool(cctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s) transport error: %v", name, err)
	}
	return res
}

// contentText concatenates the text of all TextContent blocks on a
// result. Useful for asserting error messages that surface via the
// Content body when IsError is true.
func contentText(res *mcp.CallToolResult) string {
	var out string
	for _, c := range res.Content {
		if tc, ok := c.(*mcp.TextContent); ok {
			out += tc.Text
		}
	}
	return out
}

// callExpectError invokes a tool that is expected to surface an error via
// the IsError flag. The caller asserts on the content body.
func (h *testHarness) callExpectError(t *testing.T, name string, args any) *mcp.CallToolResult {
	t.Helper()
	res := h.call(t, name, args)
	if !res.IsError {
		t.Fatalf("CallTool(%s) IsError = false, want true; content=%+v", name, res.Content)
	}
	return res
}

// structuredAs decodes the StructuredContent of a result into v.
func structuredAs(t *testing.T, res *mcp.CallToolResult, v any) {
	t.Helper()
	if res.StructuredContent == nil {
		t.Fatalf("StructuredContent is nil; content=%+v", res.Content)
	}
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal StructuredContent: %v", err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal into %T: %v (raw=%s)", v, err, b)
	}
}
