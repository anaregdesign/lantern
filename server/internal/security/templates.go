package security

// RoleTemplates are editable positive starting points. Constructing a Role
// grants nobody access; membership is a separate reviewed operation. A literal
// empty data prefix explicitly covers all logical data, never sys metadata.
func RoleTemplates(prefix string) ([]Role, error) {
	templates := []struct {
		id, name    string
		resource    ResourceKind
		permissions []Permission
	}{
		{"read_only", "Data reader (Edge edits follow global policy)", DataResource, []Permission{Read}},
		{"application_writer", "Application reader and writer", DataResource, []Permission{Read, Write}},
		{"data_maintainer", "Data administrator", DataResource, []Permission{Read, Write}},
		{"write_only", "Blind data writer", DataResource, []Permission{Write}},
		{"operations_reader", "Operations reader", OperationsResource, []Permission{Read}},
		{"settings_admin", "Settings administrator", SettingsResource, []Permission{Read, Write}},
		{"security_admin", "Security and Settings administrator", SecurityResource, []Permission{Read, Write}},
	}
	roles := make([]Role, len(templates))
	for i, template := range templates {
		role := Role{ID: template.id, Name: template.name}
		for _, permission := range template.permissions {
			rule := PermissionRule{ID: string(template.resource) + "-" + string(permission), Permission: permission, Resource: template.resource}
			if template.resource == DataResource {
				literal := prefix
				rule.Prefix = &literal
			}
			role.Rules = append(role.Rules, rule)
		}
		if template.id == "security_admin" {
			for _, permission := range []Permission{Read, Write} {
				role.Rules = append(role.Rules, PermissionRule{ID: "settings-" + string(permission), Permission: permission, Resource: SettingsResource})
			}
		}
		roles[i] = role
	}
	if _, err := CompileRoles(roles, DefaultPolicyLimits()); err != nil {
		return nil, err
	}
	return roles, nil
}
