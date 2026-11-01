package config

import (
	"fmt"
	"log/slog"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// foreignEnvPrefixes names LANTERN_*-prefixed namespaces owned by sibling
// processes that legitimately share an env file with the server, so the
// unknown-variable sweep does not flag them (#847). The MCP server reads
// LANTERN_MCP_* in its own process; LANTERN_TOKEN is the CLIENT-side auth
// token consumed by lantern-cli and the MCP server (#850) — a compose file
// commonly sets it alongside the server's LANTERN_AUTH_TOKENS.
var foreignEnvPrefixes = []string{"LANTERN_MCP_", "LANTERN_TOKEN"}

// CheckEnvironment is the boot-time config validation pass (#847). It runs after
// every loader has registered its variables, so the envconfig registry is
// the complete env contract at this point. It reports through the bootstrap
// logger supplied by the composition root and fails only under LANTERN_STRICT_CONFIG.
func CheckEnvironment(environ []string, log *slog.Logger) error {
	strict := envconfig.Bool("LANTERN_STRICT_CONFIG", false)

	findings := envconfig.Findings()
	for _, f := range findings {
		log.Warn("config: malformed value, using default",
			slog.String("key", f.Key),
			slog.String("value", f.Raw),
			slog.String("reason", f.Reason))
	}

	unknown := envconfig.UnknownLanternVars(environ, foreignEnvPrefixes...)
	for _, u := range unknown {
		if u.Suggestion != "" {
			log.Warn("config: unknown LANTERN_* variable (typo?)",
				slog.String("key", u.Key),
				slog.String("did_you_mean", u.Suggestion))
		} else {
			log.Warn("config: unknown LANTERN_* variable",
				slog.String("key", u.Key))
		}
	}

	// One summary line naming which knobs are set (keys only — values are
	// deliberately withheld so a future secret-bearing variable can never
	// leak into the boot log).
	if set := envconfig.SetKeys(); len(set) > 0 {
		log.Info("config: environment overrides active", slog.Any("keys", set))
	}

	if strict && (len(findings) > 0 || len(unknown) > 0) {
		var parts []string
		for _, f := range findings {
			parts = append(parts, fmt.Sprintf("malformed %s=%q (%s)", f.Key, f.Raw, f.Reason))
		}
		for _, u := range unknown {
			if u.Suggestion != "" {
				parts = append(parts, fmt.Sprintf("unknown %s (did you mean %s?)", u.Key, u.Suggestion))
			} else {
				parts = append(parts, fmt.Sprintf("unknown %s", u.Key))
			}
		}
		return fmt.Errorf("LANTERN_STRICT_CONFIG: refusing to boot: %s", strings.Join(parts, "; "))
	}
	return nil
}
