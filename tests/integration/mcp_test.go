package integration_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/anaregdesign/lantern/core/graphcache"
	lmcp "github.com/anaregdesign/lantern/mcp"
	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/anaregdesign/lantern/server/metrics"
	"github.com/anaregdesign/lantern/server/provider"
	"github.com/anaregdesign/lantern/server/service"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// TestMCP_TraversalFamilies_EndToEnd proves the default MCP surface reaches
// every typed Illuminate family over MCP -> Go SDK -> real Connect/h2c. The
// server metric is the independent witness that each request selected the
// family advertised in the MCP structured result (#1001).
func TestMCP_TraversalFamilies_EndToEnd(t *testing.T) {
	reg := prometheus.NewRegistry()
	m := metrics.New(reg, metrics.Options{SampleInterval: time.Hour})
	cache := graphcache.NewGraphCache[string, *pb.Vertex](time.Minute)
	svc := service.NewLanternService(cache).WithHotPathMetrics(m)
	val := provider.NewValidationInterceptor(defaultIntegrationValidationLimits())
	connectSrv := newConnectTestServer(t, svc, nil, val.ConnectInterceptor())
	lan := newConnectClientFor(t, connectSrv.url)
	cs, ctx := newMCPClientSession(t, lan)

	for _, key := range []string{"a", "b", "c", "d"} {
		if _, err := lan.PutVertex(ctx, key, key, time.Hour); err != nil {
			t.Fatalf("PutVertex(%s): %v", key, err)
		}
	}
	for _, edge := range []struct {
		tail, head string
		weight     float32
	}{{"a", "b", 4}, {"a", "c", 2}, {"b", "d", 3}, {"c", "d", 1}} {
		if _, err := lan.AddEdge(ctx, edge.tail, edge.head, edge.weight, time.Hour); err != nil {
			t.Fatalf("AddEdge(%s,%s): %v", edge.tail, edge.head, err)
		}
	}

	for _, tc := range []struct {
		family string
		args   map[string]any
	}{
		{family: "bfs", args: map[string]any{"key": "a"}},
		{family: "ppr", args: map[string]any{"key": "a", "ppr": map[string]any{"top_n": 4}}},
		{family: "community", args: map[string]any{"key": "a", "community": map[string]any{"max_size": 4}}},
	} {
		t.Run(tc.family, func(t *testing.T) {
			res := callMCP(t, ctx, cs, "whats_happening", tc.args)
			var out struct {
				Family string `json:"family"`
			}
			decodeMCP(t, res, &out)
			if out.Family != tc.family {
				t.Fatalf("structured family = %q, want %q", out.Family, tc.family)
			}
		})
	}

	scrape := httptest.NewServer(promhttp.HandlerFor(reg, promhttp.HandlerOpts{}))
	t.Cleanup(scrape.Close)
	body := scrapeMetrics(t, scrape.URL)
	for _, family := range []string{"bfs", "ppr", "community"} {
		got, ok := metricSeriesValue(t, body, "lantern_illuminate_calls_total", map[string]string{
			"algorithm": family, "reduction": "none", "objective": "maximize",
			"weighting": "raw", "phase": "complete", "code": "ok",
		})
		if !ok || got != 1 {
			t.Errorf("successful %s MCP traversal metric = %v (present=%v), want 1", family, got, ok)
		}
	}
}

// TestMCP_ContextEndToEnd walks the context-only release surface over a real
// Lantern service: announce, activity, lease, note, situational read, release.
func TestMCP_ContextEndToEnd(t *testing.T) {
	t.Setenv("LANTERN_MCP_AGENT_ID", "")
	lan, cleanup := newInProcessClientWithPrefix(t)
	defer cleanup()
	cs, ctx := newMCPClientSession(t, lan)

	callMCP(t, ctx, cs, "announce", map[string]any{"task": "refactoring auth middleware"})
	callMCP(t, ctx, cs, "track", map[string]any{"resources": []string{"repo.api.middleware.auth", "ticket.API-17"}})
	callMCP(t, ctx, cs, "claim", map[string]any{"resource": "repo.api.middleware.auth", "note": "rewriting"})
	callMCP(t, ctx, cs, "post_note", map[string]any{
		"text": "auth middleware API changed", "severity": "warn",
		"links": []string{"repo.api.middleware.auth"}, "ttl": "turn",
	})

	if got := mcpText(callMCP(t, ctx, cs, "list_agents", map[string]any{})); !strings.Contains(got, "refactoring auth middleware") {
		t.Fatalf("list_agents missing announced task: %q", got)
	}
	if got := mcpText(callMCP(t, ctx, cs, "list_claims", map[string]any{})); !strings.Contains(got, "repo.api.middleware.auth") {
		t.Fatalf("list_claims missing lease: %q", got)
	}
	happening := mcpText(callMCP(t, ctx, cs, "whats_happening", map[string]any{"key": "repo.api.middleware.auth"}))
	for _, want := range []string{"refactoring auth middleware", "auth middleware API changed"} {
		if !strings.Contains(happening, want) {
			t.Fatalf("whats_happening missing %q:\n%s", want, happening)
		}
	}

	callMCP(t, ctx, cs, "release", map[string]any{"resource": "repo.api.middleware.auth"})
	if got := mcpText(callMCP(t, ctx, cs, "list_claims", map[string]any{})); strings.Contains(got, "repo.api.middleware.auth") {
		t.Fatalf("lease survived release: %q", got)
	}
}

func newMCPClientSession(t *testing.T, lan *client.Lantern) (*mcp.ClientSession, context.Context) {
	t.Helper()
	srv, err := lmcp.NewServer(lan, slog.New(slog.NewJSONHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("lmcp.NewServer: %v", err)
	}
	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	serverSession, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		cancel()
		t.Fatalf("server.Connect: %v", err)
	}
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "integration", Version: "test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		_ = serverSession.Close()
		cancel()
		t.Fatalf("client.Connect: %v", err)
	}
	t.Cleanup(func() {
		_ = cs.Close()
		_ = serverSession.Close()
		cancel()
	})
	return cs, ctx
}

func callMCP(t *testing.T, ctx context.Context, cs *mcp.ClientSession, name string, args map[string]any) *mcp.CallToolResult {
	t.Helper()
	res, err := cs.CallTool(ctx, &mcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		t.Fatalf("CallTool(%s): %v", name, err)
	}
	if res.IsError {
		t.Fatalf("CallTool(%s) returned tool error: %+v", name, res.Content)
	}
	return res
}

func decodeMCP(t *testing.T, res *mcp.CallToolResult, dst any) {
	t.Helper()
	b, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	if err := json.Unmarshal(b, dst); err != nil {
		t.Fatalf("unmarshal structured content: %v", err)
	}
}

func mcpText(res *mcp.CallToolResult) string {
	for _, content := range res.Content {
		if text, ok := content.(*mcp.TextContent); ok {
			return text.Text
		}
	}
	return ""
}
