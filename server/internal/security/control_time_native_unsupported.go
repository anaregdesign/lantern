//go:build !darwin

package security

// The initial conditional native profile is Darwin/arm64 only. This private
// factory does not change the existing public runtime on other platforms.
func newNativeAuthorityTimeOwner() (*authorityTimeOwner, error) {
	return nil, errAuthorityTime
}
