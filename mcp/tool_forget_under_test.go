package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

// seqDeleter returns a DeleteVerticesByPrefix stub that yields the supplied
// per-round counts in order, then 0 forever after — modelling the server
// draining a namespace one limited batch at a time.
func seqDeleter(counts ...uint64) func(context.Context, string, ...client.DeleteByPrefixOption) (uint64, error) {
	i := 0
	return func(_ context.Context, _ string, _ ...client.DeleteByPrefixOption) (uint64, error) {
		if i >= len(counts) {
			return 0, nil
		}
		n := counts[i]
		i++
		return n, nil
	}
}

func TestForgetUnder_DryRunCountsWithoutDeleting(t *testing.T) {
	h := newTestHarness(t)
	h.fake.countByPrefixFn = func(_ context.Context, _ string) (uint64, error) {
		return 5, nil
	}
	res := h.call(t, "forget_under", map[string]any{"prefix": "session.verify.", "dry_run": true})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out forgetUnderOutput
	structuredAs(t, res, &out)
	if !out.DryRun || out.Count != 5 || out.Prefix != "session.verify." {
		t.Fatalf("unexpected output: %+v", out)
	}
	if out.Truncated {
		t.Fatalf("dry run must not set truncated")
	}
	// A dry run must NOT delete anything.
	if h.fake.deleteByPrefixCalls != 0 {
		t.Fatalf("deleteByPrefixCalls = %d; want 0 on dry run", h.fake.deleteByPrefixCalls)
	}
	if h.fake.lastCountPrefix != "session.verify." {
		t.Fatalf("lastCountPrefix = %q", h.fake.lastCountPrefix)
	}
	if txt := contentText(res); !strings.Contains(txt, "would be deleted") {
		t.Fatalf("text should frame the dry run: %q", txt)
	}
}

func TestForgetUnder_DeletesAndReturnsCount(t *testing.T) {
	h := newTestHarness(t)
	h.fake.deleteByPrefixFn = seqDeleter(3)
	res := h.call(t, "forget_under", map[string]any{"prefix": "session.verify."})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out forgetUnderOutput
	structuredAs(t, res, &out)
	if out.DryRun || out.Count != 3 || out.Truncated {
		t.Fatalf("unexpected output: %+v", out)
	}
	// One real round (returns 3) plus the terminating round (returns 0).
	if h.fake.deleteByPrefixCalls != 2 {
		t.Fatalf("deleteByPrefixCalls = %d; want 2", h.fake.deleteByPrefixCalls)
	}
	if h.fake.lastDeletePrefix != "session.verify." {
		t.Fatalf("lastDeletePrefix = %q", h.fake.lastDeletePrefix)
	}
	// The real path must not consult the count endpoint.
	if h.fake.lastCountPrefix != "" {
		t.Fatalf("CountVerticesByPrefix called on real delete: %q", h.fake.lastCountPrefix)
	}
}

func TestForgetUnder_DrainsAcrossRounds(t *testing.T) {
	h := newTestHarness(t)
	h.fake.deleteByPrefixFn = seqDeleter(10000, 10000, 7)
	res := h.call(t, "forget_under", map[string]any{"prefix": "p."})
	var out forgetUnderOutput
	structuredAs(t, res, &out)
	if out.Count != 20007 || out.Truncated {
		t.Fatalf("unexpected output: %+v", out)
	}
	// Three productive rounds plus the terminating zero round.
	if h.fake.deleteByPrefixCalls != 4 {
		t.Fatalf("deleteByPrefixCalls = %d; want 4", h.fake.deleteByPrefixCalls)
	}
}

func TestForgetUnder_NothingToDelete(t *testing.T) {
	h := newTestHarness(t)
	// Default deleteByPrefixFn (nil) returns 0 on the first call.
	res := h.call(t, "forget_under", map[string]any{"prefix": "empty."})
	if res.IsError {
		t.Fatalf("IsError = true; content=%s", contentText(res))
	}
	var out forgetUnderOutput
	structuredAs(t, res, &out)
	if out.Count != 0 || out.Truncated {
		t.Fatalf("unexpected output: %+v", out)
	}
	if h.fake.deleteByPrefixCalls != 1 {
		t.Fatalf("deleteByPrefixCalls = %d; want 1", h.fake.deleteByPrefixCalls)
	}
	if txt := contentText(res); !strings.Contains(txt, "Nothing to delete") {
		t.Fatalf("text should say nothing to delete: %q", txt)
	}
}

