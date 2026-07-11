package mcp

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"connectrpc.com/grpchealth"
	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
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

func TestRun_RejectsMissingLanternAddress(t *testing.T) {
	err := Run(context.Background(), Config{
		LanternAddr: ", ,",
		Resolver:    mustDefaultResolver(t),
	})
	if err == nil || !strings.Contains(err.Error(), "no lantern address configured") {
		t.Fatalf("Run error = %v, want missing-address error", err)
	}
}

func TestRun_RejectsInvalidTTLConfigBeforeDial(t *testing.T) {
	t.Setenv(ttl.MaxTTLEnvVar, "not-a-duration")
	err := Run(context.Background(), Config{LanternAddr: "http://127.0.0.1:1"})
	if err == nil || !strings.Contains(err.Error(), "load ttl config") {
		t.Fatalf("Run error = %v, want TTL configuration error", err)
	}
}

func TestRun_ServesUntilContextCancellation(t *testing.T) {
	checker := grpchealth.NewStaticChecker("")
	checker.SetStatus("", grpchealth.StatusServing)
	_, health := grpchealth.NewHandler(checker)
	upstream := httptest.NewUnstartedServer(health)
	protocols := new(http.Protocols)
	protocols.SetHTTP1(true)
	protocols.SetUnencryptedHTTP2(true)
	upstream.Config.Protocols = protocols
	upstream.Start()
	t.Cleanup(upstream.Close)
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve MCP listener address: %v", err)
	}
	httpAddr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release MCP listener address: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	resolver := mustDefaultResolver(t)
	go func() {
		done <- Run(ctx, Config{
			LanternAddr: upstream.URL,
			PingTimeout: time.Second,
			HTTPAddr:    httpAddr,
			Resolver:    resolver,
		})
	}()

	deadline := time.Now().Add(5 * time.Second)
	for {
		resp, requestErr := http.Get("http://" + httpAddr + "/healthz")
		if requestErr == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				break
			}
		}
		select {
		case runErr := <-done:
			cancel()
			t.Fatalf("Run exited before serving healthz: %v", runErr)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			t.Fatal("Run did not start its healthz listener")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run after cancellation: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not shut down after context cancellation")
	}
}

func TestNewServer_LoadsTTLAndRegistersContextSurface(t *testing.T) {
	upstream := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(upstream.Close)
	lantern, err := client.NewLantern(upstream.URL)
	if err != nil {
		t.Fatalf("NewLantern: %v", err)
	}
	t.Cleanup(func() { _ = lantern.Close() })

	srv, err := NewServer(lantern, nil)
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	if srv == nil {
		t.Fatal("NewServer returned nil server")
	}
}

func TestNewServer_RejectsInvalidTTLConfig(t *testing.T) {
	t.Setenv(ttl.MaxTTLEnvVar, "not-a-duration")
	_, err := NewServer(nil, nil)
	if err == nil || !strings.Contains(err.Error(), "load ttl config") {
		t.Fatalf("NewServer error = %v, want TTL configuration error", err)
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

// TestContextInstructions_DefinesCoordinationLoop guards the session-open
// protocol that makes independent agents converge on the same live board.
func TestContextInstructions_DefinesCoordinationLoop(t *testing.T) {
	lower := strings.ToLower(contextInstructions)
	for _, want := range []string{
		"announce",
		"track",
		"claim",
		"post_note",
		"whats_happening",
		"not long-term memory",
	} {
		if !strings.Contains(lower, strings.ToLower(want)) {
			t.Errorf("contextInstructions missing %q", want)
		}
	}
}

// TestToolSurface pins the post-cutover contract: only shared-context verbs
// are advertised; the retired ambient-memory compatibility surface cannot
// silently reappear.
func TestToolSurface(t *testing.T) {
	// A stale deployment variable must not resurrect the removed surface.
	t.Setenv("LANTERN_MCP_PROFILE", "memory")
	want := []string{
		"ping", "announce", "list_agents", "track", "whats_happening",
		"claim", "release", "list_claims", "post_note", "context_stats",
	}
	fake := &fakeLantern{}
	r := mustDefaultResolver(t)
	srv := newServer(fake, r, slog.New(slog.NewJSONHandler(io.Discard, nil)))

	serverT, clientT := mcp.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ss, err := srv.Connect(ctx, serverT, nil)
	if err != nil {
		t.Fatalf("server connect: %v", err)
	}
	defer func() { _ = ss.Close() }()
	cs, err := mcp.NewClient(&mcp.Implementation{Name: "surface-test"}, nil).Connect(ctx, clientT, nil)
	if err != nil {
		t.Fatalf("client connect: %v", err)
	}
	defer func() { _ = cs.Close() }()

	listed, err := cs.ListTools(ctx, &mcp.ListToolsParams{})
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}
	got := map[string]bool{}
	for _, tool := range listed.Tools {
		got[tool.Name] = true
	}
	if len(got) != len(want) {
		t.Fatalf("registered %d tools, want %d: %v", len(got), len(want), got)
	}
	for _, name := range want {
		if !got[name] {
			t.Fatalf("missing tool %q (got %v)", name, got)
		}
	}
	for _, retired := range []string{"remember_fact", "recall_fact", "recall_related", "memory_stats"} {
		if got[retired] {
			t.Fatalf("retired tool %q was registered", retired)
		}
	}
}
