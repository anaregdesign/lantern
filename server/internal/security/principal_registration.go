package security

import "github.com/anaregdesign/lantern/server/internal/authz"

// PrincipalRegistration is the immutable business content of one reviewed sys
// operation. Only the restricted registration workflow allocates UserUID; this
// structure alone is not provider evidence or authorization to Apply it.
type PrincipalRegistration struct {
	UserUID              string
	ClientProfileID      string
	IssuerConfigRevision uint64
}

func (r *PrincipalRegistration) valid() bool {
	return r != nil && authz.ValidPersonalUID(r.UserUID) && validRoleID(r.ClientProfileID) && r.IssuerConfigRevision != 0
}

// applyPrincipalRegistration changes one detached image. Final compilation and
// the existing sys commit atomically cover UID uniqueness, assignment and human
// enrollment; session issuance and all data operations remain separate.
func applyPrincipalRegistration(image *Image, actor Identity, registration *PrincipalRegistration) error {
	if actor.Kind != OIDCPrincipal || !actor.valid() || !registration.valid() {
		return ErrS1Contract
	}
	var issuer Issuer
	for _, candidate := range image.Issuers {
		if candidate.URL == actor.Issuer {
			issuer = candidate
			break
		}
	}
	if !issuer.Enabled || issuer.Deleted || issuer.ConfigRevision != registration.IssuerConfigRevision {
		return ErrPermissionDenied
	}
	if _, registered := issuer.NativeClient(registration.ClientProfileID); !registered {
		return ErrPermissionDenied
	}
	index := -1
	for n, principal := range image.Principals {
		if principal.Identity == actor {
			index = n
		}
		if principal.UserUID == registration.UserUID && principal.Identity != actor {
			return ErrChangeConflict
		}
	}
	if index >= 0 {
		principal := &image.Principals[index]
		if principal.State != Active {
			return ErrPermissionDenied
		}
		if principal.UserUID != "" {
			if principal.UserUID != registration.UserUID {
				return ErrChangeConflict
			}
			// Idempotent confirmation never repairs a withdrawn Role assignment.
			principal.HumanIssuerConfigRevision = issuer.ConfigRevision
			return nil
		}
	}
	if !image.RegistrationPolicy.permits(actor.Issuer, registration.ClientProfileID) {
		return ErrPermissionDenied
	}
	found := false
	for _, role := range image.Roles {
		if role.ID == PersonalUserRoleID {
			found = validInitialRegistrationRole(role)
			break
		}
	}
	if !found {
		return ErrPermissionDenied
	}
	if index < 0 {
		image.Principals = append(image.Principals, Principal{Identity: actor, State: Active})
		index = len(image.Principals) - 1
	}
	principal := &image.Principals[index]
	principal.UserUID = registration.UserUID
	principal.HumanIssuerConfigRevision = issuer.ConfigRevision
	for _, assignment := range principal.Assignments {
		if assignment.RoleID == PersonalUserRoleID {
			return nil
		}
	}
	principal.Assignments = append(principal.Assignments, RoleAssignment{RoleID: PersonalUserRoleID})
	return nil
}
