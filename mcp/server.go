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
	// Resolver holds the 12-bucket TTL configuration consumed by the
	// remember_* tools. If nil, Run loads it from environment via
	// ttl.LoadFromEnv() on startup.
	Resolver *ttl.Resolver
	// Profile selects the registered tool surface (#851):
	// "context" (default) — the multi-agent shared working context
	// (announce / track / claim / whats_happening / ...);
	// "memory" — the legacy decaying-memory verbs (remember_* / recall_*),
	// kept for one mcp/vX release before retirement.
	// Both profiles share one keyspace on the server; switching does NOT
	// migrate or clean the other profile's data — TTL decay handles it.
	Profile string
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
	profile := os.Getenv("LANTERN_MCP_PROFILE")
	if profile == "" {
		profile = ProfileContext
	}
	if profile != ProfileContext && profile != ProfileMemory {
		return Config{}, fmt.Errorf("LANTERN_MCP_PROFILE=%q: must be %q or %q", profile, ProfileContext, ProfileMemory)
	}
	return Config{
		LanternAddr: addr,
		PingTimeout: timeout,
		HTTPAddr:    httpAddr,
		Logger:      slog.New(slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})),
		Profile:     profile,
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

	profile := cfg.Profile
	if profile == "" {
		profile = ProfileContext
	}
	srv := newServer(lantern, resolver, logger, profile)
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
// client. It registers the tool surface selected by LANTERN_MCP_PROFILE
// (context — the default working-context tools — or the legacy memory
// verbs, #851) and returns the *mcp.Server so callers can drive it over any
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
	profile := os.Getenv("LANTERN_MCP_PROFILE")
	if profile == "" {
		profile = ProfileContext
	}
	return newServer(lantern, resolver, logger, profile), nil
}

// Profile names for Config.Profile / LANTERN_MCP_PROFILE (#851).
const (
	// ProfileContext is the default: the multi-agent shared working
	// context (presence / claims / activity / blackboard).
	ProfileContext = "context"
	// ProfileMemory is the legacy decaying-memory surface, available for
	// one mcp/vX release for existing deployments, then retired.
	ProfileMemory = "memory"
)

// newServer constructs the MCP server and registers the tool set the
// selected profile advertises (#851). Tests pass a fake lanternClient and
// an in-memory resolver to exercise handlers without dialing the Lantern
// server.
func newServer(lc lanternClient, resolver *ttl.Resolver, logger *slog.Logger, profile string) *mcp.Server {
	title := "Lantern shared working-context MCP server"
	instructions := contextInstructions
	if profile == ProfileMemory {
		title = "Lantern decaying-memory MCP server"
		instructions = serverInstructions
	}
	srv := mcp.NewServer(&mcp.Implementation{
		Name:    "lantern-mcp",
		Title:   title,
		Version: Version,
	}, &mcp.ServerOptions{
		Logger:       logger,
		Instructions: instructions,
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

	if profile == ProfileMemory {
		registerRememberFact(srv, lc, resolver)
		registerRememberFacts(srv, lc, resolver)
		registerRecallFact(srv, lc)
		registerSearchFacts(srv, lc)
		registerTouch(srv, lc, resolver)
		registerForget(srv, lc)
		registerForgetUnder(srv, lc)
		registerListUnder(srv, lc)
		registerListNamespaces(srv, lc)
		registerMemoryStats(srv, lc, resolver)
		registerRememberRelation(srv, lc, resolver)
		registerRememberRelations(srv, lc, resolver)
		registerRecallRelated(srv, lc, resolver)
		registerRecallRelation(srv, lc)
		return srv
	}

	// Context profile (#851): the multi-agent shared working context.
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
// profile (#851). Same care as serverInstructions: this text shapes LLM
// behaviour — treat changes like prompt updates and call them out in
// release notes.
const contextInstructions = "Lantern is the fleet's shared working context — a live board of who is doing what RIGHT NOW, built on decaying state: presence, claims, notes, and activity all expire on their own, so everything you read is current by construction. It is NOT long-term memory; never store durable knowledge here. " +
	"Run this loop: (1) ON SESSION START: announce yourself (your task line), then context_stats or whats_happening on the resources you are about to touch to see who else is around. (2) WHILE WORKING: track the resources you touch as you touch them (repetition strengthens the signal); re-announce when your task changes and periodically as a heartbeat (~every minute — presence lives ~2m); claim a resource before making changes siblings could collide with, re-claim to renew (~10m lease), release when done; a claim conflict is a structured {granted:false, holder} result — coordinate, wait for expiry, or force only after coordinating. (3) SIGNAL: post_note for anything the whole fleet should know (build broken, API flaky, migration in progress), linked to the resource keys it concerns, with the SHORTEST plausible TTL bucket. (4) BEFORE TOUCHING anything contested: whats_happening on the key — who is on it, live claims, live notes; an empty context means the coast is clear. " +
	"Key conventions: resources are dotted keys shared by convention across the fleet (repo.<name>.<path>, ticket.<id>, dataset.<name>) — use the SAME keys your siblings use or the coordination protects nothing. agents.*, claims.*, and notes.* are reserved for the tools. " +
	"Everything here is expendable by design: a crashed agent's presence and claims evaporate on their own; silence is self-cleaning. If you need durable memory, use a different store."

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
	"(1) RECALL FIRST: before answering anything that could depend on remembered context, call recall_fact (exact key), search_facts (substring over keys+values when you recall the topic but not the exact key), recall_related (walk from a seed), recall_relation (read one specific from→to edge's weight and decay), list_under (enumerate a namespace), or list_namespaces (discover which namespaces exist, counts only, no values) to pull in what you already know. " +
	"To gauge how much you remember and how soon it decays — without reading any values — call memory_stats (fact/relation counts, per-scope breakdown, and a remaining-life histogram; pass a prefix to scope it to one namespace). " +
	"(2) ANSWER using what you recalled. " +
	"(3) CAPTURE AFTER: once the exchange settles, write any new durable facts with remember_fact and any new associations with remember_relation (or remember_facts / remember_relations to record several in one call) — preferences, decisions, identities, project facts, and how they connect. Capturing is the default, not the exception. " +
	"Two invariants govern every write: (a) there is no \"forever\" — each write picks a TTL bucket from {seconds, transient, turn, conversation, task, workday, day, week, sprint, month, quarter, durable}; (b) recall does NOT refresh TTL, so to keep a fact alive you must re-remember it — or touch it to extend its TTL without resupplying the value — when you reference it. The edge-side keep-alive is recall_related with reinforce=true, which adds a decaying weight pulse to the edges it walks (the one opt-in exception to this no-refresh rule, and it never shortens an edge's existing life). Choosing a bucket: ask \"when will this stop being true?\" and \"how bad is it if this lingers past then?\" and pick the SHORTER one. Relations are additive — writing the same relation twice strengthens it, so reinforce associations you keep using. " +
	"Namespace keys so the graph reads as a mind map: user.* for stable facts about the person (user.preferences.tone, user.identity.role), project.* for the work at hand (project.lantern.stack), session.* for ephemeral working state (session.current-task). Prefer dotted, hierarchical keys; reuse a key to update its value."
