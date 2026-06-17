package service

import (
	"testing"
	"time"
)

// TestFormatWriteEcho pins the REPL success-echo formatting (#653): a
// mutating write must surface its applied TTL and absolute expiry so a
// decaying write is never silent. A fixed clock keeps the expiry
// deterministic.
func TestFormatWriteEcho(t *testing.T) {
	now := time.Date(2026, 6, 16, 12, 34, 56, 0, time.UTC)

	t.Run("PositiveTTLEchoesDurationAndAbsoluteExpiry", func(t *testing.T) {
		got := formatWriteEcho(`put vertex "a"`, 1*time.Second, now)
		want := `put vertex "a" (ttl 1s, expires 2026-06-16T12:34:57Z)`
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})

	t.Run("ZeroTTLIsPermanentSentinel", func(t *testing.T) {
		got := formatWriteEcho(`put vertex "permkey"`, 0, now)
		want := `put vertex "permkey" (no ttl)`
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})

	t.Run("NegativeTTLIsAlsoPermanent", func(t *testing.T) {
		got := formatWriteEcho("add edge", -5*time.Second, now)
		want := "add edge (no ttl)"
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})

	t.Run("MultiUnitTTLRendersViaDurationString", func(t *testing.T) {
		got := formatWriteEcho(`put edge "a" -> "b" (weight 1.5)`, 90*time.Second, now)
		want := `put edge "a" -> "b" (weight 1.5) (ttl 1m30s, expires 2026-06-16T12:36:26Z)`
		if got != want {
			t.Errorf("formatWriteEcho() = %q, want %q", got, want)
		}
	})
}
