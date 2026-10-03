package security

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"
)

type PrincipalKind string

const (
	OIDCPrincipal    PrincipalKind = "oidc"
	MachinePrincipal PrincipalKind = "machine"
)

// Identity is exact and comparable. Email, display names and group claims
// cannot link identities or assign permissions.
type Identity struct {
	Kind        PrincipalKind `json:"kind"`
	Issuer      string        `json:"issuer,omitempty"`
	Subject     string        `json:"subject,omitempty"`
	MachineName string        `json:"machine_name,omitempty"`
}

func (i Identity) valid() bool {
	switch i.Kind {
	case OIDCPrincipal:
		return i.MachineName == "" && validIssuerURL(i.Issuer) && validSubject(i.Subject)
	case MachinePrincipal:
		return i.Issuer == "" && i.Subject == "" && validRoleID(i.MachineName)
	default:
		return false
	}
}

func validSubject(subject string) bool {
	if subject == "" || len(subject) > 255 {
		return false
	}
	// OIDC Core defines sub as at most 255 ASCII characters. No trimming or
	// normalization: even case and whitespace remain part of exact identity.
	for i := range len(subject) {
		if subject[i] >= 128 {
			return false
		}
	}
	return true
}

func validHTTPSURL(raw string) (*url.URL, bool) {
	if raw == "" || len(raw) > 2048 || !utf8.ValidString(raw) || strings.Contains(raw, "#") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" ||
		u.User != nil || u.Fragment != "" || u.Opaque != "" {
		return nil, false
	}
	if port := u.Port(); port != "" {
		n, err := strconv.Atoi(port)
		if err != nil || n < 1 || n > 65535 {
			return nil, false
		}
	}
	return u, true
}

func validIssuerURL(raw string) bool {
	u, valid := validHTTPSURL(raw)
	return valid && u.RawQuery == "" && !u.ForceQuery
}
