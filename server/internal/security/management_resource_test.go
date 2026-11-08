package security

import "testing"

func TestManagementResourceBatchAndReadIndependence(t *testing.T) {
	roles := []Role{
		{ID: "security", Rules: []PermissionRule{globalRule(SecurityManage)}},
		{ID: "settings", Rules: []PermissionRule{globalRule(SettingsWrite)}},
		{ID: "reader", Rules: []PermissionRule{globalRule(SecurityRead), globalRule(SettingsRead)}},
		{ID: "data", Rules: []PermissionRule{dataRule(VertexRead, ""), dataRule(VertexWrite, "")}},
	}
	policy, err := CompileRoles(roles, DefaultPolicyLimits())
	if err != nil {
		t.Fatal(err)
	}
	securityOnly, _ := policy.ForRoles([]string{"security"})
	settingsOnly, _ := policy.ForRoles([]string{"settings"})
	both, _ := policy.ForRoles([]string{"security", "settings"})
	reader, _ := policy.ForRoles([]string{"reader"})
	data, _ := policy.ForRoles([]string{"data"})
	sec := []Change{{Kind: PutRole, Role: &Role{ID: "new_role"}}}
	settings := []Change{{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: LogPolicyVersion, Level: LogLevelInfo}}}
	mixed := append(append([]Change{}, sec...), settings...)
	for _, tc := range []struct {
		access                    *Access
		security, settings, mixed bool
	}{{securityOnly, true, false, false}, {settingsOnly, false, true, false}, {both, true, true, true}, {reader, false, false, false}, {data, false, false, false}} {
		if CanManageChanges(tc.access, sec) != tc.security || CanManageChanges(tc.access, settings) != tc.settings || CanManageChanges(tc.access, mixed) != tc.mixed {
			t.Fatal("batch resource boundary", tc)
		}
	}
	if canReadManagementChanges(both, mixed) || !canReadManagementChanges(reader, mixed) {
		t.Fatal("write implied read or read rejected")
	}
	for _, invalid := range [][]Change{nil, {{Kind: "unknown"}}, {{Kind: PutRole}}, {{Kind: PutLogPolicy, LogPolicy: &LogPolicyChange{Version: 999, Level: LogLevelInfo}}}} {
		if CanManageChanges(both, invalid) {
			t.Fatal("invalid command admitted")
		}
	}
}
