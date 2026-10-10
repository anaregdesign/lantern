//go:build !darwin && !linux

package security

// Only the admitted Darwin and Linux native profiles can construct this private
// owner. This does not change the existing public runtime on other platforms.
func newNativeAuthorityTimeOwner() (*authorityTimeOwner, error) {
	return nil, errAuthorityTime
}
