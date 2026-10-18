package security

import "reflect"

// sameEnvOwned preserves independently provisioned issuer, machine and Role locks.
func sameEnvOwned(old, next Image) bool {
	if !reflect.DeepEqual(old.MachineCredentials, next.MachineCredentials) {
		return false
	}
	locked := func(image Image) (map[Identity]map[string]bool, map[string][]PermissionRule) {
		assignments := make(map[Identity]map[string]bool)
		roles := make(map[string][]PermissionRule)
		for _, principal := range image.Principals {
			for _, assignment := range principal.Assignments {
				if !assignment.EnvOwned {
					continue
				}
				if assignments[principal.Identity] == nil {
					assignments[principal.Identity] = make(map[string]bool)
				}
				assignments[principal.Identity][assignment.RoleID] = true
			}
		}
		for _, role := range image.Roles {
			for _, ids := range assignments {
				if ids[role.ID] {
					roles[role.ID] = role.Rules
				}
			}
		}
		return assignments, roles
	}
	envIssuers := func(image Image) map[string]Issuer {
		result := make(map[string]Issuer)
		for _, issuer := range image.Issuers {
			if issuer.EnvOwned {
				result[issuer.URL] = issuer
			}
		}
		return result
	}
	if !reflect.DeepEqual(envIssuers(old), envIssuers(next)) {
		return false
	}
	oldAssignments, oldRoles := locked(old)
	nextAssignments, nextRoles := locked(next)
	return reflect.DeepEqual(oldAssignments, nextAssignments) && reflect.DeepEqual(oldRoles, nextRoles)
}
