package service

import (
	"testing"
	"time"

	"github.com/anaregdesign/lantern/server/internal/authn"
	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSessionViewUserIDPresenceWithoutRoles(t *testing.T) {
	h, _, contexts := securityAPIFixture(t)
	read := func() *security.Admission {
		t.Helper()
		a, _ := security.AdmissionFromContext(contexts("reader", time.Time{}))
		return a
	}
	legacy := encodeSessionView(authn.SessionView{Admission: read()})
	if legacy.UserId != nil {
		t.Fatal("UID-less Principal acquired a user_id")
	}
	cut, _ := h.store.Current()
	image := cut.Snapshot().Image()
	const id = "11111111111111111111111111111111"
	for i := range image.Principals {
		if image.Principals[i].Identity.Subject == "reader" {
			image.Principals[i].UserUID = id
		}
	}
	if _, err := h.store.Commit(t.Context(), cut.Sequence(), [16]byte{2}, image); err != nil {
		t.Fatal(err)
	}
	for _, browser := range []bool{false, true} {
		a := read()
		if browser {
			a = a.WithBrowserProof("csrf")
		}
		response := encodeSessionView(authn.SessionView{Admission: a})
		if response.UserId == nil || *response.UserId != id || len(response.Roles) != 0 || response.RecentAuthentication {
			t.Fatal("self ID needs a Role or fabricated authentication freshness", response)
		}
		if (response.CsrfToken != "") != browser {
			t.Fatal("browser proof boundary changed")
		}
	}
}
