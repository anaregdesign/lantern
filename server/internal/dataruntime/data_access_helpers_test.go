package dataruntime

import (
	"context"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/authz"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func dataAccessRule(id string, action security.Action, prefix string) security.PermissionRule {
	permission, _ := security.OperationPermission(action)
	return security.PermissionRule{ID: id, Permission: permission, Resource: security.DataResource, Prefix: &prefix}
}
func dataAccessFixture(t *testing.T, rules []security.PermissionRule) *security.Access {
	t.Helper()
	return dataAccessFixtureWithMode(t, rules, authz.EdgeReadRead)
}
func dataAccessFixtureWithMode(t *testing.T, rules []security.PermissionRule, mode authz.EdgeEndpointMode) *security.Access {
	t.Helper()
	p, err := authz.CompileRolesWithEdgeMode([]security.Role{{ID: "data", Rules: rules}}, security.DefaultPolicyLimits(), mode)
	if err != nil {
		t.Fatal(err)
	}
	a, err := p.ForRoles([]string{"data"})
	if err != nil {
		t.Fatal(err)
	}
	return a
}
func dataPolicyQueryContext(ctx context.Context, message proto.Message, access *security.Access) (context.Context, error) {
	ctx, _, err := planDataQuery(ctx, message, access)
	return ctx, err
}
