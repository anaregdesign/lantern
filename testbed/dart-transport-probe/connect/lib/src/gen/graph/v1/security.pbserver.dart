// This is a generated file - do not edit.
//
// Generated from graph/v1/security.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:protobuf/protobuf.dart' as $pb;

import 'security.pb.dart' as $1;
import 'security.pbjson.dart';

export 'security.pb.dart';

abstract class LanternSecurityServiceBase extends $pb.GeneratedService {
  $async.Future<$1.GetAuthCapabilitiesResponse> getAuthCapabilities(
      $pb.ServerContext ctx, $1.GetAuthCapabilitiesRequest request);
  $async.Future<$1.GetCurrentPrincipalResponse> getCurrentPrincipal(
      $pb.ServerContext ctx, $1.GetCurrentPrincipalRequest request);
  $async.Future<$1.ListIssuersResponse> listIssuers(
      $pb.ServerContext ctx, $1.ListIssuersRequest request);
  $async.Future<$1.GetIssuerResponse> getIssuer(
      $pb.ServerContext ctx, $1.GetIssuerRequest request);
  $async.Future<$1.ListRolesResponse> listRoles(
      $pb.ServerContext ctx, $1.ListRolesRequest request);
  $async.Future<$1.GetRoleResponse> getRole(
      $pb.ServerContext ctx, $1.GetRoleRequest request);
  $async.Future<$1.ListUsersResponse> listUsers(
      $pb.ServerContext ctx, $1.ListUsersRequest request);
  $async.Future<$1.GetUserResponse> getUser(
      $pb.ServerContext ctx, $1.GetUserRequest request);
  $async.Future<$1.ListRoleAssignmentsResponse> listRoleAssignments(
      $pb.ServerContext ctx, $1.ListRoleAssignmentsRequest request);
  $async.Future<$1.ListSecurityAuditResponse> listSecurityAudit(
      $pb.ServerContext ctx, $1.ListSecurityAuditRequest request);
  $async.Future<$1.GetRoleTemplatesResponse> getRoleTemplates(
      $pb.ServerContext ctx, $1.GetRoleTemplatesRequest request);
  $async.Future<$1.ExplainAccessResponse> explainAccess(
      $pb.ServerContext ctx, $1.ExplainAccessRequest request);
  $async.Future<$1.ValidateIssuerResponse> validateIssuer(
      $pb.ServerContext ctx, $1.ValidateIssuerRequest request);
  $async.Future<$1.PrepareSecurityChangesResponse> prepareSecurityChanges(
      $pb.ServerContext ctx, $1.PrepareSecurityChangesRequest request);
  $async.Future<$1.BeginSecurityChangeAuthorizationResponse>
      beginSecurityChangeAuthorization($pb.ServerContext ctx,
          $1.BeginSecurityChangeAuthorizationRequest request);
  $async.Future<$1.GetSecurityChangeAuthorizationResponse>
      getSecurityChangeAuthorization($pb.ServerContext ctx,
          $1.GetSecurityChangeAuthorizationRequest request);
  $async.Future<$1.ApplySecurityChangesResponse> applySecurityChanges(
      $pb.ServerContext ctx, $1.ApplySecurityChangesRequest request);
  $async.Future<$1.ApplySecurityChangeResponse> applySecurityChange(
      $pb.ServerContext ctx, $1.ApplySecurityChangeRequest request);
  $async.Future<$1.GetSecurityChangeStatusResponse> getSecurityChangeStatus(
      $pb.ServerContext ctx, $1.GetSecurityChangeStatusRequest request);

