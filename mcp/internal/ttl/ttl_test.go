package ttl

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestParseBucket_CaseInsensitive(t *testing.T) {
	for _, in := range []string{"day", "Day", "DAY", "  day  "} {
		got, err := ParseBucket(in)
		if err != nil {
			t.Fatalf("ParseBucket(%q) returned error: %v", in, err)
		}
		if got != Day {
			t.Fatalf("ParseBucket(%q) = %v, want %v", in, got, Day)
		}
	}
}

func TestParseBucket_Unknown(t *testing.T) {
	if _, err := ParseBucket("forever"); err == nil {
		t.Fatal("ParseBucket(\"forever\") = nil error, want non-nil")
	}
}

func TestBucket_JSONRoundTrip(t *testing.T) {
	type wrap struct {
		B Bucket `json:"b"`
	}
	for _, b := range AllBuckets() {
		raw, err := json.Marshal(wrap{B: b})
		if err != nil {
			t.Fatalf("Marshal(%v) returned error: %v", b, err)
		}
		var got wrap
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("Unmarshal(%s) returned error: %v", raw, err)
		}
		if got.B != b {
			t.Fatalf("round-trip mismatch: %v → %s → %v", b, raw, got.B)
		}
	}
}

func TestBucket_UnmarshalJSON_RejectsUnknown(t *testing.T) {
	var b Bucket
	if err := json.Unmarshal([]byte(`"forever"`), &b); err == nil {
		t.Fatal("UnmarshalJSON(\"forever\") = nil error, want non-nil")
	}
}

func TestLoadFromEnv_DefaultsAreMonotonic(t *testing.T) {
	r, err := loadFrom(func(string) (string, bool) { return "", false })
	if err != nil {
		t.Fatalf("loadFrom(empty env) returned error: %v", err)
	}
	for _, b := range AllBuckets() {
		if r.Resolve(b) != Defaults[b] {
			t.Fatalf("Resolve(%v) = %v, want %v", b, r.Resolve(b), Defaults[b])
		}
	}
}

func TestLoadFromEnv_Override(t *testing.T) {
	env := map[string]string{
		EnvVar(Seconds): "45s",
		EnvVar(Durable): "365d", // invalid: ParseDuration does not accept "d"
	}
	// The "d" suffix is unsupported by time.ParseDuration, so the call must fail
	// — proves we surface the parse error verbatim instead of swallowing it.
	_, err := loadFrom(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err == nil {
		t.Fatal("loadFrom with invalid duration returned nil error")
	}
	if !strings.Contains(err.Error(), EnvVar(Durable)) {
		t.Fatalf("error %q does not mention bucket env var", err)
	}
}

func TestLoadFromEnv_OverrideValid(t *testing.T) {
	env := map[string]string{
		EnvVar(Seconds): "45s",
		EnvVar(Durable): "4320h", // 180d expressed in hours
	}
	r, err := loadFrom(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err != nil {
		t.Fatalf("loadFrom returned error: %v", err)
	}
	if r.Resolve(Seconds) != 45*time.Second {
		t.Fatalf("Resolve(Seconds) = %v, want 45s", r.Resolve(Seconds))
	}
	if r.Resolve(Durable) != 4320*time.Hour {
		t.Fatalf("Resolve(Durable) = %v, want 4320h", r.Resolve(Durable))
	}
	// untouched buckets keep defaults
	if r.Resolve(Day) != Defaults[Day] {
		t.Fatalf("Resolve(Day) = %v, want default %v", r.Resolve(Day), Defaults[Day])
	}
}

func TestLoadFromEnv_NonPositive(t *testing.T) {
	env := map[string]string{EnvVar(Seconds): "0s"}
	_, err := loadFrom(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err == nil {
		t.Fatal("loadFrom with 0s returned nil error")
	}
}

func TestLoadFromEnv_RejectsNonMonotonic(t *testing.T) {
	// Push `transient` below `seconds` to invert the order — the resolver
	// must refuse rather than silently swap.
	env := map[string]string{EnvVar(Transient): "10s"}
	_, err := loadFrom(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err == nil {
		t.Fatal("loadFrom with inverted ordering returned nil error")
	}
	if !errors.Is(err, errNonMonotonic) {
		t.Fatalf("err = %v, want errNonMonotonic", err)
	}
}

func TestLoadFromEnv_RejectsEqualConsecutive(t *testing.T) {
	// Equal consecutive horizons would still mislead the model — reject them
	// just like strictly inverted ones.
	env := map[string]string{EnvVar(Transient): "30s"}
	_, err := loadFrom(func(k string) (string, bool) { v, ok := env[k]; return v, ok })
	if err == nil {
		t.Fatal("loadFrom with equal consecutive returned nil error")
	}
}

func TestEnvVar(t *testing.T) {
	if got := EnvVar(Day); got != "LANTERN_MCP_TTL_DAY" {
		t.Fatalf("EnvVar(Day) = %q, want LANTERN_MCP_TTL_DAY", got)
	}
}
