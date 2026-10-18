package service

import (
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
)

func TestSessionViewUserIDPresenceWithoutRoles(t *testing.T) {
	image := securityImageFixture()
	id := image.Principals[1].Identity
	snapshot, err := security.CompileImage(image, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	if sessionUserID(snapshot, id) != nil {
		t.Fatal("UID-less Principal acquired user_id")
	}
	const uid = "11111111111111111111111111111111"
	image.Principals[1].UserUID = uid
	snapshot, err = security.CompileImage(image, security.DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	got := sessionUserID(snapshot, id)
	if got == nil || *got != uid {
		t.Fatal("user_id required a Role")
	}
}
