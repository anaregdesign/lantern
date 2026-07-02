// Package envconfig is the single place where the server reads typed values
// out of process environment variables. Keeping these helpers in one
// dependency-light package makes them trivially unit-testable (no imports
// from the rest of the server tree) and frees the larger provider layer from
// reimplementing the same os.Getenv + strconv dance.
//
// All helpers fall back to the supplied default when the variable is unset
// or malformed — they never return an error. Since #847 they additionally
// feed two process-wide records so the composition root can fail loudly at
// boot instead of silently running on defaults:
//
//   - a REGISTRY of every variable a helper has been asked about (name,
//     parsed kind, rendered default) — the source of truth for the unknown-
//     variable sweep and the generated docs/env.md reference;
//   - a FINDINGS list of variables that were SET but unparseable, so the
//     startup validation can warn (or, under LANTERN_STRICT_CONFIG, abort)
//     with the exact key and reason instead of nothing at all.
//
// An explicitly empty value ("", or whitespace for the trimmed kinds) is
// treated as UNSET for the non-string kinds — `LANTERN_FOO=` in a manifest
// conventionally means "use the default" and must not trip strict mode.
package envconfig

import (
	"log/slog"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Prefix is the namespace the unknown-variable sweep scans for.
const Prefix = "LANTERN_"

// Spec describes one known environment variable: its name, the kind the
// loader parses it as, and the rendered default used when it is unset or
// malformed. Registered as a side effect of every helper call, so the set of
// Specs after a full config load is exactly the server's env contract.
type Spec struct {
	Key     string
	Kind    string
	Default string
}

// Finding records a variable that was set but failed to parse; the loader
// fell back to its default. Raw is the offending value verbatim.
type Finding struct {
	Key    string
	Raw    string
	Reason string
}

// Unknown is one LANTERN_*-prefixed environment variable that no loader ever
// registered — almost always an operator typo. Suggestion carries the
// closest registered key when one is plausibly close, else "".
type Unknown struct {
	Key        string
	Suggestion string
}

var (
	mu       sync.Mutex
	registry = map[string]Spec{}
	findings []Finding
	setKeys  = map[string]struct{}{}
)

func register(key, kind, def string) {
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[key]; !ok {
		registry[key] = Spec{Key: key, Kind: kind, Default: def}
	}
}

func markSet(key string) {
	mu.Lock()
	defer mu.Unlock()
	setKeys[key] = struct{}{}
}

// Malformed records a set-but-unparseable value for key. The typed helpers
// call it internally; it is exported for the few loaders that parse a raw
// String themselves (e.g. the LANTERN_NODE_ID hex decode) so custom parse
// failures land in the same startup report as everything else.
func Malformed(key, raw, reason string) {
	mu.Lock()
	defer mu.Unlock()
	findings = append(findings, Finding{Key: key, Raw: raw, Reason: reason})
}

// Known returns every registered variable, sorted by key.
func Known() []Spec {
	mu.Lock()
	defer mu.Unlock()
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// Findings returns the malformed-value records collected so far, in call
// order.
func Findings() []Finding {
	mu.Lock()
	defer mu.Unlock()
	return append([]Finding(nil), findings...)
}

// SetKeys returns the sorted names of registered variables that were set in
// the environment (whether or not they parsed). Values are deliberately not
// reported — a future secret-bearing variable must never end up in the boot
// log.
func SetKeys() []string {
	mu.Lock()
	defer mu.Unlock()
	out := make([]string, 0, len(setKeys))
	for k := range setKeys {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// ResetForTesting clears the registry, findings, and set-key records so a
// test can load a fresh config without cross-test bleed. Not for production
// use.
func ResetForTesting() {
	mu.Lock()
	defer mu.Unlock()
	registry = map[string]Spec{}
	findings = nil
	setKeys = map[string]struct{}{}
}

// UnknownLanternVars scans environ (the os.Environ() slice format,
// "KEY=value") for Prefix-named variables that were never registered by any
// loader. Names starting with any of ignorePrefixes are skipped — that is
// the escape hatch for sibling processes that legitimately share an env file
// (e.g. the MCP server's LANTERN_MCP_* namespace). Results are sorted by
// key, each with a did-you-mean suggestion when a registered key is within
// small edit distance.
func UnknownLanternVars(environ []string, ignorePrefixes ...string) []Unknown {
	mu.Lock()
	known := make([]string, 0, len(registry))
	for k := range registry {
		known = append(known, k)
	}
	mu.Unlock()

	var out []Unknown
	for _, kv := range environ {
		eq := strings.IndexByte(kv, '=')
		if eq < 0 {
			continue
		}
		key := kv[:eq]
		if !strings.HasPrefix(key, Prefix) {
			continue
		}
		if func() bool {
			for _, p := range ignorePrefixes {
				if strings.HasPrefix(key, p) {
					return true
				}
			}
			return false
		}() {
			continue
		}
		if func() bool {
			mu.Lock()
			defer mu.Unlock()
			_, ok := registry[key]
			return ok
		}() {
			continue
		}
		out = append(out, Unknown{Key: key, Suggestion: suggest(key, known)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out
}

// suggest returns the registered key closest to key when the edit distance
// is small enough to plausibly be a typo (≤3), else "".
func suggest(key string, known []string) string {
	best, bestDist := "", 4
	for _, k := range known {
		if d := editDistance(key, k, bestDist); d < bestDist {
			best, bestDist = k, d
		}
	}
	return best
}

// editDistance is a bounded Levenshtein distance: it returns limit as soon
// as the true distance is provably >= limit, which keeps the sweep cheap for
// wholly unrelated names.
func editDistance(a, b string, limit int) int {
	if d := len(a) - len(b); d >= limit || -d >= limit {
		return limit
	}
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		rowMin := cur[0]
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			m := prev[j-1] + cost
			if v := prev[j] + 1; v < m {
				m = v
			}
			if v := cur[j-1] + 1; v < m {
				m = v
			}
			cur[j] = m
			if m < rowMin {
				rowMin = m
			}
		}
		if rowMin >= limit {
			return limit
		}
		prev, cur = cur, prev
	}
	if prev[len(b)] > limit {
		return limit
	}
	return prev[len(b)]
}

// lookup fetches key, treating an all-whitespace value as unset for the
// non-string kinds (a bare `LANTERN_FOO=` conventionally means "default").
func lookup(key string) (string, bool) {
	raw, ok := os.LookupEnv(key)
	if !ok {
		return "", false
	}
	markSet(key)
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", false
	}
	return trimmed, true
}

// Duration returns the time.Duration value of the named env var (accepted
// by time.ParseDuration, e.g. "24h", "500ms"), or def when unset or
// unparseable.
func Duration(key string, def time.Duration) time.Duration {
	register(key, "duration", def.String())
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	v, err := time.ParseDuration(raw)
	if err != nil {
		Malformed(key, raw, `not a Go duration (e.g. "30s", "24h")`)
		return def
	}
	return v
}

// Int returns the integer value of the named env var, or def when unset or
// not a valid base-10 integer.
func Int(key string, def int) int {
	register(key, "int", strconv.Itoa(def))
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.Atoi(raw)
	if err != nil {
		Malformed(key, raw, "not a base-10 integer")
		return def
	}
	return v
}

// Uint32 returns the uint32 value of the named env var, or def when unset,
// not a valid base-10 unsigned integer, or outside the uint32 range. Parsing
// with a 32-bit size makes the conversion bounds-safe: an out-of-range value
// yields an error (and the default) rather than silently truncating.
func Uint32(key string, def uint32) uint32 {
	register(key, "uint32", strconv.FormatUint(uint64(def), 10))
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseUint(raw, 10, 32)
	if err != nil {
		Malformed(key, raw, "not an unsigned 32-bit integer")
		return def
	}
	return uint32(v)
}

// Float returns the float64 value of the named env var, or def when unset
// or not a valid float.
func Float(key string, def float64) float64 {
	register(key, "float", strconv.FormatFloat(def, 'g', -1, 64))
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		Malformed(key, raw, "not a floating-point number")
		return def
	}
	return v
}

// String returns the value of the named env var, or def when unset.
// An explicitly empty value ("") is returned as-is — only a missing variable
// falls back to the default.
func String(key, def string) string {
	register(key, "string", def)
	if v, ok := os.LookupEnv(key); ok {
		markSet(key)
		return v
	}
	return def
}

// Bool returns the boolean value of the named env var, or def when unset or
// not one of the recognised truthy/falsy spellings ("1"/"true"/"yes"/"on"
// vs "0"/"false"/"no"/"off", case- and whitespace-insensitive).
func Bool(key string, def bool) bool {
	register(key, "bool", strconv.FormatBool(def))
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	switch strings.ToLower(raw) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		Malformed(key, raw, `not a boolean ("true"/"false"/"1"/"0"/"yes"/"no"/"on"/"off")`)
		return def
	}
}

// Level returns the slog level of the named env var ("debug"/"info"/
// "warn"|"warning"/"error", case- and whitespace-insensitive), or def when
// unset or unrecognised. It is the registry-aware sibling of LogLevel.
func Level(key string, def slog.Level) slog.Level {
	register(key, "level", strings.ToLower(def.String()))
	raw, ok := lookup(key)
	if !ok {
		return def
	}
	switch strings.ToLower(raw) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		Malformed(key, raw, `not a log level ("debug"/"info"/"warn"/"error")`)
		return def
	}
}

// LogLevel parses a slog level from a free-form string ("debug"/"info"/
// "warn"|"warning"/"error", case- and whitespace-insensitive). Anything
// unrecognised falls back to slog.LevelInfo. Prefer Level for env-var reads —
// it registers the key and reports malformed values; LogLevel remains for
// callers that already hold the raw string.
func LogLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "warn", "warning":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
