package mcp

import (
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/mcp/internal/identity"
)

func TestPostNote(t *testing.T) {
	self := identity.Resolve()

	t.Run("writes the note and links note→resources plus author→note", func(t *testing.T) {
		h := newContextHarness(t)
		res := h.call(t, "post_note", map[string]any{
			"text":     "build broken on main",
			"severity": "blocker",
			"links":    []string{"repo.lantern.ci", "branch.main"},
			"ttl":      "turn",
		})
		var out postNoteOutput
		structuredAs(t, res, &out)
		if out.NoteID == "" || out.Author != self || out.Severity != "blocker" {
			t.Fatalf("note: %+v", out)
		}
		if !strings.HasPrefix(h.fake.lastPutKey, noteKeyPrefix) {
			t.Fatalf("note stored at %q", h.fake.lastPutKey)
		}
		var rec noteRecord
		payload, _ := h.fake.lastPutValue.(string)
		if err := decodePayload(payload, &rec); err != nil || rec.Text != "build broken on main" || rec.Author != self {
			t.Fatalf("payload: %+v err=%v", rec, err)
		}
		// 2 note→resource links + 1 author→note edge.
		if len(h.fake.lastAddEdges) != 3 {
			t.Fatalf("edges = %d, want 3: %+v", len(h.fake.lastAddEdges), h.fake.lastAddEdges)
		}
		noteKey := h.fake.lastPutKey
		var linked, authored int
		for _, e := range h.fake.lastAddEdges {
			switch {
			case e.Tail == noteKey:
				linked++
			case e.Tail == agentKey(self) && e.Head == noteKey:
				authored++
			}
		}
		if linked != 2 || authored != 1 {
			t.Fatalf("edge shape: linked=%d authored=%d", linked, authored)
		}
	})

	t.Run("severity defaults to info and rejects unknown values", func(t *testing.T) {
		h := newContextHarness(t)
		res := h.call(t, "post_note", map[string]any{"text": "x", "ttl": "turn"})
		var out postNoteOutput
		structuredAs(t, res, &out)
		if out.Severity != "info" {
			t.Fatalf("default severity: %+v", out)
		}
		h.callExpectError(t, "post_note", map[string]any{"text": "x", "ttl": "turn", "severity": "loud"})
	})

	t.Run("empty text and bad ttl rejected", func(t *testing.T) {
		h := newContextHarness(t)
		h.callExpectError(t, "post_note", map[string]any{"text": " ", "ttl": "turn"})
		h.callExpectError(t, "post_note", map[string]any{"text": "x", "ttl": "forever"})
	})
}
