//go:build !lantern_test_time || (!darwin && !linux)

package security

// Normal binaries always select the fixed configured-source native factory.
// There is no environment, flag, injected owner or mutable factory selection.
func newCurrentAuthorityTimeOwner() (*authorityTimeOwner, error) {
	return newNativeAuthorityTimeOwner()
}
