package securitytransport

import (
	"errors"
	"reflect"

	"connectrpc.com/connect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
	"github.com/anaregdesign/lantern/server/internal/authz"
	"github.com/anaregdesign/lantern/server/internal/security"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
)

var securityActions = map[pb.SecurityAction]security.Action{
	pb.SecurityAction_SECURITY_ACTION_VERTEX_READ:     security.VertexRead,
	pb.SecurityAction_SECURITY_ACTION_VERTEX_WRITE:    security.VertexWrite,
	pb.SecurityAction_SECURITY_ACTION_VERTEX_DELETE:   security.VertexDelete,
	pb.SecurityAction_SECURITY_ACTION_EDGE_READ:       security.EdgeRead,
	pb.SecurityAction_SECURITY_ACTION_EDGE_CREATE:     security.EdgeCreate,
	pb.SecurityAction_SECURITY_ACTION_EDGE_ADD:        security.EdgeAdd,
	pb.SecurityAction_SECURITY_ACTION_EDGE_WRITE:      security.EdgeWrite,
	pb.SecurityAction_SECURITY_ACTION_EDGE_DELETE:     security.EdgeDelete,
	pb.SecurityAction_SECURITY_ACTION_QUERY:           security.Query,
	pb.SecurityAction_SECURITY_ACTION_CDC_IDENTITY:    security.CDCIdentity,
	pb.SecurityAction_SECURITY_ACTION_CDC_VALUE:       security.CDCValue,
	pb.SecurityAction_SECURITY_ACTION_EXPORT:          security.Export,
	pb.SecurityAction_SECURITY_ACTION_RECEIPT_READ:    security.ReceiptRead,
	pb.SecurityAction_SECURITY_ACTION_OPERATIONS_READ: security.OperationsRead,
	pb.SecurityAction_SECURITY_ACTION_SCHEMA_READ:     security.SchemaRead,
	pb.SecurityAction_SECURITY_ACTION_MANAGE:          security.SecurityManage,
	pb.SecurityAction_SECURITY_ACTION_SECURITY_READ:   security.SecurityRead,
	pb.SecurityAction_SECURITY_ACTION_SETTINGS_READ:   security.SettingsRead,
	pb.SecurityAction_SECURITY_ACTION_SETTINGS_WRITE:  security.SettingsWrite,
}

func strictSecurityMessage(message proto.Message) error {
	if message == nil || !message.ProtoReflect().IsValid() {
		return security.ErrInvalidImage
	}
	return strictSecurityReflection(message.ProtoReflect())
}
func strictSecurityReflection(message protoreflect.Message) error {
	if !message.IsValid() || len(message.GetUnknown()) != 0 {
		return security.ErrInvalidImage
	}
	var invalid error
	message.Range(func(field protoreflect.FieldDescriptor, value protoreflect.Value) bool {
		check := func(v protoreflect.Value) bool {
			if field.Kind() == protoreflect.MessageKind {
				invalid = strictSecurityReflection(v.Message())
			}
			if field.Kind() == protoreflect.EnumKind && field.Enum().Values().ByNumber(v.Enum()) == nil {
				invalid = security.ErrInvalidImage
			}
			return invalid == nil
		}
		if field.IsList() {
			for i := 0; i < value.List().Len(); i++ {
				if !check(value.List().Get(i)) {
					return false
				}
			}
			return true
		}
		return check(value)
	})
	return invalid
}
func decodeSecurityIdentity(identity *pb.SecurityIdentity) (security.Identity, error) {
	if identity == nil {
		return security.Identity{}, security.ErrInvalidImage
	}
	result := security.Identity{Issuer: identity.Issuer, Subject: identity.Subject, MachineName: identity.MachineName}
	switch identity.Kind {
	case pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC:
		result.Kind = security.OIDCPrincipal
	case pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_MACHINE:
		result.Kind = security.MachinePrincipal
	default:
		return security.Identity{}, security.ErrInvalidImage
	}
	return result, nil
}
func encodeSecurityIdentity(identity security.Identity) *pb.SecurityIdentity {
	kind := pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_OIDC
	if identity.Kind == security.MachinePrincipal {
		kind = pb.SecurityPrincipalKind_SECURITY_PRINCIPAL_KIND_MACHINE
	}
	return &pb.SecurityIdentity{Kind: kind, Issuer: identity.Issuer, Subject: identity.Subject, MachineName: identity.MachineName}
}
func decodeSecurityIssuer(issuer *pb.SecurityIssuer) (security.Issuer, error) {
	if issuer == nil || issuer.ConfigRevision != 0 || issuer.EnvOwned || issuer.Deleted || issuer.HasSecretBinding {
		return security.Issuer{}, security.ErrInvalidImage
	}
	result := security.Issuer{URL: issuer.Issuer, Enabled: issuer.Enabled, ClientID: issuer.ClientId, APIAudience: issuer.ApiAudience, RedirectURI: issuer.RedirectUri, Algorithms: append([]string(nil), issuer.Algorithms...), SecretRef: issuer.GetSecretRef(), HumanSubjectNamespaceQualified: issuer.HumanSubjectNamespaceQualified}
	if len(issuer.NativeClients) > security.MaxNativeClients {
		return security.Issuer{}, security.ErrInvalidImage
	}
	for _, c := range issuer.NativeClients {
		if c == nil || c.HasSecretBinding || len(c.Presenters) > security.MaxNativePresenters {
			return security.Issuer{}, security.ErrInvalidImage
		}
		kind, ok := nativeClientKinds[c.Kind]
		if !ok {
			return security.Issuer{}, security.ErrInvalidImage
		}
		result.NativeClients = append(result.NativeClients, security.NativeClient{ID: c.Id, Kind: kind, ClientID: c.ClientId, ServiceOrigin: c.ServiceOrigin, Presenters: append([]string(nil), c.Presenters...), RedirectURI: c.RedirectUri, AndroidPackage: c.AndroidPackage, SecretRef: c.GetSecretRef()})
	}
	return result, nil
}

