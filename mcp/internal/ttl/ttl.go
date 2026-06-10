// Package ttl owns the 12-bucket TTL enum exposed by lantern-mcp tools.
//
// The bucket scheme is what every remember_*/recall_related tool exposes as
// a required string parameter, so LLM callers have to pick a horizon
// explicitly rather than silently falling back to a server default. The
// defaults below are the canonical horizons documented in epic #283; each
// is individually overridable via the LANTERN_MCP_TTL_<BUCKET> environment
// variable using time.ParseDuration syntax (e.g. "45s", "2h30m").
//
// Validation rule: regardless of overrides, the resolved durations must
// remain strictly monotonic — seconds < transient < … < durable. A
// misordered configuration is treated as a fatal startup error, never
// silently corrected, because the bucket labels carry semantic meaning to
// the LLM and a transposed pair would lie to the model.
package ttl

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"
)

// Bucket is the 12-step decay horizon advertised to MCP tool callers.
type Bucket uint8

// The bucket constants are declared in monotonic order; iteration over
// AllBuckets relies on this ordering.
const (
	Seconds Bucket = iota
	Transient
	Turn
	Conversation
	Task
	Workday
	Day
	Week
	Sprint
	Month
	Quarter
	Durable
)

// AllBuckets returns the buckets in monotonic order. The slice is freshly
// allocated; callers may mutate it freely.
func AllBuckets() []Bucket {
	return []Bucket{
		Seconds, Transient, Turn, Conversation, Task, Workday,
		Day, Week, Sprint, Month, Quarter, Durable,
	}
}

// String returns the canonical lower-case label for b. The label is what
// MCP tool callers pass in their JSON arguments and what env-var overrides
// suffix.
func (b Bucket) String() string {
	switch b {
	case Seconds:
		return "seconds"
	case Transient:
		return "transient"
	case Turn:
		return "turn"
	case Conversation:
		return "conversation"
	case Task:
		return "task"
	case Workday:
		return "workday"
	case Day:
		return "day"
	case Week:
		return "week"
	case Sprint:
		return "sprint"
	case Month:
		return "month"
	case Quarter:
		return "quarter"
	case Durable:
		return "durable"
	}
	return fmt.Sprintf("bucket(%d)", uint8(b))
}

// ParseBucket parses a bucket label. Matching is case-insensitive so MCP
// callers can write "Day" or "DAY" without surprise.
func ParseBucket(s string) (Bucket, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "seconds":
		return Seconds, nil
	case "transient":
		return Transient, nil
	case "turn":
		return Turn, nil
	case "conversation":
		return Conversation, nil
	case "task":
		return Task, nil
	case "workday":
		return Workday, nil
	case "day":
		return Day, nil
	case "week":
		return Week, nil
	case "sprint":
		return Sprint, nil
	case "month":
		return Month, nil
	case "quarter":
		return Quarter, nil
	case "durable":
		return Durable, nil
	}
	return 0, fmt.Errorf("ttl: unknown bucket %q", s)
}

// MarshalJSON encodes b as its canonical string label.
func (b Bucket) MarshalJSON() ([]byte, error) {
	return []byte(`"` + b.String() + `"`), nil
}

// UnmarshalJSON accepts the canonical labels (case-insensitive). Anything
// else returns an error — this is what causes the MCP framework to reject
// a malformed tool argument before it reaches the handler.
func (b *Bucket) UnmarshalJSON(data []byte) error {
	s := strings.TrimSpace(string(data))
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	parsed, err := ParseBucket(s)
	if err != nil {
		return err
	}
	*b = parsed
	return nil
}

// Defaults are the documented horizons from epic #283. Changing a default
// is a backward-incompatible change to LLM-facing tool behavior — bump the
// container tag and the README in the same PR.
var Defaults = map[Bucket]time.Duration{
	Seconds:      30 * time.Second,
	Transient:    2 * time.Minute,
	Turn:         10 * time.Minute,
	Conversation: time.Hour,
	Task:         4 * time.Hour,
	Workday:      12 * time.Hour,
	Day:          24 * time.Hour,
	Week:         7 * 24 * time.Hour,
	Sprint:       14 * 24 * time.Hour,
	Month:        30 * 24 * time.Hour,
	Quarter:      90 * 24 * time.Hour,
	Durable:      180 * 24 * time.Hour,
}

// EnvVar returns the LANTERN_MCP_TTL_<BUCKET> environment-variable name
// that overrides the default for b.
func EnvVar(b Bucket) string {
	return "LANTERN_MCP_TTL_" + strings.ToUpper(b.String())
}

