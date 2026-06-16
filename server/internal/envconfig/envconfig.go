// Package envconfig is the single place where the server reads typed values
// out of process environment variables. Keeping these helpers in one
// dependency-light package makes them trivially unit-testable (no imports
// from the rest of the server tree) and frees the larger provider layer from
// reimplementing the same os.Getenv + strconv dance.
//
// All helpers fall back to the supplied default when the variable is unset
// or malformed — they never return an error.
package envconfig

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Duration returns the time.Duration value of the named env var (accepted
// by time.ParseDuration, e.g. "24h", "500ms"), or def when unset or
// unparseable.
func Duration(key string, def time.Duration) time.Duration {
	if v, err := time.ParseDuration(strings.TrimSpace(os.Getenv(key))); err == nil {
		return v
	}
	return def
}

// Int returns the integer value of the named env var, or def when unset or
// not a valid base-10 integer.
func Int(key string, def int) int {
	if v, err := strconv.Atoi(os.Getenv(key)); err == nil {
		return v
	}
	return def
}

// Uint32 returns the uint32 value of the named env var, or def when unset,
// not a valid base-10 unsigned integer, or outside the uint32 range. Parsing
// with a 32-bit size makes the conversion bounds-safe: an out-of-range value
// yields an error (and the default) rather than silently truncating.
func Uint32(key string, def uint32) uint32 {
	if v, err := strconv.ParseUint(os.Getenv(key), 10, 32); err == nil {
		return uint32(v)
	}
	return def
}

// Float returns the float64 value of the named env var, or def when unset
// or not a valid float.
func Float(key string, def float64) float64 {
	if v, err := strconv.ParseFloat(os.Getenv(key), 64); err == nil {
		return v
	}
	return def
}

// String returns the value of the named env var, or def when unset.
// An explicitly empty value ("") is returned as-is — only a missing variable
// falls back to the default.
func String(key, def string) string {
	if v, ok := os.LookupEnv(key); ok {
		return v
	}
	return def
}

// Bool returns the boolean value of the named env var, or def when unset or
// not one of the recognised truthy/falsy spellings ("1"/"true"/"yes"/"on"
// vs "0"/"false"/"no"/"off", case- and whitespace-insensitive).
func Bool(key string, def bool) bool {
	v, ok := os.LookupEnv(key)
	if !ok {
		return def
	}
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		return def
	}
}

// LogLevel parses a slog level from a free-form string ("debug"/"info"/
// "warn"|"warning"/"error", case- and whitespace-insensitive). Anything
// unrecognised falls back to slog.LevelInfo.
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