var nativeClientKinds = map[pb.NativeClientKind]string{
	pb.NativeClientKind_NATIVE_CLIENT_KIND_GOOGLE_ID_TOKEN:   security.GoogleIDToken,
	pb.NativeClientKind_NATIVE_CLIENT_KIND_APPLE_NATIVE:      security.AppleNative,
	pb.NativeClientKind_NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB: security.AppleAndroidWeb,
}

func encodeNativeKind(kind string) pb.NativeClientKind {
	for encoded, value := range nativeClientKinds {
		if kind == value {
			return encoded
		}
	}
	return pb.NativeClientKind_NATIVE_CLIENT_KIND_UNSPECIFIED
}

func issuerPreservation(wire *pb.SecurityIssuer, issuer security.Issuer) security.Change {
	change := security.Change{Kind: security.PutIssuer, Issuer: &issuer, PreserveSecret: wire != nil && wire.SecretRef == nil}
	if wire != nil {
		for _, client := range wire.NativeClients {
			if client != nil && client.SecretRef == nil && (client.Kind == pb.NativeClientKind_NATIVE_CLIENT_KIND_APPLE_NATIVE || client.Kind == pb.NativeClientKind_NATIVE_CLIENT_KIND_APPLE_ANDROID_WEB) {
				change.PreserveNativeSecrets = append(change.PreserveNativeSecrets, client.Id)
			}
		}
	}
	return change
}
func encodeSecurityIssuer(issuer security.Issuer) *pb.SecurityIssuer {
	// SecretRef is deliberately write-only, including for security administrators.
	result := &pb.SecurityIssuer{Issuer: issuer.URL, Enabled: issuer.Enabled, ClientId: issuer.ClientID, ApiAudience: issuer.APIAudience, RedirectUri: issuer.RedirectURI, Algorithms: append([]string(nil), issuer.Algorithms...), ConfigRevision: issuer.ConfigRevision, EnvOwned: issuer.EnvOwned, Deleted: issuer.Deleted, HasSecretBinding: issuer.SecretRef != "", HumanSubjectNamespaceQualified: issuer.HumanSubjectNamespaceQualified}
	for _, c := range issuer.NativeClients {
		result.NativeClients = append(result.NativeClients, &pb.SecurityNativeClient{Id: c.ID, Kind: encodeNativeKind(c.Kind), ClientId: c.ClientID, ServiceOrigin: c.ServiceOrigin, Presenters: append([]string(nil), c.Presenters...), RedirectUri: c.RedirectURI, AndroidPackage: c.AndroidPackage, HasSecretBinding: c.SecretRef != ""})
	}
	return result
}

var securityPermissions = map[pb.SecurityPermission]security.Permission{
	pb.SecurityPermission_SECURITY_PERMISSION_READ:  security.Read,
	pb.SecurityPermission_SECURITY_PERMISSION_WRITE: security.Write,
}
var securityResources = map[pb.SecurityResource]security.ResourceKind{
	pb.SecurityResource_SECURITY_RESOURCE_DATA:       security.DataResource,
	pb.SecurityResource_SECURITY_RESOURCE_SECURITY:   security.SecurityResource,
	pb.SecurityResource_SECURITY_RESOURCE_SETTINGS:   security.SettingsResource,
	pb.SecurityResource_SECURITY_RESOURCE_OPERATIONS: security.OperationsResource,
	pb.SecurityResource_SECURITY_RESOURCE_SCHEMA:     security.SchemaResource,
}

