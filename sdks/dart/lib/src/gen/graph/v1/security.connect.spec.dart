//
//  Generated code. Do not modify.
//  source: graph/v1/security.proto
//

import "package:connectrpc/connect.dart" as connect;
import "security.pb.dart" as graphv1security;

/// Security is a separate control plane. Logical data RPCs cannot write sys:.
/// User resources contain identity and Role membership, never direct grants.
abstract final class LanternSecurityService {
  /// Fully-qualified name of the LanternSecurityService service.
  static const name = 'graph.v1.LanternSecurityService';

  static const getAuthCapabilities = connect.Spec(
    '/$name/GetAuthCapabilities',
    connect.StreamType.unary,
    graphv1security.GetAuthCapabilitiesRequest.new,
    graphv1security.GetAuthCapabilitiesResponse.new,
  );

  static const getCurrentPrincipal = connect.Spec(
    '/$name/GetCurrentPrincipal',
    connect.StreamType.unary,
    graphv1security.GetCurrentPrincipalRequest.new,
    graphv1security.GetCurrentPrincipalResponse.new,
  );

  static const listIssuers = connect.Spec(
    '/$name/ListIssuers',
    connect.StreamType.unary,
    graphv1security.ListIssuersRequest.new,
    graphv1security.ListIssuersResponse.new,
  );

  static const getIssuer = connect.Spec(
    '/$name/GetIssuer',
    connect.StreamType.unary,
    graphv1security.GetIssuerRequest.new,
    graphv1security.GetIssuerResponse.new,
  );

  static const listRoles = connect.Spec(
    '/$name/ListRoles',
    connect.StreamType.unary,
    graphv1security.ListRolesRequest.new,
    graphv1security.ListRolesResponse.new,
  );

  static const getRole = connect.Spec(
    '/$name/GetRole',
    connect.StreamType.unary,
    graphv1security.GetRoleRequest.new,
    graphv1security.GetRoleResponse.new,
  );

  static const listUsers = connect.Spec(
    '/$name/ListUsers',
    connect.StreamType.unary,
    graphv1security.ListUsersRequest.new,
    graphv1security.ListUsersResponse.new,
  );

  static const getUser = connect.Spec(
    '/$name/GetUser',
    connect.StreamType.unary,
    graphv1security.GetUserRequest.new,
    graphv1security.GetUserResponse.new,
  );

  static const listRoleAssignments = connect.Spec(
    '/$name/ListRoleAssignments',
    connect.StreamType.unary,
    graphv1security.ListRoleAssignmentsRequest.new,
    graphv1security.ListRoleAssignmentsResponse.new,
  );

  static const listSecurityAudit = connect.Spec(
    '/$name/ListSecurityAudit',
    connect.StreamType.unary,
    graphv1security.ListSecurityAuditRequest.new,
    graphv1security.ListSecurityAuditResponse.new,
  );

  static const getRoleTemplates = connect.Spec(
    '/$name/GetRoleTemplates',
    connect.StreamType.unary,
    graphv1security.GetRoleTemplatesRequest.new,
    graphv1security.GetRoleTemplatesResponse.new,
  );

  static const explainAccess = connect.Spec(
    '/$name/ExplainAccess',
    connect.StreamType.unary,
    graphv1security.ExplainAccessRequest.new,
    graphv1security.ExplainAccessResponse.new,
  );

  static const validateIssuer = connect.Spec(
    '/$name/ValidateIssuer',
    connect.StreamType.unary,
    graphv1security.ValidateIssuerRequest.new,
    graphv1security.ValidateIssuerResponse.new,
  );

  /// Plural is canonical and atomic, with request-index-aligned outcomes.
  static const applySecurityChanges = connect.Spec(
    '/$name/ApplySecurityChanges',
    connect.StreamType.unary,
    graphv1security.ApplySecurityChangesRequest.new,
    graphv1security.ApplySecurityChangesResponse.new,
  );

  static const applySecurityChange = connect.Spec(
    '/$name/ApplySecurityChange',
    connect.StreamType.unary,
    graphv1security.ApplySecurityChangeRequest.new,
    graphv1security.ApplySecurityChangeResponse.new,
  );

  static const getSecurityChangeStatus = connect.Spec(
    '/$name/GetSecurityChangeStatus',
    connect.StreamType.unary,
    graphv1security.GetSecurityChangeStatusRequest.new,
    graphv1security.GetSecurityChangeStatusResponse.new,
  );
}
