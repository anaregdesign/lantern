package security

import "testing"

func TestRoleTemplatesTypedPermissionsAndDetachedDrafts(t *testing.T) {
	roles, err := RoleTemplates("tenant:1:")
	if err != nil {
		t.Fatal(err)
	}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	reader, err := policy.ForRoles([]string{"read_only"})
	if err != nil || !reader.Allows(VertexRead, "tenant:1:value") || !reader.Allows(Query, "tenant:1:value") || !reader.Allows(CDCValue, "tenant:1:value") || !reader.Allows(Export, "tenant:1:value") || reader.Allows(VertexWrite, "tenant:1:value") || reader.Allows(VertexRead, "tenant:2:value") {
		t.Fatal("reader typed map/scope", err)
	}
	// RR deliberately lets readers modify Edges between readable endpoints.
	if !reader.AllowsEdge(EdgeDelete, "tenant:1:a", "tenant:1:b") {
		t.Fatal("RR consequence missing")
	}
	control, err := policy.ForRoles([]string{"security_admin"})
	if err != nil || !control.AllowsGlobal(SecurityRead) || !control.AllowsGlobal(SecurityManage) || !control.AllowsGlobal(SettingsRead) || !control.AllowsGlobal(SettingsWrite) || control.Allows(VertexRead, "tenant:1:value") || control.AllowsGlobal(OperationsRead) {
		t.Fatal("Security/Settings boundary", err)
	}
	ops, err := policy.ForRoles([]string{"operations_reader"})
	if err != nil || !ops.AllowsGlobal(OperationsRead) || ops.AllowsGlobal(SecurityManage) || ops.AllowsGlobal(SettingsRead) {
		t.Fatal("Ops boundary", err)
	}
	writer, err := policy.ForRoles([]string{"application_writer"})
	if err != nil || !writer.Allows(VertexDelete, "tenant:1:a") || !writer.Allows(ReceiptRead, "tenant:1:b") {
		t.Fatal("write/delete mapping", err)
	}
	blind, err := policy.ForRoles([]string{"write_only"})
	if err != nil || !blind.Allows(VertexWrite, "tenant:1:a") || blind.Allows(VertexRead, "tenant:1:a") || blind.Allows(ReceiptRead, "tenant:1:a") {
		t.Fatal("blind grant disclosed", err)
	}
	for _, obsolete := range []string{"connection_creator", "cdc_identity", "cdc_values", "head_relationship_manager"} {
		if _, err := policy.ForRoles([]string{obsolete}); err == nil {
			t.Fatal("retired capability template survived", obsolete)
		}
	}
	*roles[1].Rules[0].Prefix = "different:"
	if *roles[1].Rules[1].Prefix != "tenant:1:" {
		t.Fatal("draft rules share mutable selector")
	}
	fresh, err := RoleTemplates("tenant:1:")
	if err != nil || *fresh[1].Rules[0].Prefix != "tenant:1:" {
		t.Fatal("template caller mutation escaped", err)
	}
}