func encodeSecurityPermission(permission security.Permission) pb.SecurityPermission {
	for wire, value := range securityPermissions {
		if value == permission {
			return wire
		}
	}
	return pb.SecurityPermission_SECURITY_PERMISSION_UNSPECIFIED
}
func encodeSecurityResource(resource security.ResourceKind) pb.SecurityResource {
	for wire, value := range securityResources {
		if value == resource {
			return wire
		}
	}
	return pb.SecurityResource_SECURITY_RESOURCE_UNSPECIFIED
}
func decodeSecurityRole(role *pb.SecurityRole) (security.Role, error) {
	if role == nil || role.EnvOwned || strictSecurityMessage(role) != nil || len(role.Rules) > security.DefaultPolicyLimits().MaxRules {
		return security.Role{}, security.ErrInvalidPolicy
	}
	result := security.Role{ID: role.Id, Name: role.Name, Rules: make([]security.PermissionRule, len(role.Rules))}
	for i, rule := range role.Rules {
		if rule == nil {
			return security.Role{}, security.ErrInvalidPolicy
		}
		permission, known := securityPermissions[rule.Permission]
		resourceKind, resourceKnown := securityResources[rule.ResourceKind]
		if !known || !resourceKnown || !authz.ValidPermission(resourceKind, permission) {
			return security.Role{}, security.ErrInvalidPolicy
		}
		target := security.PermissionRule{ID: rule.Id, Permission: permission, Resource: resourceKind}
		switch resource := rule.Resource.(type) {
		case *pb.SecurityRule_Prefix:
			if resource == nil || resourceKind != security.DataResource {
				return security.Role{}, security.ErrInvalidPolicy
			}
			prefix := resource.Prefix
			target.Prefix = &prefix
		case *pb.SecurityRule_PrincipalUidPrefix:
			if resource == nil || resource.PrincipalUidPrefix == nil || resourceKind != security.DataResource {
				return security.Role{}, security.ErrInvalidPolicy
			}
			target.PrincipalUIDPrefix = &authz.PrincipalUIDPrefix{LiteralBefore: resource.PrincipalUidPrefix.LiteralBefore, LiteralAfter: resource.PrincipalUidPrefix.LiteralAfter}
		case nil:
			if resourceKind == security.DataResource {
				return security.Role{}, security.ErrInvalidPolicy
			}
		default:
			return security.Role{}, security.ErrInvalidPolicy
		}
		result.Rules[i] = target
	}
	return result, nil
}
func encodeSecurityRole(role security.Role) *pb.SecurityRole {
	result := &pb.SecurityRole{Id: role.ID, Name: role.Name, Rules: make([]*pb.SecurityRule, len(role.Rules))}
	for i, rule := range role.Rules {
		if !authz.ValidPermission(rule.Resource, rule.Permission) {
			return nil
		}
		target := &pb.SecurityRule{Id: rule.ID, Permission: encodeSecurityPermission(rule.Permission), ResourceKind: encodeSecurityResource(rule.Resource)}
		if rule.PrincipalUIDPrefix != nil {
			if rule.Prefix != nil || rule.Resource != security.DataResource {
				return nil
			}
			target.Resource = &pb.SecurityRule_PrincipalUidPrefix{PrincipalUidPrefix: &pb.SecurityPrincipalUIDPrefix{LiteralBefore: rule.PrincipalUIDPrefix.LiteralBefore, LiteralAfter: rule.PrincipalUIDPrefix.LiteralAfter}}
		} else if rule.Prefix != nil {
			if rule.Resource != security.DataResource {
				return nil
			}
			target.Resource = &pb.SecurityRule_Prefix{Prefix: *rule.Prefix}
		} else if rule.Resource == security.DataResource {
			return nil
		}
		result.Rules[i] = target
	}
	return result
}

