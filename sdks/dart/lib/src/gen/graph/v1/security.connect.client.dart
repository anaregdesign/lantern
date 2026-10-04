//
//  Generated code. Do not modify.
//  source: graph/v1/security.proto
//

import "package:connectrpc/connect.dart" as connect;
import "security.pb.dart" as graphv1security;
import "security.connect.spec.dart" as specs;

/// Security is a separate control plane. Logical data RPCs cannot write sys:.
/// User resources contain identity and Role membership, never direct grants.
extension type LanternSecurityServiceClient (connect.Transport _transport) {
  Future<graphv1security.GetAuthCapabilitiesResponse> getAuthCapabilities(
    graphv1security.GetAuthCapabilitiesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getAuthCapabilities,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.GetCurrentPrincipalResponse> getCurrentPrincipal(
    graphv1security.GetCurrentPrincipalRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getCurrentPrincipal,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ListIssuersResponse> listIssuers(
    graphv1security.ListIssuersRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.listIssuers,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.GetIssuerResponse> getIssuer(
    graphv1security.GetIssuerRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getIssuer,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ListRolesResponse> listRoles(
    graphv1security.ListRolesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.listRoles,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.GetRoleResponse> getRole(
    graphv1security.GetRoleRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getRole,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ListUsersResponse> listUsers(
    graphv1security.ListUsersRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.listUsers,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.GetUserResponse> getUser(
    graphv1security.GetUserRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getUser,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ListRoleAssignmentsResponse> listRoleAssignments(
    graphv1security.ListRoleAssignmentsRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.listRoleAssignments,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ListSecurityAuditResponse> listSecurityAudit(
    graphv1security.ListSecurityAuditRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.listSecurityAudit,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.GetRoleTemplatesResponse> getRoleTemplates(
    graphv1security.GetRoleTemplatesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getRoleTemplates,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ExplainAccessResponse> explainAccess(
    graphv1security.ExplainAccessRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.explainAccess,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ValidateIssuerResponse> validateIssuer(
    graphv1security.ValidateIssuerRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.validateIssuer,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  /// Plural is canonical and atomic, with request-index-aligned outcomes.
  Future<graphv1security.ApplySecurityChangesResponse> applySecurityChanges(
    graphv1security.ApplySecurityChangesRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.applySecurityChanges,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.ApplySecurityChangeResponse> applySecurityChange(
    graphv1security.ApplySecurityChangeRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.applySecurityChange,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }

  Future<graphv1security.GetSecurityChangeStatusResponse> getSecurityChangeStatus(
    graphv1security.GetSecurityChangeStatusRequest input, {
    connect.Headers? headers,
    connect.AbortSignal? signal,
    Function(connect.Headers)? onHeader,
    Function(connect.Headers)? onTrailer,
  }) {
    return connect.Client(_transport).unary(
      specs.LanternSecurityService.getSecurityChangeStatus,
      input,
      signal: signal,
      headers: headers,
      onHeader: onHeader,
      onTrailer: onTrailer,
    );
  }
}
