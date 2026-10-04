package security

import "strings"

// RoleTemplates are editable starting points for explicit assignment. Merely
// viewing or creating a template grants no Principal access. Prefix is literal
// in the public logical-key domain, including an explicit empty all-data prefix.
func RoleTemplates(prefix string) ([]Role, error) {
	templates := []struct {
		id, name string
		actions  []Action
	}{
		{"read_only", "Read only", []Action{VertexRead, Query}},
		{"application_writer", "Application writer", []Action{VertexRead, Query, VertexWrite}},
		{"data_maintainer", "Data maintainer", []Action{VertexRead, Query, VertexWrite, VertexDelete}},
		{"head_relationship_manager", "Manage relationships owned by head", []Action{VertexRead, VertexWrite, ReceiptRead}},
		{"cdc_identity", "CDC identities", []Action{CDCIdentity}},
		{"cdc_values", "CDC values", []Action{CDCIdentity, CDCValue, VertexRead}},
		{"data_exporter", "Data exporter", []Action{Export, VertexRead}},
		{"operations_reader", "Operations reader", []Action{OperationsRead}},
		{"security_admin", "Security administrator", []Action{SecurityManage}},
	}
	roles := make([]Role, len(templates))
	for i, template := range templates {
		roles[i] = Role{ID: template.id, Name: template.name}
		for _, action := range template.actions {
			resource, _ := actionResource(action)
			rule := PermissionRule{ID: strings.ReplaceAll(string(action), ".", "-"), Effect: Allow, Action: action, Resource: resource}
			if resource == DataResource {
				literal := prefix
				rule.Prefix = &literal
			}
			roles[i].Rules = append(roles[i].Rules, rule)
		}
	}
	if _, err := CompileRoles(roles, DefaultPolicyLimits()); err != nil {
		return nil, err
	}
	return roles, nil
}