  $pb.GeneratedMessage createRequest($core.String methodName) {
    switch (methodName) {
      case 'GetAuthCapabilities':
        return $1.GetAuthCapabilitiesRequest();
      case 'GetCurrentPrincipal':
        return $1.GetCurrentPrincipalRequest();
      case 'ListIssuers':
        return $1.ListIssuersRequest();
      case 'GetIssuer':
        return $1.GetIssuerRequest();
      case 'ListRoles':
        return $1.ListRolesRequest();
      case 'GetRole':
        return $1.GetRoleRequest();
      case 'ListUsers':
        return $1.ListUsersRequest();
      case 'GetUser':
        return $1.GetUserRequest();
      case 'ListRoleAssignments':
        return $1.ListRoleAssignmentsRequest();
      case 'ListSecurityAudit':
        return $1.ListSecurityAuditRequest();
      case 'GetRoleTemplates':
        return $1.GetRoleTemplatesRequest();
      case 'ExplainAccess':
        return $1.ExplainAccessRequest();
      case 'ValidateIssuer':
        return $1.ValidateIssuerRequest();
      case 'PrepareSecurityChanges':
        return $1.PrepareSecurityChangesRequest();
      case 'BeginSecurityChangeAuthorization':
        return $1.BeginSecurityChangeAuthorizationRequest();
      case 'GetSecurityChangeAuthorization':
        return $1.GetSecurityChangeAuthorizationRequest();
      case 'ApplySecurityChanges':
        return $1.ApplySecurityChangesRequest();
      case 'ApplySecurityChange':
        return $1.ApplySecurityChangeRequest();
      case 'GetSecurityChangeStatus':
        return $1.GetSecurityChangeStatusRequest();
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $async.Future<$pb.GeneratedMessage> handleCall($pb.ServerContext ctx,
      $core.String methodName, $pb.GeneratedMessage request) {
    switch (methodName) {
      case 'GetAuthCapabilities':
        return getAuthCapabilities(
            ctx, request as $1.GetAuthCapabilitiesRequest);
      case 'GetCurrentPrincipal':
        return getCurrentPrincipal(
            ctx, request as $1.GetCurrentPrincipalRequest);
      case 'ListIssuers':
        return listIssuers(ctx, request as $1.ListIssuersRequest);
      case 'GetIssuer':
        return getIssuer(ctx, request as $1.GetIssuerRequest);
      case 'ListRoles':
        return listRoles(ctx, request as $1.ListRolesRequest);
      case 'GetRole':
        return getRole(ctx, request as $1.GetRoleRequest);
      case 'ListUsers':
        return listUsers(ctx, request as $1.ListUsersRequest);
      case 'GetUser':
        return getUser(ctx, request as $1.GetUserRequest);
      case 'ListRoleAssignments':
        return listRoleAssignments(
            ctx, request as $1.ListRoleAssignmentsRequest);
      case 'ListSecurityAudit':
        return listSecurityAudit(ctx, request as $1.ListSecurityAuditRequest);
      case 'GetRoleTemplates':
        return getRoleTemplates(ctx, request as $1.GetRoleTemplatesRequest);
      case 'ExplainAccess':
        return explainAccess(ctx, request as $1.ExplainAccessRequest);
      case 'ValidateIssuer':
        return validateIssuer(ctx, request as $1.ValidateIssuerRequest);
      case 'PrepareSecurityChanges':
        return prepareSecurityChanges(
            ctx, request as $1.PrepareSecurityChangesRequest);
      case 'BeginSecurityChangeAuthorization':
        return beginSecurityChangeAuthorization(
            ctx, request as $1.BeginSecurityChangeAuthorizationRequest);
      case 'GetSecurityChangeAuthorization':
        return getSecurityChangeAuthorization(
            ctx, request as $1.GetSecurityChangeAuthorizationRequest);
      case 'ApplySecurityChanges':
        return applySecurityChanges(
            ctx, request as $1.ApplySecurityChangesRequest);
      case 'ApplySecurityChange':
        return applySecurityChange(
            ctx, request as $1.ApplySecurityChangeRequest);
      case 'GetSecurityChangeStatus':
        return getSecurityChangeStatus(
            ctx, request as $1.GetSecurityChangeStatusRequest);
      default:
        throw $core.ArgumentError('Unknown method: $methodName');
    }
  }

  $core.Map<$core.String, $core.dynamic> get $json =>
      LanternSecurityServiceBase$json;
  $core.Map<$core.String, $core.Map<$core.String, $core.dynamic>>
      get $messageJson => LanternSecurityServiceBase$messageJson;
}
