package security

import "math"

// applyManagementChanges edits a detached typed image. Final compilation checks
// references and last-admin access after the entire batch, allowing explicit
// atomic membership removal plus Role deletion in either request order.
func applyManagementChanges(image *Image, changes []Change) error {
	for _, change := range changes {
		switch change.Kind {
		case PutIssuer, DisableIssuer, DeleteIssuer:
			url := change.IssuerURL
			if change.Kind == PutIssuer {
				url = change.Issuer.URL
			}
			index := -1
			for i, issuer := range image.Issuers {
				if issuer.URL == url {
					index = i
					break
				}
			}
			if index < 0 && change.Kind != PutIssuer {
				return ErrInvalidImage
			}
			revision := uint64(1)
			if index >= 0 {
				old := image.Issuers[index]
				if old.EnvOwned {
					return ErrBootstrapLocked
				}
				if old.ConfigRevision == math.MaxUint64 {
					return ErrInvalidImage
				}
				revision = old.ConfigRevision + 1
			}
			if change.Kind == PutIssuer {
				issuer := *change.Issuer
				if change.PreserveSecret && index >= 0 {
					issuer.SecretRef = image.Issuers[index].SecretRef
				}
				issuer.ConfigRevision = revision
				issuer.Algorithms = append([]string(nil), issuer.Algorithms...)
				if index < 0 {
					image.Issuers = append(image.Issuers, issuer)
				} else {
					image.Issuers[index] = issuer
				}
			} else {
				image.Issuers[index].Enabled = false
				image.Issuers[index].ConfigRevision = revision
				if change.Kind == DeleteIssuer {
					image.Issuers[index].Deleted = true
				}
				for i := range image.Sessions {
					if image.Sessions[i].Identity.Issuer == url {
						image.Sessions[i].Revoked = true
					}
				}
			}
		case PutRole, DeleteRole:
			id := change.RoleID
			if change.Kind == PutRole {
				id = change.Role.ID
			}
			index := -1
			for i, role := range image.Roles {
				if role.ID == id {
					index = i
					break
				}
			}
			if change.Kind == PutRole {
				role := *change.Role
				if index < 0 {
					image.Roles = append(image.Roles, role)
				} else {
					image.Roles[index] = role
				}
			} else {
				if index < 0 {
					return ErrUnknownRole
				}
				image.Roles = append(image.Roles[:index], image.Roles[index+1:]...)
			}
		case PutPrincipal, DeletePrincipal, PutAssignment, DeleteAssignment, RevokeSessions:
			identity := *change.Identity
			index := -1
			for i, principal := range image.Principals {
				if principal.Identity == identity {
					index = i
					break
				}
			}
			if index < 0 && change.Kind != PutPrincipal {
				return ErrInvalidImage
			}
			switch change.Kind {
			case PutPrincipal:
				if index < 0 {
					image.Principals = append(image.Principals, Principal{Identity: identity, State: change.State})
				} else {
					if image.Principals[index].State == Deleted {
						return ErrInvalidImage
					}
					image.Principals[index].State = change.State
				}
				if change.State == Suspended {
					revokePrincipalSessions(image, identity)
				}
			case DeletePrincipal:
				image.Principals[index].State = Deleted
				image.Principals[index].Assignments = nil
				revokePrincipalSessions(image, identity)
			case PutAssignment, DeleteAssignment:
				if image.Principals[index].State == Deleted || change.RoleID == "cluster_replica" {
					return ErrInvalidPolicy
				}
				assignments := image.Principals[index].Assignments
				found := -1
				for i, assignment := range assignments {
					if assignment.RoleID == change.RoleID {
						found = i
						break
					}
				}
				if change.Kind == PutAssignment {
					if found < 0 {
						image.Principals[index].Assignments = append(assignments, RoleAssignment{RoleID: change.RoleID})
					}
				} else if found >= 0 {
					if assignments[found].EnvOwned {
						return ErrBootstrapLocked
					}
					image.Principals[index].Assignments = append(assignments[:found], assignments[found+1:]...)
				}
			case RevokeSessions:
				revokePrincipalSessions(image, identity)
			}
		default:
			return ErrInvalidImage
		}
	}
	return nil
}

func revokePrincipalSessions(image *Image, identity Identity) {
	for i := range image.Sessions {
		if image.Sessions[i].Identity == identity {
			image.Sessions[i].Revoked = true
		}
	}
}
