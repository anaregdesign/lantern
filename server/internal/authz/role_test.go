package authz

import (
	"encoding/json"
	"testing"
)

func TestRoleCanonicalEncoding(t *testing.T) {
	for _, tc := range []struct {
		rule PermissionRule
		want string
	}{
		{dataRule(Read, "orders:"), `{"id":"reader","name":"","rules":[{"permission":"read","resource":"data","prefix":"orders:"}]}`},
		{dataRule(Write, ""), `{"id":"reader","name":"","rules":[{"permission":"write","resource":"data","prefix":""}]}`},
		{globalRule(OperationsResource, Read), `{"id":"reader","name":"","rules":[{"permission":"read","resource":"operations"}]}`},
	} {
		b, err := json.Marshal(Role{ID: "reader", Rules: []PermissionRule{tc.rule}})
		if err != nil || string(b) != tc.want {
			t.Fatalf("%s (%v)", b, err)
		}
	}
}
