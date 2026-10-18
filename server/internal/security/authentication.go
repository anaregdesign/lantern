package security

import (
	"context"
	"time"
)

// Provenance identifies the verified credential protocol, independently of
// whether that credential represents an end-user or an OAuth client.
type CredentialProvenance string

const (
	BrowserCode                CredentialProvenance = "browser_code"
	RFC9068Bearer              CredentialProvenance = "rfc9068_bearer"
	NativeMachine              CredentialProvenance = "native_machine"
	NativeProviderLogin        CredentialProvenance = "native_provider_login"
	NativeEndUserSession       CredentialProvenance = "native_end_user_session"
	NativeProviderRegistration CredentialProvenance = "native_provider_registration"
)

type ActorClass string

const (
	EndUser         ActorClass = "end_user"
	MachineActor    ActorClass = "machine"
	UnresolvedActor ActorClass = "unresolved"
)

// Authentication is trusted verification input, never a request/header claim.
// RFC9068 classification uses exact durable human enrollment plus the current
// issuer's qualified noncollision/nonimpersonation issuance contract.
type Authentication struct {
	Provenance           CredentialProvenance
	Class                ActorClass
	IssuerConfigRevision uint64
	SessionDigest        string
}

func (s *Snapshot) BearerActor(identity Identity) ActorClass {
	if identity.Kind == MachinePrincipal {
		return MachineActor
	}
	issuer, known := s.Issuer(identity.Issuer)
	if known && issuer.HumanSubjectNamespaceQualified && s.HumanIdentity(identity) {
		return EndUser
	}
	return UnresolvedActor
}

func (a *Admission) Authentication() Authentication { return a.authentication }
func (a *Admission) CheckManagement(ctx context.Context, now time.Time) error {
	return a.checkHumanAdmission(ctx, now)
}

// A privileged issuer probe requires the same captured human credential.
func (a *Admission) CheckIssuerProbe(ctx context.Context, now time.Time) error {
	return a.checkHumanAdmission(ctx, now)
}
func (a *Admission) checkHumanAdmission(ctx context.Context, _ time.Time) error {
	if a == nil || a.current == nil {
		return ErrAuthorityUnavailable
	}
	if a.current.credential.browserReadOnly || a.current.credential.authentication.Class != EndUser {
		return ErrPermissionDenied
	}
	return a.current.check(ctx)
}
func (s *Snapshot) checkHumanAuthentication(identity Identity, authentication Authentication, now time.Time) error {
	if identity.Kind != OIDCPrincipal || authentication.Class != EndUser {
		return ErrPermissionDenied
	}
	issuer, known := s.Issuer(identity.Issuer)
	if !known || !issuer.Enabled || issuer.Deleted || issuer.ConfigRevision == 0 || issuer.ConfigRevision != authentication.IssuerConfigRevision {
		return ErrPermissionDenied
	}
	switch authentication.Provenance {
	case RFC9068Bearer:
		if s.BearerActor(identity) != EndUser || authentication.SessionDigest != "" {
			return ErrPermissionDenied
		}
	case BrowserCode:
		if authentication.SessionDigest != "" {
			session, known := s.Session(authentication.SessionDigest)
			_, _, active := s.SessionAccess(authentication.SessionDigest, now)
			if !known || !active || session.Identity != identity {
				return ErrPermissionDenied
			}
		}
	default:
		return ErrPermissionDenied
	}
	return nil
}
