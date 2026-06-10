package mcp

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestDefaultConfig_DefaultsWhenUnset(t *testing.T) {
	t.Setenv("LANTERN_ADDR", "")
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "")
	t.Setenv("LANTERN_MCP_HTTP_ADDR", "")
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig() returned error: %v", err)
	}
	if cfg.LanternAddr != "http://localhost:6380" {
		t.Fatalf("LanternAddr = %q, want http://localhost:6380", cfg.LanternAddr)
	}
	if cfg.PingTimeout != 5*time.Second {
		t.Fatalf("PingTimeout = %v, want 5s", cfg.PingTimeout)
	}
	if cfg.HTTPAddr != "127.0.0.1:6390" {
		t.Fatalf("HTTPAddr = %q, want 127.0.0.1:6390", cfg.HTTPAddr)
	}
	if cfg.Logger == nil {
		t.Fatal("Logger = nil, want non-nil")
	}
}

func TestDefaultConfig_HonorsEnv(t *testing.T) {
	t.Setenv("LANTERN_ADDR", "lantern.internal:9000")
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "750ms")
	t.Setenv("LANTERN_MCP_HTTP_ADDR", "0.0.0.0:7000")
	cfg, err := DefaultConfig()
	if err != nil {
		t.Fatalf("DefaultConfig() returned error: %v", err)
	}
	if cfg.LanternAddr != "lantern.internal:9000" {
		t.Fatalf("LanternAddr = %q, want lantern.internal:9000", cfg.LanternAddr)
	}
	if cfg.PingTimeout != 750*time.Millisecond {
		t.Fatalf("PingTimeout = %v, want 750ms", cfg.PingTimeout)
	}
	if cfg.HTTPAddr != "0.0.0.0:7000" {
		t.Fatalf("HTTPAddr = %q, want 0.0.0.0:7000", cfg.HTTPAddr)
	}
}

func TestDefaultConfig_RejectsMalformedTimeout(t *testing.T) {
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "fortnight")
	_, err := DefaultConfig()
	if err == nil {
		t.Fatal("DefaultConfig() returned nil error for malformed timeout")
	}
	if !strings.Contains(err.Error(), "LANTERN_MCP_PING_TIMEOUT") {
		t.Fatalf("error %q does not mention the env var name", err)
	}
}

func TestDefaultConfig_RejectsNonPositiveTimeout(t *testing.T) {
	t.Setenv("LANTERN_MCP_PING_TIMEOUT", "0s")
	_, err := DefaultConfig()
	if err == nil {
		t.Fatal("DefaultConfig() returned nil error for 0s timeout")
	}
}

// newHandlerTestServer builds a fake-backed MCP server and serves it over
// HTTP via mcpHTTPHandler on an ephemeral httptest listener. It returns
// the base URL (no path) so callers can hit /healthz or /mcp.
func newHandlerTestServer(t *testing.T) string {
	t.Helper()
	fake := &fakeLantern{}
	r := mustDefaultResolver(t)
	srv := newServer(fake, r, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	ts := httptest.NewServer(mcpHTTPHandler(srv, nil))
	t.Cleanup(ts.Close)
	return ts.URL
}

func TestMCPHTTPHandler_Healthz(t *testing.T) {
	base := newHandlerTestServer(t)

	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("GET /healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz status = %d, want 200", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read /healthz body: %v", err)
	}
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("/healthz body = %q, want ok", body)
	}
}

// TestMCPHTTPHandler_ServesMCPOverStreamableHTTP exercises the full wire
// path a real host agent uses: a Streamable-HTTP MCP client connects to
// /mcp, completes the initialize handshake, and calls the ping tool. The
// go-sdk client sends no Origin header, so it passes both the net/http
// cross-origin guard and the go-sdk's localhost protection.
func TestMCPHTTPHandler_ServesMCPOverStreamableHTTP(t *testing.T) {
	base := newHandlerTestServer(t)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	c := mcp.NewClient(&mcp.Implementation{Name: "test", Version: "test"}, nil)
	session, err := c.Connect(ctx, &mcp.StreamableClientTransport{Endpoint: base + mcpEndpointPath}, nil)
	if err != nil {
		t.Fatalf("client.Connect over streamable http: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: "ping"})
	if err != nil {
		t.Fatalf("CallTool(ping): %v", err)
	}
	if res.IsError {
		t.Fatalf("CallTool(ping) IsError = true; content=%+v", res.Content)
	}
	if got := contentText(res); got != "pong" {
		t.Fatalf("ping content = %q, want pong", got)
	}
}

// TestServerInstructions_DefinesCaptureRecallLoop asserts the
// session-open instructions advertise the ambient loop contract: recall
// before answering, capture after the exchange, the no-forever / recall-
// does-not-refresh invariants, and the key-namespace convention. These
// are string-level guards so a future edit cannot silently drop the
// loop-shaped guidance the LLM relies on (#528).
func TestServerInstructions_DefinesCaptureRecallLoop(t *testing.T) {
	lower := strings.ToLower(serverInstructions)
	for _, want := range []string{
		"recall",   // step 1
		"answer",   // step 2
		"capture",  // step 3
		"proactiv", // proactive framing (matches proactive/proactively)
		"does not refresh",
		"there is no \"forever\"",
	} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("serverInstructions missing %q", want)
		}
	}
	// All three namespace prefixes must be named so the captured graph is
	// navigable as a mind map.
	for _, ns := range []string{"user.", "project.", "session."} {
		if !strings.Contains(serverInstructions, ns) {
			t.Errorf("serverInstructions missing namespace %q", ns)
		}
	}
	// Every memory tool should be referenced so the agent knows the full
	// toolset participates in the loop.
	for _, tool := range []string{"remember_fact", "recall_fact", "recall_related", "list_under", "remember_relation"} {
		if !strings.Contains(serverInstructions, tool) {
			t.Errorf("serverInstructions does not mention tool %q", tool)
		}
	}
}
