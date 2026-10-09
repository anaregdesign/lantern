package provider

import (
	"fmt"
	"net/http"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/oidc"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestCurrentCredentialProducerBoundary(t *testing.T) {
	r := &SecurityRuntime{mode: "oidc", keys: oidc.NewKeyCache(nil)}
	for _, values := range [][]string{nil, {"Bearer "}, {"Bearer a b"}, {"Bearer a", "Bearer b"}, {"Basic a"}, {"Bearer " + security.MachineTokenPrefix + "not-human"}} {
		if _, err := r.CurrentBearerProducer(http.Header{"Authorization": values}); err == nil {
			t.Fatal("ambiguous or nonhuman credential accepted")
		}
	}
	producer, err := r.CurrentBearerProducer(http.Header{"Authorization": {"Bearer confidential-placeholder"}})
	if err != nil {
		t.Fatal(err)
	}
	if fmt.Sprintf("%v", producer) != "[redacted current credential producer]" || fmt.Sprintf("%#v", producer) != "[redacted current credential producer]" {
		t.Fatal("producer revealed credential")
	}
	if _, err := producer.VerifyCurrentCredential(t.Context(), security.CurrentCredentialView{}); err == nil {
		t.Fatal("zero installed S1 view accepted")
	}
	if _, err := r.CurrentBrowserProducer(nil); err == nil {
		t.Fatal("missing native browser request")
	}
	if _, err := (*SecurityRuntime)(nil).CurrentCodeProducer(oidc.LoginCompletion{}, oidc.CodeTokens{}); err == nil {
		t.Fatal("missing native Code producer")
	}
}
