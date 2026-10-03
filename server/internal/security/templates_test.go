package security

import "testing"

func TestRoleTemplatesSeparateDataCDCExportAndControl(t *testing.T) {
	roles, err := RoleTemplates("tenant:1:")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := policy.ForRoles([]string{"read_only"})
	if err != nil || !reader.Allows(VertexRead, "tenant:1:value") || !reader.Allows(Query, "tenant:1:value") || reader.Allows(VertexWrite, "tenant:1:value") || reader.Allows(CDCIdentity, "tenant:1:value") || reader.Allows(Export, "tenant:1:value") || reader.Allows(VertexRead, "tenant:2:value") {
		t.Fatal("reader crossed a capability/scope boundary", err)
	}
	identity, err := policy.ForRoles([]string{"cdc_identity"})
	if err != nil || !identity.Allows(CDCIdentity, "tenant:1:value") || identity.Allows(CDCValue, "tenant:1:value") || identity.Allows(VertexRead, "tenant:1:value") {
		t.Fatal("identity CDC acquired value access", err)
	}
	control, err := policy.ForRoles([]string{"security_admin"})
	if err != nil || !control.AllowsGlobal(SecurityManage) || control.Allows(VertexRead, "tenant:1:value") || control.AllowsGlobal(OperationsRead) {
		t.Fatal("security administrator acquired data/operations access", err)
	}
	operations, err := policy.ForRoles([]string{"operations_reader"})
	if err != nil || !operations.AllowsGlobal(OperationsRead) || operations.AllowsGlobal(SecurityManage) {
		t.Fatal("operations access acquired management", err)
	}
	// Editor-owned templates are detached from a later caller and each other.
	*roles[0].Rules[0].Prefix = "different:"
	if *roles[0].Rules[1].Prefix != "tenant:1:" {
		t.Fatal("editing one rule changed another")
	}
	fresh, err := RoleTemplates("tenant:1:")
	if err != nil || *fresh[0].Rules[0].Prefix != "tenant:1:" {
		t.Fatal("template mutation escaped call", err)
	}
}
