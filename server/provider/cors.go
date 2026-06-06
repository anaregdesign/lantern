package provider

import (
	"net/http"
	"strings"

	"github.com/anaregdesign/lantern/server/internal/envconfig"
)

// CORSConfig governs the cross-origin policy enforced on top of the
// grpc-gateway HTTP handler. It exists to let the `lantern-admin` SPA call
// `/v1/...` from a different origin (e.g. `http://localhost:5173` during
// dev, or the GHCR-served admin container in deployment).
//
//   - LANTERN_CORS_ALLOWED_ORIGINS    Comma-separated list of exact
//     origins permitted to call the gateway. Empty (default) leaves the
//     handler untouched, so existing single-port deployments are
//     byte-for-byte unchanged. The special value "*" allows any origin —
//     but ONLY when it is the only entry in the list, mirroring the
//     fetch-spec restriction on `*` together with credentials.
//
// Allow-Credentials is intentionally never set to true: v1 admin has no
// auth and never sends cookies, so keeping the door closed avoids
// accidentally widening the policy when auth lands later.
type CORSConfig struct {
	// AllowedOrigins is the parsed, trimmed allow-list. Length 0 ⇒ CORS
	// disabled. Length 1 with the single entry "*" ⇒ wildcard mode.
	AllowedOrigins []string
}

// NewCORSConfig selects the CORS slice of Config.
func NewCORSConfig(c *Config) CORSConfig { return c.CORS }

// loadCORSConfig reads CORSConfig from the environment.
//
// Parsing rules:
//   - split on comma
//   - trim whitespace around each token
//   - drop empty tokens (so a trailing comma is harmless)
//   - if "*" appears with any other token, ignore the wildcard and keep
//     only the explicit origins (safer default than silently expanding to
//     any origin)
func loadCORSConfig() CORSConfig {
	raw := envconfig.String("LANTERN_CORS_ALLOWED_ORIGINS", "")
	if raw == "" {
		return CORSConfig{}
	}
	parts := strings.Split(raw, ",")
	origins := make([]string, 0, len(parts))
	for _, p := range parts {
		t := strings.TrimSpace(p)
		if t == "" {
			continue
		}
		origins = append(origins, t)
	}
	if len(origins) > 1 {
		filtered := origins[:0]
		for _, o := range origins {
			if o == "*" {
				continue
			}
			filtered = append(filtered, o)
		}
		origins = filtered
	}
	return CORSConfig{AllowedOrigins: origins}
}

// corsAllowedMethods and corsAllowedHeaders are advertised on the preflight
// response. They match the surface declared by the grpc-gateway annotations
// today plus the headers the admin SPA needs (Content-Type for JSON
// payloads, Authorization for the future bearer flow, X-Request-Id for
// correlation logging).
var (
	corsAllowedMethods = "GET, POST, PUT, DELETE, OPTIONS"
	corsAllowedHeaders = "Content-Type, Authorization, X-Request-Id"
)

// CORSMiddleware returns an HTTP middleware that applies cfg to every
// request. When cfg.AllowedOrigins is empty the middleware is the identity
// function — callers can wire it unconditionally and pay zero overhead in
// the disabled case.
//
// Behaviour:
//   - The request's Origin header is matched against the allow-list. A
//     wildcard ("*" as the sole entry) matches every non-empty Origin and
//     echoes the literal "*" back, which is sufficient for the
//     no-credentials profile v1 ships with.
//   - When the Origin is allowed, Access-Control-Allow-Origin is set to
//     the matched value (the literal origin for explicit matches; "*" for
//     wildcard), and Vary: Origin is appended so caches do not collapse
//     responses across origins.
//   - Preflight requests (OPTIONS with Access-Control-Request-Method) get
//     a 204 with Access-Control-Allow-Methods and Access-Control-Allow-Headers
//     and never reach the downstream handler.
//   - Requests from a disallowed origin pass through to the handler
//     without any Access-Control-* header — the browser then enforces the
//     same-origin policy on the response, which is the standard CORS
//     contract.
func CORSMiddleware(cfg CORSConfig) func(http.Handler) http.Handler {
	if len(cfg.AllowedOrigins) == 0 {
		return func(next http.Handler) http.Handler { return next }
	}

	// Pre-compute the lookup. The expected list size is small (a handful
	// of dev / prod origins), so a map keeps the allow check O(1)
	// without pulling in any sort/binary-search ceremony.
	wildcard := len(cfg.AllowedOrigins) == 1 && cfg.AllowedOrigins[0] == "*"
	allow := make(map[string]struct{}, len(cfg.AllowedOrigins))
	for _, o := range cfg.AllowedOrigins {
		allow[o] = struct{}{}
	}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			origin := r.Header.Get("Origin")
			allowed, echo := matchOrigin(origin, wildcard, allow)

			if allowed {
				w.Header().Set("Access-Control-Allow-Origin", echo)
				w.Header().Add("Vary", "Origin")
			}

			// Preflight: OPTIONS + Access-Control-Request-Method. The
			// CORS spec requires the second header; treating bare
			// OPTIONS as a normal request (rather than swallowing it
			// with 204) lets the gateway answer OPTIONS-mapped RPCs in
			// the future without this middleware getting in the way.
			if r.Method == http.MethodOptions && r.Header.Get("Access-Control-Request-Method") != "" {
				if allowed {
					w.Header().Set("Access-Control-Allow-Methods", corsAllowedMethods)
					w.Header().Set("Access-Control-Allow-Headers", corsAllowedHeaders)
					// Browsers cache the preflight for up to this
					// many seconds. 10 minutes keeps the admin SPA
					// snappy without overcommitting on stale policy.
					w.Header().Set("Access-Control-Max-Age", "600")
				}
				w.WriteHeader(http.StatusNoContent)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// matchOrigin returns whether the request's Origin should be allowed and
// the value to echo in Access-Control-Allow-Origin. The echo distinguishes
// the wildcard ("*") case from explicit-origin matches.
func matchOrigin(origin string, wildcard bool, allow map[string]struct{}) (bool, string) {
	if origin == "" {
		return false, ""
	}
	if wildcard {
		return true, "*"
	}
	if _, ok := allow[origin]; ok {
		return true, origin
	}
	return false, ""
}
