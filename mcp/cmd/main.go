// Command lantern-mcp exposes a remote Lantern instance as a Model
// Context Protocol server over Streamable HTTP.
//
// Configuration:
//
//	LANTERN_ADDR              http(s):// URL of the Lantern server.
//	                          Default: http://localhost:6380.
//	LANTERN_MCP_HTTP_ADDR     host:port the MCP endpoint listens on.
//	                          Default: 127.0.0.1:6390 (loopback only).
//	LANTERN_MCP_PING_TIMEOUT  bounds the startup health check.
//	                          Default: 5s.
//	LANTERN_MCP_TTL_<BUCKET>  per-bucket TTL override; see package
//	                          github.com/anaregdesign/lantern/mcp/internal/ttl
//	                          for the canonical bucket list and defaults.
//
// The binary writes all diagnostics to stderr. The MCP endpoint is served
// at http://<LANTERN_MCP_HTTP_ADDR>/mcp (Streamable HTTP, MCP spec
// 2025-06-18); a plain /healthz endpoint answers orchestrator probes.
// Exit code 1 indicates a fatal configuration or connection error.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	lmcp "github.com/anaregdesign/lantern/mcp"
)

func main() {
	if err := realMain(); err != nil {
		fmt.Fprintln(os.Stderr, "lantern-mcp:", err)
		os.Exit(1)
	}
}

func realMain() error {
	cfg, err := lmcp.DefaultConfig()
	if err != nil {
		return err
	}

	// Signal handling: SIGINT/SIGTERM cancels the context, which triggers
	// a graceful drain of the HTTP server inside Run. A failed startup
	// Ping or a listener bind error surfaces as a non-nil error from Run
	// with a non-zero exit. The TTL ladder is validated inside Run (before
	// dialing) so a misordered config is a fatal startup error.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Logger != nil {
		cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "lantern-mcp starting",
			slog.String("lantern_addr", cfg.LanternAddr),
			slog.String("http_addr", cfg.HTTPAddr),
			slog.String("version", lmcp.Version),
		)
	}

	return lmcp.Run(ctx, cfg)
}
