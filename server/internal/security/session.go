package security

import "time"

// Session contains only the digest of an opaque random cookie. Fixed expiry
// avoids a synchronous security write on every business data request.
type Session struct {
	Digest    string    `json:"digest"`
	Identity  Identity  `json:"identity"`
	CreatedAt time.Time `json:"created_at"`
	ExpiresAt time.Time `json:"expires_at"`
	AuthTime  time.Time `json:"auth_time"`
	Revoked   bool      `json:"revoked"`
}
