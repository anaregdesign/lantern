package config

import (
	"time"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// ShutdownConfig is the graceful-shutdown timing.
//
//   - LANTERN_SHUTDOWN_TIMEOUT_SECONDS   (default 30) — how long
//     http.Server.Shutdown may take to drain in-flight requests before a
//     hard Close.
//   - LANTERN_DRAIN_DELAY_SECONDS         (default 0 = disabled) — the
//     zero-drop rolling-update drain window (#768). On SIGTERM the server
//     flips readiness to NOT_SERVING (overall "" gRPC health + /readyz)
//     immediately, then keeps the listeners serving for this long so load
//     balancers and kube-proxy observe the drain and stop routing new
//     requests before the listener actually stops accepting. Opt-in
//     because it lengthens shutdown; set it slightly above the platform's
//     endpoint-propagation lag (typically 1-5s).
type ShutdownConfig struct {
	Timeout    time.Duration
	DrainDelay time.Duration
}

func loadShutdown() ShutdownConfig {
	return ShutdownConfig{
		Timeout:    time.Duration(envconfig.Int("LANTERN_SHUTDOWN_TIMEOUT_SECONDS", 30)) * time.Second,
		DrainDelay: time.Duration(envconfig.Int("LANTERN_DRAIN_DELAY_SECONDS", 0)) * time.Second,
	}
}
