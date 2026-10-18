//go:build !darwin && !linux

package security

// Only the admitted Darwin and Linux native profiles can construct this private
// owner. Other platforms retain OFF and native TLS, but cannot host OIDC.
func newNativeAuthorityTimeOwner() (*authorityTimeOwner, error) {
	return nil, errAuthorityTime
}