// Environment policy locks are response metadata, never client-owned grants.
func encodeSecurityManagedRole(role security.Role, image security.Image) *pb.SecurityRole {
	result := encodeSecurityRole(role)
	if result == nil {
		return nil
	}
	for _, principal := range image.Principals {
		for _, assignment := range principal.Assignments {
			if assignment.RoleID == role.ID && assignment.EnvOwned {
				result.EnvOwned = true
				return result
			}
		}
	}
	return result
}
func encodeSecurityUser(user security.Principal) *pb.SecurityUser {
	state := pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE
	if user.State == security.Suspended {
		state = pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_SUSPENDED
	}
	if user.State == security.Deleted {
		state = pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_DELETED
	}
	result := &pb.SecurityUser{Identity: encodeSecurityIdentity(user.Identity), State: state}
	if user.VerifiedEmail != "" {
		result.VerifiedEmail = &user.VerifiedEmail
	}
	if user.UserUID != "" {
		result.UserId = &user.UserUID
	}
	for _, assignment := range user.Assignments {
		result.Assignments = append(result.Assignments, &pb.SecurityRoleAssignment{Identity: encodeSecurityIdentity(user.Identity), RoleId: assignment.RoleID, EnvOwned: assignment.EnvOwned})
	}
	return result
}
func decodeSecurityChange(change *pb.SecurityChange) (security.Change, error) {
	if change == nil || change.Operation == nil || reflect.ValueOf(change.Operation).IsNil() {
		return security.Change{}, security.ErrInvalidImage
	}
	result := security.Change{}
	var err error
	switch op := change.Operation.(type) {
	case *pb.SecurityChange_PutEdgePolicy:
		result.Kind = security.PutEdgePolicy
		result.EdgePolicy, err = decodeEdgePolicy(op.PutEdgePolicy)
	case *pb.SecurityChange_PutOperationalPolicy:
		result.Kind = security.PutOperationalPolicy
		result.OperationalPolicy, err = decodeOperationalPolicy(op.PutOperationalPolicy)
	case *pb.SecurityChange_PutLogPolicy:
		result.Kind = security.PutLogPolicy
		result.LogPolicy, err = decodeLogPolicy(op.PutLogPolicy)
	case *pb.SecurityChange_PutRegistrationPolicy:
		result.Kind = security.PutRegistrationPolicy
		result.RegistrationPolicy, err = decodeRegistrationPolicy(op.PutRegistrationPolicy)
	case *pb.SecurityChange_PutIssuer:
		result.Kind = security.PutIssuer
		var issuer security.Issuer
		issuer, err = decodeSecurityIssuer(op.PutIssuer)
		result = issuerPreservation(op.PutIssuer, issuer)
	case *pb.SecurityChange_DisableIssuer:
		result.Kind = security.DisableIssuer
		result.IssuerURL = op.DisableIssuer
	case *pb.SecurityChange_DeleteIssuer:
		result.Kind = security.DeleteIssuer
		result.IssuerURL = op.DeleteIssuer
	case *pb.SecurityChange_PutRole:
		result.Kind = security.PutRole
		var role security.Role
		role, err = decodeSecurityRole(op.PutRole)
		result.Role = &role
	case *pb.SecurityChange_DeleteRole:
		result.Kind = security.DeleteRole
		result.RoleID = op.DeleteRole
	case *pb.SecurityChange_PutUser:
		if op.PutUser == nil {
			return result, security.ErrInvalidImage
		}
		result.Kind = security.PutPrincipal
		identity, e := decodeSecurityIdentity(op.PutUser.Identity)
		err = e
		result.Identity = &identity
		switch op.PutUser.State {
		case pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_ACTIVE:
			result.State = security.Active
		case pb.SecurityPrincipalState_SECURITY_PRINCIPAL_STATE_SUSPENDED:
			result.State = security.Suspended
		default:
			err = security.ErrInvalidImage
		}
	case *pb.SecurityChange_DeleteUser:
		result.Kind = security.DeletePrincipal
		identity, e := decodeSecurityIdentity(op.DeleteUser)
		err = e
		result.Identity = &identity
	case *pb.SecurityChange_RevokeUserSessions:
		result.Kind = security.RevokeSessions
		identity, e := decodeSecurityIdentity(op.RevokeUserSessions)
		err = e
		result.Identity = &identity
	case *pb.SecurityChange_PutAssignment:
		result.Kind = security.PutAssignment
		err = decodeSecurityAssignment(&result, op.PutAssignment)
	case *pb.SecurityChange_DeleteAssignment:
		result.Kind = security.DeleteAssignment
		err = decodeSecurityAssignment(&result, op.DeleteAssignment)
	default:
		err = security.ErrInvalidImage
	}
	return result, err
}
func decodeSecurityAssignment(result *security.Change, assignment *pb.SecurityRoleAssignment) error {
	if assignment == nil || assignment.EnvOwned {
		return security.ErrInvalidImage
	}
	identity, err := decodeSecurityIdentity(assignment.Identity)
	result.Identity = &identity
	result.RoleID = assignment.RoleId
	return err
}
func securityErrorCode(err error) connect.Code {
	if errors.Is(err, security.ErrS1Contract) {
		return connect.CodeInvalidArgument
	}
	switch {
	case errors.Is(err, security.ErrAuthorityUnavailable):
		return connect.CodeUnavailable
	case errors.Is(err, security.ErrRevisionConflict), errors.Is(err, security.ErrChangeConflict):
		return connect.CodeAborted
	case errors.Is(err, security.ErrPermissionDenied):
		return connect.CodePermissionDenied
	case errors.Is(err, security.ErrOperationAuthorization), errors.Is(err, security.ErrBootstrapLocked), errors.Is(err, security.ErrLastAdministrator), errors.Is(err, security.ErrUnknownRole):
		return connect.CodeFailedPrecondition
	case errors.Is(err, security.ErrControlReserve):
		return connect.CodeResourceExhausted
	default:
		return connect.CodeInvalidArgument
	}
}
