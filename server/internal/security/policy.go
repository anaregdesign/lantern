// Package security owns immutable security state, durable changes and current
// authentication/admission. Pure permission evaluation belongs to authz.
package security

import "github.com/anaregdesign/lantern/server/internal/authz"

// Domain state and existing internal consumers share authz's exact types.
// These aliases preserve the transition without a second policy model.
type Action = authz.Action
type ResourceKind = authz.ResourceKind
type Effect = authz.Effect
type PermissionRule = authz.PermissionRule
type PrefixPair = authz.PrefixPair
type Role = authz.Role
type PolicyLimits = authz.PolicyLimits
type CompiledPolicy = authz.CompiledPolicy
type Access = authz.Access
type Scope = authz.Scope
type Range = authz.Range

const (
	VertexRead     = authz.VertexRead
	VertexWrite    = authz.VertexWrite
	VertexDelete   = authz.VertexDelete
	EdgeRead       = authz.EdgeRead
	EdgeCreate     = authz.EdgeCreate
	EdgeAdd        = authz.EdgeAdd
	EdgeWrite      = authz.EdgeWrite
	EdgeDelete     = authz.EdgeDelete
	Query          = authz.Query
	CDCIdentity    = authz.CDCIdentity
	CDCValue       = authz.CDCValue
	Export         = authz.Export
	ReceiptRead    = authz.ReceiptRead
	OperationsRead = authz.OperationsRead
	SchemaRead     = authz.SchemaRead
	SecurityManage = authz.SecurityManage
	DataResource   = authz.DataResource
	GlobalResource = authz.GlobalResource
	Allow          = authz.Allow
	Deny           = authz.Deny
)

var (
	ErrInvalidPolicy = authz.ErrInvalidPolicy
	ErrUnknownRole   = authz.ErrUnknownRole
)

func CompileRoles(roles []Role, limits PolicyLimits) (*CompiledPolicy, error) {
	return authz.CompileRoles(roles, limits)
}
func DefaultPolicyLimits() PolicyLimits { return authz.DefaultPolicyLimits() }
func validateRoleBounds(roles []Role, limits PolicyLimits) error {
	return authz.ValidateRoleBounds(roles, limits)
}
func validRoleID(id string) bool                        { return authz.ValidRoleID(id) }
func grantableAction(action Action) bool                { return authz.GrantableAction(action) }
func actionResource(action Action) (ResourceKind, bool) { return authz.ActionResource(action) }
func edgeSelectorAction(action Action) bool             { return authz.EdgeSelectorAction(action) }
