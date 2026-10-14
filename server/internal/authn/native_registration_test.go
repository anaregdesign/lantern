package authn

import (
	"fmt"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestNativeRegistrationUnavailableWithoutCurrentComposition(t *testing.T) {
	for _, w := range []*Workflow{nil, {}, {current: &security.CurrentAuthority{}}} {
		if _, err := w.BeginNativeRegistration(t.Context(), security.GoogleIssuer, "android", nil); err != ErrUnavailable {
			t.Fatal(err)
		}
		if _, err := w.PrepareNativeRegistration(t.Context(), NativeCompletionInput{}, nil); err != ErrUnavailable {
			t.Fatal(err)
		}
		if _, err := w.ApplyNativeRegistration(t.Context(), NativeRegistrationRequest{}); err != ErrUnavailable {
			t.Fatal(err)
		}
		if _, err := w.GetNativeRegistrationStatus(t.Context(), NativeRegistrationRequest{}); err != ErrUnavailable {
			t.Fatal(err)
		}
		w.Close()
	}
}

func TestNativeRegistrationRedactsHandlesAndCredentials(t *testing.T) {
	for _, value := range []any{NativeRegistrationPrepared{Proof: "private-proof"}, NativeRegistrationRequest{Proof: "private-proof"}, NativeRegistrationResult{Session: &NativeSessionResult{Credential: "private-credential"}}} {
		if strings.Contains(fmt.Sprintf("%v %#v", value, value), "private-") {
			t.Fatal("credential escaped formatting")
		}
	}
}
