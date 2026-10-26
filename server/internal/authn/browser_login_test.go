package authn

import (
	"fmt"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/decisiontrace"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCallbackValuesRedactSecrets(t *testing.T) {
	for _, value := range []any{
		CallbackInput{Code: "secret-code", State: "secret-state", Transaction: "secret-cookie"},
		CallbackResult{Session: "secret-session", CSRF: "secret-csrf"},
	} {
		if strings.Contains(fmt.Sprintf("%v %#v", value, value), "secret-") {
			t.Fatal("callback value exposed raw credentials")
		}
	}
}

func TestBeginLoginDiagnosticPreservesRejectionAndRedactsInput(t *testing.T) {
	capture := decisiontrace.Start(8)
	if capture == nil {
		t.Fatal("capture already owned")
	}
	defer capture.Stop()
	w := &Workflow{current: &security.CurrentAuthority{}}
	if _, err := w.BeginLogin(t.Context(), LoginRequest{Issuer: "private-issuer-input", ReturnPath: "/private-return"}); err != ErrUnauthenticated {
		t.Fatal("diagnostic changed login rejection", err)
	}
	capture.Stop()
	events, dropped := capture.Drain()
	if dropped != 0 || len(events) != 1 || events[0].Kind != "browser_decision" || events[0].Stage != "login_start" || events[0].Reason != "issuer" {
		t.Fatal("missing first login refusal", events, dropped)
	}
	if strings.Contains(fmt.Sprint(events), "private-") {
		t.Fatal("diagnostic exposed login input")
	}
}
