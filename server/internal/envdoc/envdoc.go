// Package envdoc renders the generated operator-facing reference of every
// LANTERN_* environment variable the server reads (docs/env.md). The variable
// set itself comes from the envconfig registry — populated as a side effect of
// provider.NewConfig() — so the reference cannot silently drift from the code:
// a variable added without a description here, or a description left behind
// after a variable is removed, fails Render (and with it `go generate` and the
// generated-code CI check). See #847.
package envdoc

import (
	"fmt"
	"sort"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// descriptions is the curated one-line operator description per variable.
// Render enforces that this table and the envconfig registry agree exactly.
var descriptions = map[string]string{
	"LANTERN_PORT":                   "TCP port of the primary Connect/h2c listener.",
	"LANTERN_MAX_RECV_MSG_BYTES":     "Maximum accepted request message size in bytes.",
	"LANTERN_MAX_SEND_MSG_BYTES":     "Maximum produced response message size in bytes.",
	"LANTERN_MAX_CONCURRENT_STREAMS": "HTTP/2 max concurrent streams per connection (0 = unlimited).",

	"LANTERN_TLS_CERT_FILE":      "PEM certificate path; setting cert + key enables TLS on the primary listener.",
	"LANTERN_TLS_KEY_FILE":       "PEM private-key path; pairs with LANTERN_TLS_CERT_FILE.",
	"LANTERN_TLS_CLIENT_CA_FILE": "PEM client-CA bundle path; setting it additionally enables mTLS client verification.",

	"LANTERN_RATE_LIMIT_RPS":   "Process-wide token-bucket refill rate in requests/second (0 disables rate limiting).",
	"LANTERN_RATE_LIMIT_BURST": "Token-bucket burst size; when unset or <= 0 it resolves to 2x LANTERN_RATE_LIMIT_RPS.",

	"LANTERN_LOG_LEVEL":              "Structured-log level: debug, info, warn, or error.",
	"LANTERN_LOG_FORMAT":             "Log output format: json or text.",
	"LANTERN_METRICS_ADDR":           "host:port for the /metrics + /healthz + /readyz HTTP listener (empty disables it).",
	"LANTERN_REFLECTION":             "Serve gRPC server reflection on the primary listener.",
	"LANTERN_VERSION":                "Overrides the version label reported in lantern_build_info and GetServerStatus.",
	"LANTERN_COMMIT":                 "Overrides the commit label reported in lantern_build_info.",
	"LANTERN_SLOW_RPC_THRESHOLD_MS":  "RPCs slower than this emit a warn-level \"slow rpc\" log line (0 disables).",
	"LANTERN_PPROF_ENABLED":          "Mount /debug/pprof/* on the metrics listener (keep the listener internal-only).",
	"LANTERN_MUTEX_PROFILE_FRACTION": "runtime.SetMutexProfileFraction sampling rate (0 = disabled; has runtime cost).",
	"LANTERN_BLOCK_PROFILE_RATE":     "runtime.SetBlockProfileRate in nanoseconds between samples (0 = disabled).",

	"LANTERN_DEFAULT_TTL_SECONDS": "Reported in GetServerStatus and startup logs only; RPC writes without an expiration are permanent (decay is opt-in per write, #523).",
	"LANTERN_GC_INTERVAL_SECONDS": "Graph-cache GC tick interval in seconds (expired vertex/edge sweep).",

	"LANTERN_SHUTDOWN_TIMEOUT_SECONDS": "Graceful-shutdown drain budget for in-flight requests before a hard close.",
	"LANTERN_DRAIN_DELAY_SECONDS":      "Zero-drop rolling-update window: keep serving this long after readiness flips NOT_SERVING (0 = disabled).",

	"LANTERN_MAX_KEY_LEN":         "Maximum accepted vertex-key length in bytes.",
	"LANTERN_MAX_BATCH_SIZE":      "Maximum items accepted per batch RPC (Put/Get/Add/Delete plural forms).",
	"LANTERN_ILLUMINATE_MAX_STEP": "Upper bound on the Illuminate BFS step parameter.",
	"LANTERN_ILLUMINATE_MAX_K":    "Upper bound on the Illuminate k parameter.",

	"LANTERN_SCAN_DEFAULT_LIMIT":             "Page size used when a Scan* request leaves limit unset.",
	"LANTERN_SCAN_MAX_LIMIT":                 "Ceiling a Scan* request's limit is clamped to.",
	"LANTERN_DELETE_BY_PREFIX_DEFAULT_LIMIT": "Deletion cap used when DeleteVerticesByPrefix leaves limit unset.",
	"LANTERN_DELETE_BY_PREFIX_MAX_LIMIT":     "Ceiling DeleteVerticesByPrefix's limit is clamped to.",

	"LANTERN_SEARCH_ENABLED":       "Build the full-text search index and serve SearchVertices (off = FAILED_PRECONDITION).",
	"LANTERN_SEARCH_DEFAULT_LIMIT": "Ranked-hit count used when SearchVertices leaves limit unset.",
	"LANTERN_SEARCH_MAX_LIMIT":     "Ceiling SearchVertices' limit is clamped to.",

	"LANTERN_MUTATION_LOG_CAPACITY":          "Replication mutation-log ring capacity in entries; size for peak_cluster_rps x retention_seconds.",
	"LANTERN_MUTATION_LOG_SUBSCRIBER_BUFFER": "Per-subscriber outbound channel depth; a subscriber that falls further behind is gapped.",
	"LANTERN_NODE_ID":                        "Stable 32-hex-char (16-byte) node identity for HLC/replication; random per boot when unset.",
	"LANTERN_TOMBSTONE_TTL":                  "Delete-tombstone retention window and the upper bound on caller-supplied expirations.",

	"LANTERN_PEERS":                      "Comma-separated static peer list (host:port) for the replication pump; empty = single instance.",
	"LANTERN_PEER_DISCOVERY":             "Peer discovery mode: static or dns.",
	"LANTERN_PEER_DNS_NAME":              "DNS name resolved for peer discovery when LANTERN_PEER_DISCOVERY=dns (e.g. a headless Service).",
	"LANTERN_PEER_DEFAULT_PORT":          "Port appended to DNS-discovered peer addresses.",
	"LANTERN_PEER_DISCOVERY_INTERVAL_MS": "Peer re-resolution cadence in milliseconds (0 = resolve once at startup).",
	"LANTERN_PUMP_BACKOFF_MIN_MS":        "Initial reconnect backoff after a peer session error, in milliseconds.",
	"LANTERN_PUMP_BACKOFF_MAX_MS":        "Reconnect backoff ceiling, in milliseconds.",

	"LANTERN_ANTI_ENTROPY_INTERVAL_MS":          "Anti-entropy sweep cadence in milliseconds.",
	"LANTERN_ANTI_ENTROPY_SUBSCRIBE_TIMEOUT_MS": "Per-peer anti-entropy catch-up subscribe timeout in milliseconds.",
	"LANTERN_ANTI_ENTROPY_GAP_WARN_THRESHOLD":   "Origin-seq gap size above which the anti-entropy sweep logs a warning.",

	"LANTERN_MAX_REPLICATION_LAG": "Readiness gate: maximum tolerated replication lag (entries) before /readyz reports not ready.",

	"LANTERN_CORS_ALLOWED_ORIGINS": "Comma-separated browser origins allowed by CORS (e.g. the admin SPA origin); empty disables CORS.",

	"LANTERN_BACKUP_ENABLED":          "Enable the periodic whole-graph dump loop (requires LANTERN_BACKUP_DIR).",
	"LANTERN_BACKUP_DIR":              "Mounted directory dumps are written to and restored from.",
	"LANTERN_BACKUP_INTERVAL":         "Dump cadence (Go duration).",
	"LANTERN_BACKUP_RETAIN":           "How many of this instance's own dumps to keep, newest first (0 keeps all).",
	"LANTERN_BACKUP_INSTANCE_ID":      "Per-instance dump filename token for shared storage; defaults to the hostname.",
	"LANTERN_BACKUP_RESTORE_ON_START": "Replay the newest valid dump as a baseline before serving.",
	"LANTERN_BACKUP_RESTORE_REQUIRED": "Fail boot when restore-on-startup errors instead of starting with current state.",

	"LANTERN_STRICT_CONFIG": "Refuse to boot when any LANTERN_* value is malformed or an unknown LANTERN_* variable is set.",
}

// Render produces the docs/env.md markdown for the given registry specs. It
// returns an error when the description table and the registry disagree in
// either direction, so the generated reference can never drift from the code.
func Render(specs []envconfig.Spec) (string, error) {
	var missing, extra []string
	seen := map[string]bool{}
	for _, s := range specs {
		seen[s.Key] = true
		if _, ok := descriptions[s.Key]; !ok {
			missing = append(missing, s.Key)
		}
	}
	for k := range descriptions {
		if !seen[k] {
			extra = append(extra, k)
		}
	}
	sort.Strings(missing)
	sort.Strings(extra)
	if len(missing) > 0 || len(extra) > 0 {
		return "", fmt.Errorf("envdoc: description table out of sync with envconfig registry: missing descriptions %v, stale descriptions %v", missing, extra)
	}

	sorted := append([]envconfig.Spec(nil), specs...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Key < sorted[j].Key })

	var b strings.Builder
	b.WriteString("# Lantern server environment variables\n\n")
	b.WriteString("<!-- GENERATED FILE - do not edit. Regenerate with `make envdoc` (or `go generate ./...`). -->\n\n")
	b.WriteString("Every `LANTERN_*` variable the server reads, generated from the envconfig\n")
	b.WriteString("registry (#847). A set-but-malformed value logs a startup warning and falls\n")
	b.WriteString("back to its default; an unknown `LANTERN_*` name logs a typo warning; with\n")
	b.WriteString("`LANTERN_STRICT_CONFIG=true` either condition fails boot. An explicitly empty\n")
	b.WriteString("value is treated as unset for the non-string kinds. The MCP server's\n")
	b.WriteString("`LANTERN_MCP_*` namespace belongs to that process and is not listed here.\n\n")
	b.WriteString("| Variable | Type | Default | Description |\n")
	b.WriteString("|---|---|---|---|\n")
	for _, s := range sorted {
		def := s.Default
		if def == "" {
			def = "(empty)"
		} else {
			def = "`" + def + "`"
		}
		fmt.Fprintf(&b, "| `%s` | %s | %s | %s |\n", s.Key, s.Kind, def, descriptions[s.Key])
	}
	return b.String(), nil
}
