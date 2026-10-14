package authz

import (
	"encoding/json"
	"testing"
)

// Existing signed Role/Image/operation transcripts retain these bytes after
// package movement and when optional selector fields are added later.
func TestRoleCanonicalEncoding(t *testing.T) {
	for _, tc := range []struct {
		name string
		rule PermissionRule
		want string
	}{
		{"literal", dataRule(Allow, VertexRead, "orders:"), `{"id":"reader","name":"","rules":[{"effect":"allow","action":"vertex.read","resource":"data","prefix":"orders:"}]}`},
		{"whole data", dataRule(Deny, VertexWrite, ""), `{"id":"reader","name":"","rules":[{"effect":"deny","action":"vertex.write","resource":"data","prefix":""}]}`},
		{"global", globalRule(Allow, OperationsRead), `{"id":"reader","name":"","rules":[{"effect":"allow","action":"operations.read","resource":"global"}]}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			encoded, err := json.Marshal(Role{ID: "reader", Rules: []PermissionRule{tc.rule}})
			if err != nil || string(encoded) != tc.want {
				t.Fatalf("legacy canonical Role changed: %s (%v)", encoded, err)
			}
		})
	}
}
