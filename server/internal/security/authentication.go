package security

import (
	"context"
	"time"
)

// Provenance identifies the verified credential protocol, independently of
// whether that credential represents an end-user or an OAuth client.
type CredentialProvenance string

const (
	BrowserCode   CredentialProvenance = "browser_code"
	RFC9068Bearer CredentialProvenance = "rfc9068_bearer"
	NativeMachine CredentialProvenance = "native_machine"
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

func (a *Admission) WithAuthentication(authentication Authentication) *Admission {
	copy := *a
	copy.authentication = authentication
	return &copy
}
func (a *Admission) Authentication() Authentication { return a.authentication }

// CheckManagement rechecks the captured credential at the commit boundary.
// Fence callbacks must not perform I/O or reenter the Store; the writer's
// current authority fence follows the Store -> authority lock order.
func (a *Admission) CheckManagement(ctx context.Context, current *Revision, now time.Time) error {
	return a.checkHumanAdmission(ctx, current, now)
}

// CheckIssuerProbe is the conservative policy for a privileged outbound
// metadata probe. Machine read/status eligibility does not qualify this probe.
func (a *Admission) CheckIssuerProbe(ctx context.Context, current *Revision, now time.Time) error {
	return a.checkHumanAdmission(ctx, current, now)
}

func (a *Admission) checkHumanAdmission(ctx context.Context, current *Revision, now time.Time) error {
	if a == nil || current == nil || a.revision.digest != current.digest {
		return ErrAuthorityUnavailable
	}
	if err := a.Check(ctx, now); err != nil {
		return err
	}
	return current.snapshot.checkHumanAuthentication(a.identity, a.authentication, now)
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
