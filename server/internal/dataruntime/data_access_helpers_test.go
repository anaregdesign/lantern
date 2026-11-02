package dataruntime

import (
	"context"
	"testing"

	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
)

func dataAccessRule(id string, effect security.Effect, action security.Action, prefix string) security.PermissionRule {
	return security.PermissionRule{ID: id, Effect: effect, Action: action, Resource: security.DataResource, Prefix: &prefix}
}
func dataAccessFixture(t *testing.T, rules []security.PermissionRule) *security.Access {
	t.Helper()
	p, err := security.CompileRoles([]security.Role{{ID: "data", Rules: rules}}, security.DefaultPolicyLimits())
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
