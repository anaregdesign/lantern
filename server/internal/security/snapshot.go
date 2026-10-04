package security

import (
	"encoding/json"
	"time"
)

type principalAccess struct {
	state  PrincipalState
	access *Access
}

// Snapshot owns a validated image and its compiled indexes. All fields are
// private so a caller cannot mutate an installed revision after publication.
type Snapshot struct {
	image      []byte
	roleBytes  []byte
	limits     PolicyLimits
	accessSets map[string]*Access
	policy     *CompiledPolicy
	issuers    map[string]Issuer
	principals map[Identity]principalAccess
	sessions   map[string]Session
	machines   map[string]MachineCredential
}

func (s *Snapshot) AccessFor(identity Identity) (*Access, bool) {
	if s == nil {
		return nil, false
	}
	principal, known := s.principals[identity]
	if !known || principal.state != Active {
		return nil, false
	}
	if identity.Kind == OIDCPrincipal && !s.issuers[identity.Issuer].Enabled {
		return nil, false
	}
	return principal.access, true
}

func (s *Snapshot) Issuer(exactURL string) (Issuer, bool) {
	if s == nil {
		return Issuer{}, false
	}
	issuer, known := s.issuers[exactURL]
	issuer.Algorithms = append([]string(nil), issuer.Algorithms...)
	return issuer, known
}

func (s *Snapshot) SessionAccess(digest string, now time.Time) (*Access, time.Time, bool) {
	if s == nil {
		return nil, time.Time{}, false
	}
	session, known := s.sessions[digest]
	issuer, issuerKnown := s.issuers[session.Identity.Issuer]
	if !known || !issuerKnown || session.IssuerConfigRevision != issuer.ConfigRevision || session.Revoked || now.Before(session.CreatedAt) || !now.Before(session.ExpiresAt) {
		return nil, time.Time{}, false
	}
	access, active := s.AccessFor(session.Identity)
	return access, session.AuthTime, active
}

// Image returns a detached copy for management read/modify/CAS. A snapshot's
// encoded bytes never escape to callers that can mutate them.
func (s *Snapshot) Image() Image {
	var image Image
	if s != nil {
		_ = json.Unmarshal(s.image, &image)
	}
	return image
}

// Session returns immutable digest metadata, never the raw cookie or IdP token.
func (s *Snapshot) Session(digest string) (Session, bool) {
	if s == nil {
		return Session{}, false
	}
	session, known := s.sessions[digest]
	return session, known
}
