package security

import (
	"testing"
	"time"
)

func TestAdmissionRequiresInstalledCurrentOwner(t *testing.T) {
	for _, a := range []*Admission{nil, {}} {
		if a.Check(t.Context(), time.Now()) == nil || a.Snapshot() != nil || a.ScopeBinding() != [32]byte{} {
			t.Fatal("unowned admission established authority")
		}
		if a.WithBrowserProof("unverified") != a {
			t.Fatal("unowned admission promoted to browser")
		}
	}
}