// MaxTTLEnvVar is the environment variable that caps every resolved bucket
// duration. It exists so an operator whose Lantern server runs a low
// LANTERN_TOMBSTONE_TTL (which rejects any Expiration beyond it with
// invalid_argument) can have the MCP clamp long buckets down to a value
// the server will accept, instead of surfacing a hard error for week+
// horizons. Unset or non-positive means no cap — the default server
// LANTERN_TOMBSTONE_TTL (8760h) already exceeds the longest bucket.
const MaxTTLEnvVar = "LANTERN_MCP_MAX_TTL"

// Resolver maps each bucket to its configured duration after applying env
// overrides. It is the single source of truth for ttl.Resolve at runtime.
type Resolver struct {
	values map[Bucket]time.Duration
	// maxTTL clamps every resolved duration when positive; zero disables
	// the cap. Configured via LANTERN_MCP_MAX_TTL.
	maxTTL time.Duration
}

// Resolve returns the configured duration for b, or panics if b is not a
// valid bucket — bucket values originate from ParseBucket so this should
// be unreachable in normal flow. Resolve returns the bucket's nominal
// horizon and does NOT apply the LANTERN_MCP_MAX_TTL cap; callers that
// write to the server should use ResolveCapped.
func (r *Resolver) Resolve(b Bucket) time.Duration {
	d, ok := r.values[b]
	if !ok {
		panic(fmt.Sprintf("ttl: bucket %v not configured", b))
	}
	return d
}

// MaxTTL reports the configured cap, or zero when no cap is set.
func (r *Resolver) MaxTTL() time.Duration {
	return r.maxTTL
}

// ResolveCapped returns the duration the MCP should actually send to the
// server for bucket b: the bucket's nominal horizon clamped to
// LANTERN_MCP_MAX_TTL when that cap is configured and shorter. The second
// return value reports whether the clamp fired, so handlers can tell the
// caller their requested horizon was shortened rather than silently
// writing a different expiry.
func (r *Resolver) ResolveCapped(b Bucket) (time.Duration, bool) {
	d := r.Resolve(b)
	if r.maxTTL > 0 && d > r.maxTTL {
		return r.maxTTL, true
	}
	return d, false
}

// LoadFromEnv constructs a Resolver from os.LookupEnv overrides applied on
// top of Defaults, then validates strict monotonic ordering. Equal
// consecutive values are rejected because the LLM-facing labels promise
// distinct horizons.
//
// Returns the resolver and an error describing the first ordering or parse
// failure. The caller (main) should log and exit on error — a misordered
// resolver would silently mislead LLMs and is not recoverable.
func LoadFromEnv() (*Resolver, error) {
	return loadFrom(os.LookupEnv)
}

// loadFrom is the env-injection seam used by tests.
func loadFrom(lookup func(string) (string, bool)) (*Resolver, error) {
	values := make(map[Bucket]time.Duration, len(Defaults))
	for b, d := range Defaults {
		values[b] = d
	}
	for _, b := range AllBuckets() {
		raw, ok := lookup(EnvVar(b))
		if !ok {
			continue
		}
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("ttl: %s=%q: %w", EnvVar(b), raw, err)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("ttl: %s=%q: duration must be positive", EnvVar(b), raw)
		}
		values[b] = parsed
	}
	if err := validateMonotonic(values); err != nil {
		return nil, err
	}
	var maxTTL time.Duration
	if raw, ok := lookup(MaxTTLEnvVar); ok {
		parsed, err := time.ParseDuration(raw)
		if err != nil {
			return nil, fmt.Errorf("ttl: %s=%q: %w", MaxTTLEnvVar, raw, err)
		}
		if parsed <= 0 {
			return nil, fmt.Errorf("ttl: %s=%q: duration must be positive", MaxTTLEnvVar, raw)
		}
		maxTTL = parsed
	}
	return &Resolver{values: values, maxTTL: maxTTL}, nil
}

// errNonMonotonic indicates a configuration that would lie to the model
// about decay horizons. Exported as a sentinel for tests.
var errNonMonotonic = errors.New("ttl: bucket durations must be strictly monotonic")

func validateMonotonic(values map[Bucket]time.Duration) error {
	buckets := AllBuckets()
	for i := 1; i < len(buckets); i++ {
		prev, curr := buckets[i-1], buckets[i]
		if values[curr] <= values[prev] {
			return fmt.Errorf("%w: %s=%s must be > %s=%s",
				errNonMonotonic,
				curr.String(), values[curr],
				prev.String(), values[prev],
			)
		}
	}
	return nil
}
