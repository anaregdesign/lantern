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
		{"read_only", "Read only", []Action{VertexRead, EdgeRead, Query}},
		{"application_writer", "Application writer", []Action{VertexRead, EdgeRead, Query, VertexWrite, VertexDelete, EdgeAdd, EdgeWrite, EdgeDelete}},
		{"cdc_identity", "CDC identities", []Action{CDCIdentity}},
		{"cdc_values", "CDC values", []Action{CDCIdentity, CDCValue, VertexRead, EdgeRead}},
		{"data_exporter", "Data exporter", []Action{Export, VertexRead, EdgeRead}},
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
	// Operators edit and explicitly assign each literal pair. A Principal's
	// own prefix comes from its trusted assignment, never a client assertion.
	for _, template := range []struct {
		id, name string
		actions  []Action
	}{
		{"connection_creator", "Create connections between existing endpoints", []Action{EdgeCreate, ReceiptRead}},
		{"connection_deleter", "Delete connections", []Action{EdgeRead, EdgeDelete, ReceiptRead}},
	} {
		literal := prefix
		role := Role{ID: template.id, Name: template.name, Rules: []PermissionRule{{ID: "endpoint-read", Effect: Allow, Action: VertexRead, Resource: DataResource, Prefix: &literal}}}
		for _, action := range template.actions {
			role.Rules = append(role.Rules, PermissionRule{ID: strings.ReplaceAll(string(action), ".", "-"), Effect: Allow, Action: action, Resource: DataResource, Pair: &PrefixPair{Tail: prefix, Head: prefix}})
		}
		roles = append(roles, role)
	}
	if _, err := CompileRoles(roles, DefaultPolicyLimits()); err != nil {
		return nil, err
	}
	return roles, nil
}
