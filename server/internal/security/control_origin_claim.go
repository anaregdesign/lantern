package security

import "time"

// Typed factual claims retain source distinctions. Token dates keep their
// original numeric presence/value, including absent/null nbf and ancient iat.
// A native session has no invented token transcript. DerivedStart is explicitly
// derived, not a claim that the provider signed a missing nbf.
type authorityCredentialClaim struct {
	Kind              string
	Token             *TokenAuthenticationEvidence
	Session           *Session
	Verification      [32]byte
	Enrollment        [32]byte
	HumanNamespace    [32]byte
	Origin, CSRF      [32]byte
	DerivedStart      AuthenticationTime
	AdmissionDeadline time.Time
}

type authorityPurposeBinding struct {
	ID                   FullChangeID
	Operation            [32]byte
	Reviewed             SemanticCut
	Actor                Identity
	IssuerConfigRevision uint64
	Lineage              uint64
}

type authorityPurposeClaim struct {
	Kind                                       string
	Binding                                    authorityPurposeBinding
	Authorization, Challenge                   [32]byte
	ReviewAt, NotBefore, ApprovedAt, ExpiresAt time.Time
	Event                                      TokenAuthenticationEvidence
	EventCommitment                            [32]byte
	Origin                                     [32]byte
	Serial                                     uint64
}

func authorityNumericTime(t time.Time) AuthenticationTime {
	return AuthenticationTime{Present: true, Numeric: true, Seconds: t.Unix(), Nanoseconds: int32(t.Nanosecond())}
}

func authorityCredentialStart(c authorityCredentialClaim) AuthenticationTime {
	if c.Token != nil {
		start := c.Token.IssuedAt
		if nbf := c.Token.NotBefore; nbf.Numeric && nbf.Time().After(start.Time()) {
			start = nbf
		}
		return start
	}
	if c.Session != nil {
		return authorityNumericTime(c.Session.CreatedAt)
	}
	return AuthenticationTime{}
}

func authorityMinTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return a
	}
	return b
}

// Original producer transcript dates may carry their original UTC offset.
// Preserve that encoding; only newly derived consume/deadline fields require
// canonical UTC. Marshalability is the same requirement as #1719 evidence.
func authorityOriginalTime(t time.Time) bool {
	if t.IsZero() {
		return false
	}
	_, err := t.MarshalJSON()
	return err == nil
}

func verifyAuthorityCredential(h authorityHistoricalHeader, operation OperationIdentity, command S1Command) bool {
	c, a := h.Credential, h.Authentication
	low, high := h.Time.utcTimes()
	if a.Class != EndUser || a.IssuerConfigRevision == 0 || h.Lineage == 0 || c.Verification == [32]byte{} ||
		!s2cCanonicalTime(c.AdmissionDeadline, false) || c.DerivedStart != authorityCredentialStart(c) ||
		!c.DerivedStart.Present || !c.DerivedStart.Numeric || low.Before(c.DerivedStart.Time()) {
		return false
	}
	var expires time.Time
	if command.Kind == S1IssueSession {
		if c.Kind != "code" || c.Token == nil || command.Session == nil || !command.Session.AuthTime.Equal(c.Token.AuthTime.Time()) || command.Session.CreatedAt.Before(c.Token.Code.ConsumedAt) || low.Before(command.Session.CreatedAt) {
			return false
		}
	}
	switch c.Kind {
	case "access", "code":
		if c.Token == nil || c.Session != nil || c.Origin != [32]byte{} || c.CSRF != [32]byte{} || a.SessionDigest != "" {
			return false
		}
		e := *c.Token
		commitment, err := e.Commitment()
		if err != nil || commitment != c.Verification || e.Identity != operation.actor || e.Generation != operation.reviewed.Generation || e.ConfigRevision != a.IssuerConfigRevision {
			return false
		}
		if e.AuthTime.Numeric && (e.AuthTime.Time().After(e.IssuedAt.Time()) || low.Before(e.AuthTime.Time())) {
			return false
		}
		if c.Kind == "access" {
			if e.Mode != "access" || a.Provenance != RFC9068Bearer || command.Kind != S1Management || c.Enrollment == [32]byte{} || c.HumanNamespace == [32]byte{} {
				return false
			}
		} else {
			if e.Mode != "code" || e.Code.Flow != "login" || a.Provenance != BrowserCode || c.Enrollment != [32]byte{} || c.HumanNamespace != [32]byte{} {
				return false
			}
			for _, stamp := range []time.Time{e.Code.CreatedAt, e.Code.ConsumedAt, e.Code.ExpiresAt} {
				if !authorityOriginalTime(stamp) {
					return false
				}
			}
			if e.Code.ConsumedAt.Before(e.Code.CreatedAt) || !e.Code.ConsumedAt.Before(e.Code.ExpiresAt) || low.Before(e.Code.ConsumedAt) {
				return false
			}
		}
		expires = e.ExpiresAt.Time()
	case "session":
		if c.Token != nil || c.Session == nil || c.Enrollment != [32]byte{} || c.HumanNamespace != [32]byte{} || c.Origin == [32]byte{} || c.CSRF == [32]byte{} || a.Provenance != BrowserCode {
			return false
		}
		s := *c.Session
		if s.Revoked || s.Identity != operation.actor || s.IssuerConfigRevision != a.IssuerConfigRevision || s.Digest != a.SessionDigest || !validHexDigest(s.Digest) || !validHexDigest(s.CSRFDigest) ||
			!s2cCanonicalTime(s.CreatedAt, false) || !s2cCanonicalTime(s.ExpiresAt, false) || !s2cCanonicalTime(s.AuthTime, true) ||
			!s.CreatedAt.Before(s.ExpiresAt) || c.Verification != s2cHash("current-native-session-v2", s) {
			return false
		}
		expires = s.ExpiresAt
	default:
		return false
	}
	return h.CredentialDeadline == authorityMinTime(expires, c.AdmissionDeadline) && s2cCanonicalTime(h.CredentialDeadline, false) && high.Before(h.CredentialDeadline)
}