func TestForgetUnder_TruncatesAtRoundCap(t *testing.T) {
	h := newTestHarness(t)
	// Never returns 0 — models a namespace that keeps yielding matches.
	h.fake.deleteByPrefixFn = func(_ context.Context, _ string, _ ...client.DeleteByPrefixOption) (uint64, error) {
		return 1, nil
	}
	res := h.call(t, "forget_under", map[string]any{"prefix": "busy."})
	var out forgetUnderOutput
	structuredAs(t, res, &out)
	if !out.Truncated {
		t.Fatalf("expected truncated=true at the round cap: %+v", out)
	}
	if out.Count != uint64(forgetUnderMaxRounds) {
		t.Fatalf("Count = %d; want %d", out.Count, forgetUnderMaxRounds)
	}
	if h.fake.deleteByPrefixCalls != forgetUnderMaxRounds {
		t.Fatalf("deleteByPrefixCalls = %d; want %d", h.fake.deleteByPrefixCalls, forgetUnderMaxRounds)
	}
	if txt := contentText(res); !strings.Contains(txt, "re-run") {
		t.Fatalf("truncated text should tell the caller to re-run: %q", txt)
	}
}

func TestForgetUnder_RejectsEmptyPrefix(t *testing.T) {
	h := newTestHarness(t)
	for _, p := range []string{"", "   "} {
		res := h.callExpectError(t, "forget_under", map[string]any{"prefix": p})
		if !strings.Contains(contentText(res), "empty") {
			t.Fatalf("prefix %q: error should mention empty: %q", p, contentText(res))
		}
	}
	// The guard must short-circuit before any server call.
	if h.fake.deleteByPrefixCalls != 0 || h.fake.lastCountPrefix != "" {
		t.Fatalf("empty-prefix guard touched the server: deletes=%d count=%q", h.fake.deleteByPrefixCalls, h.fake.lastCountPrefix)
	}
}

func TestForgetUnder_RejectsWildcardPrefix(t *testing.T) {
	h := newTestHarness(t)
	res := h.callExpectError(t, "forget_under", map[string]any{"prefix": "*"})
	if !strings.Contains(contentText(res), "wildcard") {
		t.Fatalf("error should explain the wildcard refusal: %q", contentText(res))
	}
	if h.fake.deleteByPrefixCalls != 0 || h.fake.lastCountPrefix != "" {
		t.Fatalf("wildcard guard touched the server: deletes=%d count=%q", h.fake.deleteByPrefixCalls, h.fake.lastCountPrefix)
	}
}

func TestForgetUnder_DryRunCountErrorIsMapped(t *testing.T) {
	h := newTestHarness(t)
	h.fake.countByPrefixFn = func(_ context.Context, _ string) (uint64, error) {
		return 0, errors.New("boom")
	}
	res := h.callExpectError(t, "forget_under", map[string]any{"prefix": "p.", "dry_run": true})
	if !strings.Contains(contentText(res), "forget_under") {
		t.Fatalf("error should be tool-prefixed: %q", contentText(res))
	}
}

func TestForgetUnder_DeleteErrorIsMapped(t *testing.T) {
	h := newTestHarness(t)
	h.fake.deleteByPrefixFn = func(_ context.Context, _ string, _ ...client.DeleteByPrefixOption) (uint64, error) {
		return 0, errors.New("boom")
	}
	res := h.callExpectError(t, "forget_under", map[string]any{"prefix": "p."})
	if !strings.Contains(contentText(res), "forget_under") {
		t.Fatalf("error should be tool-prefixed: %q", contentText(res))
	}
}

// TestForgetUnderDescription_GuardsBlastRadius locks the framing that makes
// the tool safe to expose: dry-run-first, the empty/wildcard refusal, and
// the same "TTL handles staleness" discouragement forget carries (#528).
func TestForgetUnderDescription_GuardsBlastRadius(t *testing.T) {
	for _, want := range []string{"dry_run", "Refuses an empty", "TTL decay"} {
		if !strings.Contains(forgetUnderDescription, want) {
			t.Errorf("forgetUnderDescription missing %q: %q", want, forgetUnderDescription)
		}
	}
}
