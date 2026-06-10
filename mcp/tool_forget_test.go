package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestForget_ExistingKey(t *testing.T) {
	h := newTestHarness(t)
	h.fake.deleteVertexFn = func(_ context.Context, _ string) (bool, error) {
		return true, nil
	}
	res := h.call(t, "forget", map[string]any{"key": "k"})
	if res.IsError {
		t.Fatalf("IsError = true")
	}
	var out forgetOutput
	structuredAs(t, res, &out)
	if !out.Existed || out.Key != "k" {
		t.Fatalf("unexpected output: %+v", out)
	}
	if h.fake.lastDeleteKey != "k" {
		t.Fatalf("lastDeleteKey = %q", h.fake.lastDeleteKey)
	}
}

func TestForget_MissingKeyIsNotError(t *testing.T) {
	h := newTestHarness(t)
	h.fake.deleteVertexFn = func(_ context.Context, _ string) (bool, error) {
		return false, nil
	}
	res := h.call(t, "forget", map[string]any{"key": "ghost"})
	if res.IsError {
		t.Fatalf("missing key must be idempotent, not error")
	}
	var out forgetOutput
	structuredAs(t, res, &out)
	if out.Existed {
		t.Fatalf("Existed = true; want false")
	}
}

func TestForget_RejectsEmptyKey(t *testing.T) {
	h := newTestHarness(t)
	h.callExpectError(t, "forget", map[string]any{"key": ""})
}

// TestForgetDescription_DiscouragesRoutineUse guards the framing that
// forget is for wrong/unwanted facts, not routine staleness (TTL handles
// that), so the agent does not over-delete (#528).
func TestForgetDescription_DiscouragesRoutineUse(t *testing.T) {
	if !strings.Contains(forgetDescription, "TTL decay") {
		t.Errorf("forgetDescription should point routine staleness at TTL decay: %q", forgetDescription)
	}
}
