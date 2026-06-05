package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is overridden at build time via -ldflags "-X .../mcp.Version=…".
// The default keeps go run ./mcp/cmd self-identifying during development.
var Version = "dev"

// Config gathers the runtime-mutable knobs for the MCP server. Everything
// here is read from environment variables in main; tests construct Config
// directly to skip env wiring.
type Config struct {
	// LanternAddr is the target passed to client.NewLantern. Default
	// "localhost:6380" matches the Lantern server's default port.
	LanternAddr string
	// PingTimeout bounds the startup health check. The MCP server will
	// refuse to register tools if the Lantern endpoint is unreachable
	// within this window.
	PingTimeout time.Duration
	// Logger receives structured server-side logs. nil disables logging.
	// Writes always go to stderr — stdout is owned by the MCP transport.
	Logger *slog.Logger
}

// DefaultConfig reads the standard env vars and returns a Config suitable
// for production. The function intentionally does not return an error for
// missing vars — every knob has a documented default — but does return an
// error if a present value is malformed (e.g. LANTERN_MCP_PING_TIMEOUT is
// not a valid duration), because silently falling back to the default
// would mask operator typos.
func DefaultConfig() (Config, error) {
	addr := os.Getenv("LANTERN_ADDR")
	if addr == "" {
		addr = "localhost:6380"
	}
	timeout := 5 * time.Second
	if raw := os.Getenv("LANTERN_MCP_PING_TIMEOUT"); raw != "" {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return Config{}, fmt.Errorf("LANTERN_MCP_PING_TIMEOUT=%q: %w", raw, err)
		}
		if parsed <= 0 {
			return Config{}, fmt.Errorf("LANTERN_MCP_PING_TIMEOUT=%q: must be positive", raw)
		}
		timeout = parsed
	}
	return Config{
		LanternAddr: addr,
		PingTimeout: timeout,
		Logger:      slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}, nil
}

// Run wires up the MCP server, verifies the Lantern endpoint with a
// startup Ping, and blocks on the given transport until ctx is cancelled
// or the peer disconnects. The provided io.Writer is reserved for the
// stdout half of the MCP transport — callers building a StdioTransport
// should pass os.Stdout, but tests typically use an InMemoryTransport and
// can leave it nil.
//
// Run returns the first non-nil error from any of: Lantern dial, startup
// Ping, server.Run.
func Run(ctx context.Context, cfg Config, transport mcp.Transport) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	logger.Info("mcp: dialing lantern",
		slog.String("addr", cfg.LanternAddr),
		slog.String("version", Version),
	)
	lantern, err := client.NewLantern(cfg.LanternAddr)
	if err != nil {
		return fmt.Errorf("mcp: dial %s: %w", cfg.LanternAddr, err)
	}
	defer func() {
		if cerr := lantern.Close(); cerr != nil {
			logger.Warn("mcp: lantern close failed", slog.Any("err", cerr))
		}
	}()

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()
	if err := lantern.Ping(pingCtx); err != nil {
		return fmt.Errorf("mcp: lantern health check at %s failed: %w", cfg.LanternAddr, err)
	}
	logger.Info("mcp: lantern reachable, registering tools")

	srv := newServer(lantern, logger)
	return srv.Run(ctx, transport)
}

// newServer constructs the MCP server and registers the tool set. The
// no-op ping tool is the only tool in this scaffold; sub-issue #285 adds
// the real surface.
//
// Wired separately from Run so the InMemoryTransport-driven tests can
// build the server without having to dial a real Lantern endpoint.
func newServer(lantern *client.Lantern, logger *slog.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "lantern-mcp",
		Title:   "Lantern decaying-memory MCP server",
		Version: Version,
	}, &mcp.ServerOptions{
		Logger:       logger,
		Instructions: "Lantern is a decaying graph memory. Vertices and edges carry TTLs and disappear when they expire. When in doubt, choose the SHORTER TTL bucket; recall does NOT refresh TTL.",
	})

	// The ping tool exists so that #284 can be merged without #285 — it
	// proves the round-trip through MCP works and (because it hits
	// Lantern.Ping) that the wire to Lantern is live.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ping",
		Description: "Verify the MCP server is alive and the upstream Lantern endpoint is reachable. Returns the literal string \"pong\" on success.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := lantern.Ping(ctx); err != nil {
			return nil, nil, fmt.Errorf("lantern: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
		}, nil, nil
	})

	return srv
}
