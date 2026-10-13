package security

import (
	"encoding/base64"
	"time"
)

// Factual native-session image for the admission I/O guard and lookup benchmark.
// Actual provider-to-session proof is exercised by the root native wire gate.
func nativeSessionBenchmarkImage() (Image, Session, string) {
	raw := NativeCredentialPrefix + base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	digest, _ := NativeCredentialDigest(raw)
	now := time.Now().UTC().Round(0).Add(-time.Minute)
	c := NativeClient{ID: "mobile", Kind: GoogleIDToken, ClientID: "backend", ServiceOrigin: "https://lantern.example"}
	id := Identity{Kind: OIDCPrincipal, Issuer: GoogleIssuer, Subject: "native-hot-path"}
	s := Session{Profile: NativeEndUserProfile, ClientProfileID: c.ID, ServiceOrigin: c.ServiceOrigin, IssuerConfigRevision: 1, Digest: digest, Identity: id, CreatedAt: now, ExpiresAt: now.Add(MaxSessionLifetime)}
	i := s1Image()
	i.Issuers = append(i.Issuers, Issuer{URL: GoogleIssuer, Enabled: true, ConfigRevision: 1, Algorithms: []string{"RS256"}, NativeClients: []NativeClient{c}})
	i.Principals = append(i.Principals, Principal{Identity: id, State: Active, Assignments: []RoleAssignment{{RoleID: "reader"}}})
	i.Sessions = append(i.Sessions, s)
	return i, s, raw
}
