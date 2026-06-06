package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
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
	// "http://localhost:6380" matches the Lantern server's default
	// port and the SDK's plaintext-h2c scheme requirement.
	LanternAddr string
	// PingTimeout bounds the startup health check. The MCP server will
	// refuse to register tools if the Lantern endpoint is unreachable
	// within this window.
	PingTimeout time.Duration
	// Logger receives structured server-side logs. nil disables logging.
	// Writes always go to stderr — stdout is owned by the MCP transport.
	Logger *slog.Logger
	// Resolver holds the 12-bucket TTL configuration consumed by the
	// remember_* tools. If nil, Run loads it from environment via
	// ttl.LoadFromEnv() on startup.
	Resolver *ttl.Resolver
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
		addr = "http://localhost:6380"
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
// or the peer disconnects. The provided transport is typically
// &mcp.StdioTransport{} in production; integration tests use
// mcp.InMemoryTransport.
//
// Run returns the first non-nil error from any of: TTL resolver load,
// Lantern dial, startup Ping, server.Run.
func Run(ctx context.Context, cfg Config, transport mcp.Transport) error {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}

	resolver := cfg.Resolver
	if resolver == nil {
		r, err := ttl.LoadFromEnv()
		if err != nil {
			return fmt.Errorf("mcp: load ttl config: %w", err)
		}
		resolver = r
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

	srv := newServer(lantern, resolver, logger)
	return srv.Run(ctx, transport)
}

// NewServer constructs an MCP server bound to an already-dialled Lantern
// client. It registers the full tool set (ping + the six fact/relation
// tools) and returns the *mcp.Server so callers can drive it over any
// transport — production uses Run, tests can use mcp.NewInMemoryTransports.
//
// The TTL ladder is loaded from environment variables (LANTERN_MCP_TTL_*);
// override the defaults via env before calling. The caller retains
// ownership of lantern and must close it when the server stops.
func NewServer(lantern *client.Lantern, logger *slog.Logger) (*mcp.Server, error) {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	resolver, err := ttl.LoadFromEnv()
	if err != nil {
		return nil, fmt.Errorf("mcp: load ttl config: %w", err)
	}
	return newServer(lantern, resolver, logger), nil
}

// newServer constructs the MCP server and registers the full tool set
// (ping + the six fact/relation tools). Tests pass a fake lanternClient
// and an in-memory resolver to exercise handlers without dialing the
// Lantern server.
func newServer(lc lanternClient, resolver *ttl.Resolver, logger *slog.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "lantern-mcp",
		Title:   "Lantern decaying-memory MCP server",
		Version: Version,
	}, &mcp.ServerOptions{
		Logger:       logger,
		Instructions: serverInstructions,
	})

	// The ping tool exists so operators can sanity-check the wire to
	// Lantern without touching state. It also keeps the surface alive
	// even if the tool functions are temporarily disabled.
	mcp.AddTool(srv, &mcp.Tool{
		Name:        "ping",
		Description: "Verify the MCP server is alive and the upstream Lantern endpoint is reachable. Returns the literal string \"pong\" on success.",
	}, func(ctx context.Context, _ *mcp.CallToolRequest, _ struct{}) (*mcp.CallToolResult, any, error) {
		if err := lc.Ping(ctx); err != nil {
			return nil, nil, fmt.Errorf("lantern: %w", err)
		}
		return &mcp.CallToolResult{
			Content: []mcp.Content{&mcp.TextContent{Text: "pong"}},
		}, nil, nil
	})

	registerRememberFact(srv, lc, resolver)
	registerRecallFact(srv, lc)
	registerForget(srv, lc)
	registerListUnder(srv, lc)
	registerRememberRelation(srv, lc, resolver)
	registerRecallRelated(srv, lc)

	return srv
}

// serverInstructions is the system-prompt-style guidance the MCP server
// advertises to the LLM at session-open. The wording is deliberate:
//   - states the core mental model (decaying graph),
//   - calls out the single most-violated invariant (recall does NOT
//     refresh TTL), and
//   - gives a one-line tie-breaker for ambiguous bucket choices.
//
// Changing this text is an LLM-behavior-affecting change; treat it the
// way you would a prompt update and call it out in release notes.
const serverInstructions = "Lantern is a decaying graph memory. Vertices (facts) and edges (relations) carry TTLs and disappear when they expire. Two critical properties: (1) there is no \"forever\" — every write picks a bucket from {seconds, transient, turn, conversation, task, workday, day, week, sprint, month, quarter, durable}; (2) recall does NOT refresh TTL, so to keep a fact alive you must call remember_fact again. When in doubt about the right bucket, ask yourself \"when will this stop being true?\" and \"how bad is it if this lingers past then?\" — and pick the SHORTER bucket. Relations are additive: writing the same relation twice strengthens it."
