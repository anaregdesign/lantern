package authn

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestOperationAuthenticationRequiresOpaqueCodeEvidence(t *testing.T) {
	// No context, native session, admission or caller-declared authentication
	// facts can replace a genuine operation Code event in this API.
	w := &Workflow{}
	if err := w.completeOperationAuthentication([32]byte{1}, oidc.VerifiedIdentity{}, nil); !errors.Is(err, security.ErrOperationAuthorization) {
		t.Fatal("zero verifier result reached the purpose owner", err)
	}
}

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
