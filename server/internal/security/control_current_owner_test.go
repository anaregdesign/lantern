package security

import (
	"testing"
)

func TestCurrentAuthorityOwnerRejectsInjectedTimeBeforeIO(t *testing.T) {
	clock, _ := fakeAuthorityTimeOwner(t)
	if owner, err := openCurrentAuthorityOwner(t.Context(), s3aConfig{timeOwner: clock}, "/unused-origin-key", "https://admin.example", true, s3aFloors{}); owner != nil || err == nil {
		t.Fatal("current native constructor accepted injected time")
	}
	if owner, err := openCurrentAuthorityOwner(t.Context(), s3aConfig{}, "/unused-origin-key", "https://admin.example", true, s3aFloors{}); owner != nil || err == nil {
		t.Fatal("missing independent origin bootstrap")
	}
}
