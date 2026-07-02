package identity

import (
	"strings"
	"testing"
)

func TestResolve(t *testing.T) {
	t.Run("env id wins", func(t *testing.T) {
		if got := resolve("fleet-worker-7"); got != "fleet-worker-7" {
			t.Fatalf("resolve(env) = %q, want fleet-worker-7", got)
		}
	})

	t.Run("dots and whitespace sanitised out of env id", func(t *testing.T) {
		got := resolve("team a.builder 1")
		if strings.ContainsAny(got, ". \t\n") {
			t.Fatalf("sanitised id still carries separators: %q", got)
		}
		if got != "team-a-builder-1" {
			t.Fatalf("resolve = %q, want team-a-builder-1", got)
		}
	})

	t.Run("fallback is host-pid-rand shaped and stable per call set", func(t *testing.T) {
		a := resolve("")
		if a == "" || strings.Contains(a, ".") {
			t.Fatalf("fallback id malformed: %q", a)
		}
		// Distinct resolve("") calls differ in the random suffix — the
		// process-level stability contract lives in Resolve's sync.Once,
		// which memoises the FIRST resolution.
		if parts := strings.Split(a, "-"); len(parts) < 3 {
			t.Fatalf("fallback %q missing host-pid-rand shape", a)
		}
	})

	t.Run("Resolve memoises", func(t *testing.T) {
		first := Resolve()
		if second := Resolve(); second != first {
			t.Fatalf("Resolve changed identity mid-process: %q vs %q", first, second)
		}
	})
}
