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
	// LanternAddr is the target — or comma-separated list of targets —
	// the Lantern client dials. A single value dials one endpoint; a
	// comma-separated list (e.g. "http://a:6380,http://b:6380") enables
	// multi-node failover across HA replicas (see #544 and the failover
	// notes in mcp/README.md). Default "http://localhost:6380" matches the
	// Lantern server's default port and the SDK's plaintext-h2c scheme
	// requirement.
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
	// Resolver holds the TTL configuration consumed by the working-context
	// tools. If nil, Run loads it from environment via ttl.LoadFromEnv().
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

	addrs := parseLanternAddrs(cfg.LanternAddr)
	if len(addrs) == 0 {
		return fmt.Errorf("mcp: no lantern address configured (set LANTERN_ADDR)")
	}
	logger.Info("mcp: dialing lantern",
		slog.Any("addrs", addrs),
		slog.Int("nodes", len(addrs)),
		slog.String("version", Version),
	)
	// LANTERN_TOKEN authenticates against servers running with
	// LANTERN_AUTH_TOKENS (#850); unset keeps the open-cluster behaviour.
	var cliOpts []client.Option
	if tok := os.Getenv("LANTERN_TOKEN"); tok != "" {
		cliOpts = append(cliOpts, client.WithAuthToken(tok))
	}
	lantern, err := client.NewLanternFailover(addrs, cliOpts...)
	if err != nil {
		return fmt.Errorf("mcp: dial lantern: %w", err)
	}
	defer func() {
		if cerr := lantern.Close(); cerr != nil {
			logger.Warn("mcp: lantern close failed", slog.Any("err", cerr))
		}
	}()

	pingCtx, cancel := context.WithTimeout(ctx, cfg.PingTimeout)
	defer cancel()
	if err := lantern.Ping(pingCtx); err != nil {
		return fmt.Errorf("mcp: lantern health check failed (tried %d node(s)): %w", len(addrs), err)
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
// client. It registers the shared working-context tool surface and returns
// the *mcp.Server so callers can drive it over any transport — production
// uses Run, tests can use mcp.NewInMemoryTransports.
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

// newServer constructs the MCP server and registers the shared
// working-context tools. Tests pass a fake lanternClient and an in-memory
// resolver to exercise handlers without dialing the Lantern server.
func newServer(lc lanternClient, resolver *ttl.Resolver, logger *slog.Logger) *mcp.Server {
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "lantern-mcp",
		Title:   "Lantern shared working-context MCP server",
		Version: Version,
	}, &mcp.ServerOptions{
		Logger:       logger,
		Instructions: contextInstructions,
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

	registerAnnounce(srv, lc, resolver)
	registerListAgents(srv, lc)
	registerTrack(srv, lc, resolver)
	registerWhatsHappening(srv, lc)
	registerClaim(srv, lc, resolver)
	registerRelease(srv, lc)
	registerListClaims(srv, lc)
	registerPostNote(srv, lc, resolver)
	registerContextStats(srv, lc)

	return srv
}

// contextInstructions is the session-open guidance for the working-context
// server. This text shapes LLM behaviour — treat changes like prompt
// updates and call them out in release notes.
const contextInstructions = "Lantern is the fleet's shared working context — a live board of who is doing what RIGHT NOW, built on decaying state: presence, claims, notes, and activity all expire on their own, so everything you read is current by construction. It is NOT long-term memory; never store durable knowledge here. " +
	"Run this loop: (1) ON SESSION START: announce yourself (your task line), then context_stats or whats_happening on the resources you are about to touch to see who else is around. (2) WHILE WORKING: track the resources you touch as you touch them (repetition strengthens the signal); re-announce when your task changes and periodically as a heartbeat (~every minute — presence lives ~2m); claim a resource before making changes siblings could collide with, re-claim to renew (~10m lease), release when done; a claim conflict is a structured {granted:false, holder} result — coordinate, wait for expiry, or force only after coordinating. (3) SIGNAL: post_note for anything the whole fleet should know (build broken, API flaky, migration in progress), linked to the resource keys it concerns, with the SHORTEST plausible TTL bucket. (4) BEFORE TOUCHING anything contested: whats_happening on the key — who is on it, live claims, live notes; an empty context means the coast is clear. " +
	"Key conventions: resources are dotted keys shared by convention across the fleet (repo.<name>.<path>, ticket.<id>, dataset.<name>) — use the SAME keys your siblings use or the coordination protects nothing. agents.*, claims.*, and notes.* are reserved for the tools. " +
	"Everything here is expendable by design: a crashed agent's presence and claims evaporate on their own; silence is self-cleaning. If you need durable memory, use a different store."
