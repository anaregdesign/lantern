package service

import (
	"connectrpc.com/connect"
	"errors"
	"reflect"

	pb "github.com/anaregdesign/lantern/pb/graph/v1"
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
	return security.Issuer{URL: issuer.Issuer, Enabled: issuer.Enabled, ClientID: issuer.ClientId, APIAudience: issuer.ApiAudience, RedirectURI: issuer.RedirectUri, Algorithms: append([]string(nil), issuer.Algorithms...), SecretRef: issuer.GetSecretRef(), HumanSubjectNamespaceQualified: issuer.HumanSubjectNamespaceQualified}, nil
}
func encodeSecurityIssuer(issuer security.Issuer) *pb.SecurityIssuer {
	// SecretRef is deliberately write-only, including for security administrators.
	return &pb.SecurityIssuer{Issuer: issuer.URL, Enabled: issuer.Enabled, ClientId: issuer.ClientID, ApiAudience: issuer.APIAudience, RedirectUri: issuer.RedirectURI, Algorithms: append([]string(nil), issuer.Algorithms...), ConfigRevision: issuer.ConfigRevision, EnvOwned: issuer.EnvOwned, Deleted: issuer.Deleted, HasSecretBinding: issuer.SecretRef != "", HumanSubjectNamespaceQualified: issuer.HumanSubjectNamespaceQualified}
}
func decodeSecurityRole(role *pb.SecurityRole) (security.Role, error) {
	if role == nil || role.EnvOwned || len(role.Rules) > security.DefaultPolicyLimits().MaxRules {
		return security.Role{}, security.ErrInvalidPolicy
	}
	result := security.Role{ID: role.Id, Name: role.Name, Rules: make([]security.PermissionRule, len(role.Rules))}
	for i, rule := range role.Rules {
		if rule == nil {
			return security.Role{}, security.ErrInvalidPolicy
		}
		action, known := securityActions[rule.Action]
		if !known {
			return security.Role{}, security.ErrInvalidPolicy
		}
		switch action {
		case security.EdgeRead, security.EdgeCreate, security.EdgeAdd, security.EdgeWrite, security.EdgeDelete:
			return security.Role{}, security.ErrInvalidPolicy
		}
		target := security.PermissionRule{ID: rule.Id, Action: action}
		switch rule.Effect {
		case pb.SecurityEffect_SECURITY_EFFECT_ALLOW:
			target.Effect = security.Allow
		case pb.SecurityEffect_SECURITY_EFFECT_DENY:
			target.Effect = security.Deny
		default:
			return security.Role{}, security.ErrInvalidPolicy
		}
		switch resource := rule.Resource.(type) {
		case *pb.SecurityRule_Prefix:
			if resource == nil {
				return security.Role{}, security.ErrInvalidPolicy
			}
			target.Resource = security.DataResource
			prefix := resource.Prefix
			target.Prefix = &prefix
		case *pb.SecurityRule_Global:
			if resource == nil || !resource.Global {
				return security.Role{}, security.ErrInvalidPolicy
			}
			target.Resource = security.GlobalResource
		case *pb.SecurityRule_Pair:
			return security.Role{}, security.ErrInvalidPolicy
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
		target := &pb.SecurityRule{Id: rule.ID, Effect: encodeSecurityEffect(rule.Effect)}
		target.Action = encodeSecurityAction(rule.Action)
		if rule.Pair != nil {
			target.Resource = &pb.SecurityRule_Pair{Pair: &pb.SecurityPrefixPair{TailPrefix: rule.Pair.Tail, HeadPrefix: rule.Pair.Head}}
		} else if rule.Prefix != nil {
			target.Resource = &pb.SecurityRule_Prefix{Prefix: *rule.Prefix}
		} else {
			target.Resource = &pb.SecurityRule_Global{Global: true}
		}
		result.Rules[i] = target
	}
	return result
}

func encodeSecurityAction(action security.Action) pb.SecurityAction {
	for value, candidate := range securityActions {
		if candidate == action {
			return value
		}
	}
	return pb.SecurityAction_SECURITY_ACTION_UNSPECIFIED
}

// Environment policy locks are response metadata, never client-owned grants.
func encodeSecurityManagedRole(role security.Role, image security.Image) *pb.SecurityRole {
	result := encodeSecurityRole(role)
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
func encodeSecurityEffect(effect security.Effect) pb.SecurityEffect {
	if effect == security.Deny {
		return pb.SecurityEffect_SECURITY_EFFECT_DENY
	}
	return pb.SecurityEffect_SECURITY_EFFECT_ALLOW
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
	case *pb.SecurityChange_PutIssuer:
		result.Kind = security.PutIssuer
		var issuer security.Issuer
		issuer, err = decodeSecurityIssuer(op.PutIssuer)
		result.Issuer = &issuer
		result.PreserveSecret = op.PutIssuer != nil && op.PutIssuer.SecretRef == nil
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
	switch {
	case errors.Is(err, security.ErrStoreUnavailable), errors.Is(err, security.ErrAuthorityUnavailable):
		return connect.CodeUnavailable
	case errors.Is(err, security.ErrRevisionConflict), errors.Is(err, security.ErrChangeConflict):
		return connect.CodeAborted
	case errors.Is(err, security.ErrPermissionDenied):
		return connect.CodePermissionDenied
	case errors.Is(err, security.ErrUnknownChange), errors.Is(err, security.ErrRecentAuthentication), errors.Is(err, security.ErrOperationAuthorization), errors.Is(err, security.ErrBootstrapLocked), errors.Is(err, security.ErrLastAdministrator), errors.Is(err, security.ErrReadOnlyWriter), errors.Is(err, security.ErrUnknownRole):
		return connect.CodeFailedPrecondition
	case errors.Is(err, security.ErrControlReserve):
		return connect.CodeResourceExhausted
	default:
		return connect.CodeInvalidArgument
	}
}
