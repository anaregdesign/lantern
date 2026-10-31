package securitytransport

import (
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSecurityAuthenticationUnavailableAndOff(t *testing.T) {
	off := NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter("off", BrowserConfig{}, nil), Now: time.Now})
	ctx, err := off.AuthenticateBearer(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, known := security.AdmissionFromContext(ctx); known {
		t.Fatal("OFF invented authenticated authority")
	}
	oidc := NewAuthenticationAdapter(AuthenticationOptions{Credentials: NewCredentialAdapter("oidc", BrowserConfig{}, nil), Now: time.Now})
	for _, r := range []*AuthenticationAdapter{nil, oidc} {
		if _, err := r.AuthenticateBearer(t.Context(), nil); connect.CodeOf(err) != connect.CodeUnavailable {
			t.Fatal("unowned OIDC became OFF", err)
		}
	}
}
