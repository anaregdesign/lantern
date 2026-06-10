package mcp

import (
	"strings"
	"testing"
)

// TestLintKey_GoodKeysReturnNoWarnings is the back-stop for the acceptance
// criterion "good keys return no warnings": the canonical namespace shapes the
// memory instructions teach must lint clean.
func TestLintKey_GoodKeysReturnNoWarnings(t *testing.T) {
	good := []string{
		"user.preferences.tone",
		"user.identity.role",
		"project.lantern.stack",
		"session.current-task",
		"task.today.focus",
		"user.tone",
		"session.cursor",
		// An immutable-event leaf may legitimately end in a date.
		"session.filed-issues-2026-06-10",
	}
	for _, key := range good {
		if w := lintKey(key); len(w) != 0 {
			t.Errorf("lintKey(%q) = %v, want no warnings", key, w)
		}
	}
}

func TestLintKey_FlagsWhitespace(t *testing.T) {
	w := lintKey("user.full name")
	if !hasWarningContaining(w, "whitespace") {
		t.Fatalf("lintKey should flag whitespace; got %v", w)
	}
	if len(w) != 1 {
		t.Errorf("expected exactly the whitespace warning, got %v", w)
	}
}

func TestLintKey_FlagsMidKeyDate(t *testing.T) {
	w := lintKey("user.2026-06-10.note")
	if !hasWarningContaining(w, "high-cardinality") {
		t.Fatalf("lintKey should flag a mid-key date; got %v", w)
	}
	if len(w) != 1 {
		t.Errorf("expected only the high-cardinality warning, got %v", w)
	}
}

func TestLintKey_FlagsMidKeyUUID(t *testing.T) {
	w := lintKey("user.550e8400-e29b-41d4-a716-446655440000.note")
	if !hasWarningContaining(w, "high-cardinality") {
		t.Fatalf("lintKey should flag a mid-key UUID; got %v", w)
	}
}

func TestLintKey_FlagsMidKeyLongDigitRun(t *testing.T) {
	w := lintKey("user.20260610.note")
	if !hasWarningContaining(w, "high-cardinality") {
		t.Fatalf("lintKey should flag a mid-key digit run; got %v", w)
	}
}

func TestLintKey_AllowsDateInLeaf(t *testing.T) {
	// A high-cardinality token IS allowed as the final segment (unique event).
	w := lintKey("session.run-2026-06-10")
	if hasWarningContaining(w, "high-cardinality") {
		t.Fatalf("a date in the leaf must NOT be flagged; got %v", w)
	}
}

func TestLintKey_FlagsFreeTextLeaf(t *testing.T) {
	w := lintKey("user.notes.this-is-a-long-free-text-leaf")
	if !hasWarningContaining(w, "free text") {
		t.Fatalf("lintKey should flag a multi-word leaf; got %v", w)
	}
}

func TestLintKey_FlagsExcessiveDepth(t *testing.T) {
	w := lintKey("user.a.b.c.d.e.f")
	if !hasWarningContaining(w, "segments") {
		t.Fatalf("lintKey should flag excessive depth; got %v", w)
	}
}

func TestLintKey_FlagsUnrecognizedScope(t *testing.T) {
	w := lintKey("topic.lantern.note")
	if !hasWarningContaining(w, "recognized scope") {
		t.Fatalf("lintKey should flag a missing scope head; got %v", w)
	}
	if len(w) != 1 {
		t.Errorf("expected only the scope warning, got %v", w)
	}
}

func TestLintKey_AccumulatesMultipleWarnings(t *testing.T) {
	// Unrecognized scope + mid-key date + whitespace + free-text leaf, at once.
	w := lintKey("topic.2026-06-10.free text leaf here")
	if len(w) < 4 {
		t.Fatalf("expected several warnings for a key that breaks multiple rules; got %v", w)
	}
}

func hasWarningContaining(warnings []string, substr string) bool {
	for _, w := range warnings {
		if strings.Contains(w, substr) {
			return true
		}
	}
	return false
}
