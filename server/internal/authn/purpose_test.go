package authn

import (
	"testing"

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
