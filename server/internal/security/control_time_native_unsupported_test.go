//go:build !darwin && !linux

package security

import "testing"

func TestAuthorityTimeUnsupportedNativeProfile(t *testing.T) {
	if owner, err := newNativeAuthorityTimeOwner(); owner != nil || err == nil {
		t.Fatal("unsupported profile admitted")
	}
}
