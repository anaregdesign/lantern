// Command lantern-mcp exposes a remote Lantern instance as a Model
// Context Protocol server speaking stdio.
//
// Configuration:
//
//	LANTERN_ADDR              http(s):// URL of the Lantern server.
//	                          Default: http://localhost:6380.
//	LANTERN_MCP_PING_TIMEOUT  bounds the startup health check.
//	                          Default: 5s.
//	LANTERN_MCP_TTL_<BUCKET>  per-bucket TTL override; see package
//	                          github.com/anaregdesign/lantern/mcp/internal/ttl
//	                          for the canonical bucket list and defaults.
//
// The binary writes all diagnostics to stderr — stdout is reserved for the
// MCP JSON-RPC stream. Exit code 1 indicates a fatal configuration or
// connection error during startup.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	lmcp "github.com/anaregdesign/lantern/mcp"
	"github.com/anaregdesign/lantern/mcp/internal/ttl"
	"github.com/modelcontextprotocol/go-sdk/mcp"
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

	// Validate the TTL configuration up front. We do not pass the resolver
	// into Run — the scaffold has no tools that consume it — but a
	// misordered config must be a fatal startup error per #283 so that the
	// behaviour does not surprise operators once #285 lands.
	if _, err := ttl.LoadFromEnv(); err != nil {
		return err
	}

	// Signal handling: SIGINT/SIGTERM cancels the context, which both
	// closes the MCP session and ends Run. The MCP server itself ends Run
	// when its peer disconnects (Ctrl-D on stdin); either path is a clean
	// shutdown.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if cfg.Logger != nil {
		cfg.Logger.LogAttrs(ctx, slog.LevelInfo, "lantern-mcp starting",
			slog.String("lantern_addr", cfg.LanternAddr),
			slog.String("version", lmcp.Version),
		)
	}

	return lmcp.Run(ctx, cfg, &mcp.StdioTransport{})
}