func verifyAuthorityPurpose(h authorityHistoricalHeader, operation OperationIdentity) bool {
	p := h.Purpose
	if p == nil {
		return h.PurposeEvidence == [32]byte{} && h.PurposeBinding == [32]byte{} && h.PurposeDeadline == (time.Time{})
	}
	expected := authorityPurposeBinding{h.ID, h.OperationDigest, operation.reviewed, operation.actor, h.Authentication.IssuerConfigRevision, h.Lineage}
	if p.Kind != "full-s1-operation" || p.Binding != expected || p.Authorization == [32]byte{} || p.Challenge == [32]byte{} || p.Origin != h.OriginDigest || p.Serial != h.Serial {
		return false
	}
	e := p.Event
	commitment, err := e.Commitment()
	if err != nil || commitment != p.EventCommitment || e.Mode != "code" || e.Code.Flow != "operation" || e.Code.AuthorizationID != p.Authorization ||
		e.Identity != operation.actor || e.ConfigRevision != h.Authentication.IssuerConfigRevision || e.Generation != operation.reviewed.Generation || !e.AuthTime.Present || !e.AuthTime.Numeric {
		return false
	}
	for _, stamp := range []time.Time{p.ReviewAt, p.NotBefore, p.ApprovedAt, p.ExpiresAt, h.PurposeDeadline} {
		if !s2cCanonicalTime(stamp, false) {
			return false
		}
	}
	for _, stamp := range []time.Time{e.Code.CreatedAt, e.Code.ConsumedAt, e.Code.ExpiresAt} {
		if !authorityOriginalTime(stamp) {
			return false
		}
	}
	low, high := h.Time.utcTimes()
	return p.ReviewAt.Before(p.NotBefore) && !e.Code.CreatedAt.Before(p.NotBefore) && !e.Code.ConsumedAt.Before(e.Code.CreatedAt) && e.Code.ConsumedAt.Before(e.Code.ExpiresAt) &&
		!p.ApprovedAt.Before(e.Code.ConsumedAt) && !e.AuthTime.Time().Before(p.NotBefore) && !p.ApprovedAt.Before(e.AuthTime.Time()) && !e.AuthTime.Time().After(e.IssuedAt.Time()) &&
		!low.Before(p.ApprovedAt) && !low.Before(authorityCredentialStart(authorityCredentialClaim{Token: &e}).Time()) &&
		!p.ExpiresAt.After(p.ReviewAt.Add(10*time.Minute)) && !p.ExpiresAt.After(e.ExpiresAt.Time()) &&
		h.PurposeDeadline == p.ExpiresAt && high.Before(h.PurposeDeadline) && h.PurposeBinding == s1PurposeBinding(h.ID, operation) && h.PurposeEvidence == s2cHash("current-purpose-evidence-v2", p)
}
