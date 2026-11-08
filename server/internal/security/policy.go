// Package security owns immutable security state, durable changes and current
// authentication/admission. Pure permission evaluation belongs to authz.
package security

import "github.com/anaregdesign/lantern/server/internal/authz"

// Action and the other aliases share authz's exact domain types.
// They preserve the transition without a second policy model.
type Action = authz.Action
type ResourceKind = authz.ResourceKind
type Permission = authz.Permission
type PermissionRule = authz.PermissionRule
type Role = authz.Role
type PolicyLimits = authz.PolicyLimits
type CompiledPolicy = authz.CompiledPolicy
type Access = authz.Access
type Scope = authz.Scope
type Range = authz.Range

const (
	VertexRead         = authz.VertexRead
	VertexWrite        = authz.VertexWrite
	VertexDelete       = authz.VertexDelete
	EdgeRead           = authz.EdgeRead
	EdgeCreate         = authz.EdgeCreate
	EdgeAdd            = authz.EdgeAdd
	EdgeWrite          = authz.EdgeWrite
	EdgeDelete         = authz.EdgeDelete
	Query              = authz.Query
	CDCIdentity        = authz.CDCIdentity
	CDCValue           = authz.CDCValue
	Export             = authz.Export
	ReceiptRead        = authz.ReceiptRead
	OperationsRead     = authz.OperationsRead
	SchemaRead         = authz.SchemaRead
	SecurityManage     = authz.SecurityManage
	DataResource       = authz.DataResource
	SecurityResource   = authz.SecurityResource
	SettingsResource   = authz.SettingsResource
	OperationsResource = authz.OperationsResource
	SchemaResource     = authz.SchemaResource
	Read               = authz.Read
	Write              = authz.Write
	SecurityRead       = authz.SecurityRead
	SettingsRead       = authz.SettingsRead
	SettingsWrite      = authz.SettingsWrite
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
func actionResource(action Action) (ResourceKind, bool) { return authz.ActionResource(action) }

func OperationPermission(action Action) (Permission, bool) { return authz.OperationPermission(action) }
func ActionResource(action Action) (ResourceKind, bool)    { return authz.ActionResource(action) }
