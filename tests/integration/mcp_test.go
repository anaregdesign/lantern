package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	lmcp "github.com/anaregdesign/lantern/mcp"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCP_EndToEnd exercises the 6-tool MCP surface through a real MCP
// client session over InMemoryTransport, backed by a real Lantern
// service (Connect-on-h2c per #350). It is the smoke test that proves
// the whole stack — schema inference, argument validation, SDK
// round-trip, and result shape — agrees end-to-end.
func TestMCP_EndToEnd(t *testing.T) {
	// This suite exercises the LEGACY memory verbs, which live behind
	// LANTERN_MCP_PROFILE=memory since the #851 working-context retarget
	// (context is the default). TestMCP_ContextProfile_EndToEnd below is
	// the default-profile sibling.
	t.Setenv("LANTERN_MCP_PROFILE", lmcp.ProfileMemory)
	lan, cleanup := newInProcessClientWithPrefix(t)
	defer cleanup()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	srv, err := lmcp.NewServer(lan, logger)
	if err != nil {
		t.Fatalf("lmcp.NewServer: %v", err)
	}

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	serverSession, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()

	c := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "test"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("CallTool(%s): %v", name, err)
		}
		if res.IsError {
			t.Fatalf("CallTool(%s) IsError; content=%+v", name, res.Content)
		}
		return res
	}

	decode := func(res *mcp.CallToolResult, v any) {
		t.Helper()
		b, err := json.Marshal(res.StructuredContent)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := json.Unmarshal(b, v); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
	}

	// remember_fact then recall_fact then forget.
	call("remember_fact", map[string]any{"key": "u.tone", "value": "warm", "ttl": "turn"})

	recallRes := call("recall_fact", map[string]any{"key": "u.tone"})
	var recall struct {
		Found bool   `json:"found"`
		Key   string `json:"key"`
		Value any    `json:"value"`
	}
	decode(recallRes, &recall)
	if !recall.Found || recall.Value != "warm" {
		t.Fatalf("recall mismatch: %+v", recall)
	}

	forgetRes := call("forget", map[string]any{"key": "u.tone"})
	var forget struct {
		Existed bool `json:"existed"`
	}
	decode(forgetRes, &forget)
	if !forget.Existed {
		t.Fatalf("expected Existed=true; got %+v", forget)
	}

	missRes := call("recall_fact", map[string]any{"key": "u.tone"})
	var miss struct {
		Found bool `json:"found"`
	}
	decode(missRes, &miss)
	if miss.Found {
		t.Fatalf("expected Found=false after forget; got %+v", miss)
	}

	// Additive relations: write twice, recall_related should see the
	// summed weight to the neighbour.
	call("remember_fact", map[string]any{"key": "fact.a", "value": "A", "ttl": "turn"})
	call("remember_fact", map[string]any{"key": "fact.b", "value": "B", "ttl": "turn"})
	call("remember_relation", map[string]any{"from": "fact.a", "to": "fact.b", "ttl": "turn"})
	call("remember_relation", map[string]any{"from": "fact.a", "to": "fact.b", "ttl": "turn"})

	relRes := call("recall_related", map[string]any{"seed": "fact.a", "step": 1, "k": 4})
	var rel struct {
		Seed      string `json:"seed"`
		Neighbors []struct {
			Key    string  `json:"key"`
			Weight float32 `json:"weight"`
		} `json:"neighbors"`
	}
	decode(relRes, &rel)
	if rel.Seed != "fact.a" {
		t.Fatalf("seed = %q", rel.Seed)
	}
	var seenB bool
	for _, n := range rel.Neighbors {
		if n.Key == "fact.b" {
			seenB = true
			if n.Weight < 1.5 {
				t.Errorf("additive write should yield weight \u2265 1.5; got %v", n.Weight)
			}
		}
	}
	if !seenB {
		t.Fatalf("fact.b missing from neighbours: %+v", rel.Neighbors)
	}

	// list_under should see fact.a + fact.b.
	listRes := call("list_under", map[string]any{"prefix": "fact."})
	var list struct {
		Count   int  `json:"count"`
		HasMore bool `json:"has_more"`
	}
	decode(listRes, &list)
	if list.Count < 2 {
		t.Fatalf("expected at least 2 entries under fact.; got %d", list.Count)
	}
}

// TestMCP_ContextProfile_EndToEnd is the #851 walkthrough over a real
// Lantern service: agent A announces + tracks + claims; the shared board
// (list_agents / whats_happening / list_claims) sees it; a note posted
// against a resource surfaces in its context; release clears the lease.
func TestMCP_ContextProfile_EndToEnd(t *testing.T) {
	t.Setenv("LANTERN_MCP_PROFILE", lmcp.ProfileContext)
	t.Setenv("LANTERN_MCP_AGENT_ID", "") // process identity is memoised; accept whatever it is
	lan, cleanup := newInProcessClientWithPrefix(t)
	defer cleanup()

	logger := slog.New(slog.NewJSONHandler(io.Discard, nil))
	srv, err := lmcp.NewServer(lan, logger)
	if err != nil {
		t.Fatalf("lmcp.NewServer: %v", err)
	}

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	serverSession, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server.Connect: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	c := mcp.NewClient(&mcp.Implementation{Name: "integration-ctx", Version: "test"}, nil)
	cs, err := c.Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client.Connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	call := func(name string, args map[string]any) *mcp.CallToolResult {
		t.Helper()
		res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if res.IsError {
			t.Fatalf("%s returned tool error: %+v", name, res.Content)
		}
		return res
	}
	textOf := func(res *mcp.CallToolResult) string {
		t.Helper()
		for _, content := range res.Content {
			if tc, ok := content.(*mcp.TextContent); ok {
				return tc.Text
			}
		}
		return ""
	}

	// A announces and works.
	call("announce", map[string]any{"task": "refactoring auth middleware"})
	call("track", map[string]any{"resources": []string{"repo.api.middleware.auth", "ticket.API-17"}})
	call("claim", map[string]any{"resource": "repo.api.middleware.auth", "note": "rewriting"})
	call("post_note", map[string]any{
		"text": "auth middleware API changed", "severity": "warn",
		"links": []string{"repo.api.middleware.auth"}, "ttl": "turn",
	})

	// The board sees all of it.
	if got := textOf(call("list_agents", map[string]any{})); !strings.Contains(got, "refactoring auth middleware") {
		t.Fatalf("list_agents missing the announced task: %q", got)
	}
	if got := textOf(call("list_claims", map[string]any{})); !strings.Contains(got, "repo.api.middleware.auth") {
		t.Fatalf("list_claims missing the lease: %q", got)
	}
	happening := textOf(call("whats_happening", map[string]any{"key": "repo.api.middleware.auth"}))
	for _, want := range []string{"refactoring auth middleware", "auth middleware API changed"} {
		if !strings.Contains(happening, want) {
			t.Fatalf("whats_happening missing %q:\n%s", want, happening)
		}
	}

	// Release clears the lease.
	call("release", map[string]any{"resource": "repo.api.middleware.auth"})
	if got := textOf(call("list_claims", map[string]any{})); strings.Contains(got, "repo.api.middleware.auth") {
		t.Fatalf("lease survived release: %q", got)
	}
}
