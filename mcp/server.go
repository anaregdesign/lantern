package mcp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Version is overridden at build time via -ldflags "-X .../mcp.Version=…".
// The default keeps go run ./mcp/cmd self-identifying during development.
var Version = "dev"

const (
	// defaultHTTPAddr is the listen address used when LANTERN_MCP_HTTP_ADDR
	// is unset. It binds loopback only: the MCP endpoint is an
	// unauthenticated local network surface, so the safe default never
	// exposes it beyond the host. Container/orchestrator deployments opt
	// into 0.0.0.0 explicitly (see mcp/Dockerfile and the Helm chart).
	defaultHTTPAddr = "127.0.0.1:6390"
	// mcpEndpointPath is the single HTTP path the Streamable-HTTP MCP
	// endpoint is served at. Clients POST/GET/DELETE here per the MCP
	// 2025-06-18 Streamable HTTP transport spec.
	mcpEndpointPath = "/mcp"
	// shutdownTimeout bounds graceful HTTP server drain on ctx cancel.
	shutdownTimeout = 5 * time.Second
)

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
	// HTTPAddr is the address the Streamable-HTTP MCP endpoint listens on
	// (host:port). Default "127.0.0.1:6390" binds loopback only; set
	// LANTERN_MCP_HTTP_ADDR=0.0.0.0:6390 to expose it on all interfaces
	// (container/Kubernetes deployments do this) — see the security notes
	// in mcp/README.md before doing so.
	HTTPAddr string
	// Logger receives structured server-side logs. nil disables logging.
	// Diagnostics always go to stderr.
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
	httpAddr := os.Getenv("LANTERN_MCP_HTTP_ADDR")
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
	}
	return Config{
		LanternAddr: addr,
		PingTimeout: timeout,
		HTTPAddr:    httpAddr,
		Logger:      slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
	}, nil
}

// Run wires up the MCP server, verifies the Lantern endpoint with a
// startup Ping, and serves the Model Context Protocol over Streamable
// HTTP on cfg.HTTPAddr until ctx is cancelled.
//
// The MCP endpoint is served at /mcp; a plain /healthz endpoint returns
// 200 for container/orchestrator probes. The MCP handler is wrapped with
// net/http cross-origin protection (a DNS-rebinding defence) because the
// endpoint is an unauthenticated local network surface — see the security
// notes in mcp/README.md before binding beyond loopback.
//
// Run returns the first non-nil error from any of: TTL resolver load,
// Lantern dial, startup Ping, or the HTTP server (the expected
// http.ErrServerClosed on graceful shutdown is treated as success).
func Run(ctx context.Context, cfg Config) error {
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

	httpAddr := cfg.HTTPAddr
	if httpAddr == "" {
		httpAddr = defaultHTTPAddr
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
	httpSrv := &http.Server{
		Addr:              httpAddr,
		Handler:           mcpHTTPHandler(srv, logger),
		ReadHeaderTimeout: 10 * time.Second,
	}

	serveErr := make(chan error, 1)
	go func() {
		logger.Info("mcp: serving streamable http",
			slog.String("addr", httpAddr),
			slog.String("endpoint", mcpEndpointPath),
		)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case <-ctx.Done():
		shutdownCtx, scancel := context.WithTimeout(context.Background(), shutdownTimeout)
		defer scancel()
		if err := httpSrv.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("mcp: http server shutdown: %w", err)
		}
		logger.Info("mcp: shut down cleanly")
		return nil
	case err := <-serveErr:
		if err != nil {
			return fmt.Errorf("mcp: http server: %w", err)
		}
		return nil
	}
}

// mcpHTTPHandler builds the http.Handler that serves the MCP server over
// Streamable HTTP at mcpEndpointPath, plus a plain /healthz probe.
//
// The MCP endpoint is wrapped with net/http cross-origin protection: it
// rejects unsafe cross-origin requests, a DNS-rebinding defence for the
// local network surface. The go-sdk handler additionally rejects loopback
// requests carrying a non-loopback Host header even when the listener is
// bound to 0.0.0.0, so both layers apply. /healthz is left unprotected so
// orchestrator probes (which send no Origin) succeed.
func mcpHTTPHandler(srv *mcp.Server, logger *slog.Logger) http.Handler {
	streamable := mcp.NewStreamableHTTPHandler(
		func(*http.Request) *mcp.Server { return srv },
		&mcp.StreamableHTTPOptions{Logger: logger},
	)
	protection := http.NewCrossOriginProtection()
	mux := http.NewServeMux()
	mux.Handle(mcpEndpointPath, protection.Handler(streamable))
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, "ok")
	})
	return mux
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
// advertises to the LLM at session-open. The wording is deliberate and
// does three jobs:
//   - states the core mental model (a decaying graph) and the single
//     most-violated invariant (recall does NOT refresh TTL);
//   - mandates an ambient capture+recall LOOP (recall before answering,
//     capture after a substantive exchange) so memory accrues without the
//     user having to ask for it; and
//   - establishes a key-namespace convention (user.* / project.* /
//     session.*) so the captured graph stays navigable as a mind map in
//     Admin Illuminate.
//
// Changing this text is an LLM-behavior-affecting change; treat it the
// way you would a prompt update and call it out in release notes.
const serverInstructions = "Lantern is your ambient, decaying graph memory. Use it proactively — you do not need to be asked. Run this loop every turn: " +
	"(1) RECALL FIRST: before answering anything that could depend on remembered context, call recall_fact (exact key), recall_related (walk from a seed), or list_under (enumerate a namespace) to pull in what you already know. " +
	"(2) ANSWER using what you recalled. " +
	"(3) CAPTURE AFTER: once the exchange settles, write any new durable facts with remember_fact and any new associations with remember_relation — preferences, decisions, identities, project facts, and how they connect. Capturing is the default, not the exception. " +
	"Two invariants govern every write: (a) there is no \"forever\" — each write picks a TTL bucket from {seconds, transient, turn, conversation, task, workday, day, week, sprint, month, quarter, durable}; (b) recall does NOT refresh TTL, so to keep a fact alive you must re-remember it when you reference it. Choosing a bucket: ask \"when will this stop being true?\" and \"how bad is it if this lingers past then?\" and pick the SHORTER one. Relations are additive — writing the same relation twice strengthens it, so reinforce associations you keep using. " +
	"Namespace keys so the graph reads as a mind map: user.* for stable facts about the person (user.preferences.tone, user.identity.role), project.* for the work at hand (project.lantern.stack), session.* for ephemeral working state (session.current-task). Prefer dotted, hierarchical keys; reuse a key to update its value."
