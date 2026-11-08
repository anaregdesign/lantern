package security

// ManagementActions is the exhaustive typed-resource boundary for one atomic
// batch. Mixed Security/Settings batches need both writes; a name never grants
// authority. Invalid/empty changes cannot manufacture an authorized command.
func ManagementActions(changes []Change) ([]Action, error) {
	if len(changes) == 0 || len(changes) > MaxTransactionChanges {
		return nil, ErrInvalidImage
	}
	security, settings := false, false
	for _, change := range changes {
		if err := change.validate(); err != nil {
			return nil, err
		}
		switch change.Kind {
		case PutLogPolicy, PutOperationalPolicy:
			settings = true
		case PutIssuer, DisableIssuer, DeleteIssuer, PutRole, DeleteRole, PutPrincipal,
			DeletePrincipal, PutAssignment, DeleteAssignment, RevokeSessions, PutRegistrationPolicy, PutEdgePolicy:
			security = true
		default:
			return nil, ErrInvalidImage
		}
	}
	var actions []Action
	if security {
		actions = append(actions, SecurityManage)
	}
	if settings {
		actions = append(actions, SettingsWrite)
	}
	return actions, nil
}

func CanManageChanges(access *Access, changes []Change) bool {
	actions, err := ManagementActions(changes)
	if err != nil {
		return false
	}
	for _, action := range actions {
		if !access.AllowsGlobal(action) {
			return false
		}
	}
	return true
}

func canReadManagementChanges(access *Access, changes []Change) bool {
	actions, err := ManagementActions(changes)
	if err != nil {
		return false
	}
	for _, action := range actions {
		required := SecurityRead
		if action == SettingsWrite {
			required = SettingsRead
		}
		if !access.AllowsGlobal(required) {
			return false
		}
	}
	return true
}

func hasManagementWrite(access *Access) bool {
	return access.AllowsGlobal(SecurityManage) || access.AllowsGlobal(SettingsWrite)
}
