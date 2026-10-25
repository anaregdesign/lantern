package authn

import (
	"testing"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestPurposeAffinityRequiresSameProcess(t *testing.T) {
	id := [32]byte{1}
	for _, workflow := range []*Workflow{nil, {}, {current: &security.CurrentAuthority{}}} {
		if workflow.AttemptAffinity(id) != "" {
			t.Fatal("missing process incarnation invented callback affinity")
		}
	}
	w := &Workflow{current: &security.CurrentAuthority{}, attemptPrefix: "member.process"}
	if w.AttemptAffinity([32]byte{}) != "" {
		t.Fatal("empty attempt accepted")
	}
	for _, affinity := range []string{"", "another-process", w.AttemptAffinity([32]byte{2})} {
		if _, err := w.ReadCurrentAuthorization(t.Context(), nil, id, affinity); err == nil {
			t.Fatal("foreign process/attempt affinity reached purpose owner")
		}
	}
}

func TestPurposeDiagnosticPreservesCollapsedRejection(t *testing.T) {
	capture := decisiontrace.Start(8)
	if capture == nil {
		t.Fatal("capture already owned")
	}
	defer capture.Stop()
	w := &Workflow{current: &security.CurrentAuthority{}}
	if _, err := w.StartAuthorization(t.Context(), [32]byte{99}, "private-affinity"); err != ErrUnauthenticated {
		t.Fatal("diagnostic changed purpose rejection", err)
	}
	capture.Stop()
	events, dropped := capture.Drain()
	if dropped != 0 || len(events) != 2 || events[0].Stage != "purpose_not_before" || events[0].Reason != "owner" || events[1].Stage != "purpose_start" || events[1].Reason != "not_before" {
		t.Fatal("missing lower refusal before collapsed workflow result", events, dropped)
	}
}
