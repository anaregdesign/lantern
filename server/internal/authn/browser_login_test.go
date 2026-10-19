package authn

import (
	"fmt"
	"strings"
	"testing"
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
