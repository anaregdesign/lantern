// This is a generated file - do not edit.
//
// Generated from graph/v1/security.proto.

// @dart = 3.3

// ignore_for_file: annotate_overrides, camel_case_types, comment_references
// ignore_for_file: constant_identifier_names
// ignore_for_file: curly_braces_in_flow_control_structures
// ignore_for_file: deprecated_member_use_from_same_package, library_prefixes
// ignore_for_file: non_constant_identifier_names, prefer_relative_imports

import 'dart:async' as $async;
import 'dart:core' as $core;

import 'package:grpc/service_api.dart' as $grpc;
import 'package:protobuf/protobuf.dart' as $pb;

import 'security.pb.dart' as $0;

export 'security.pb.dart';

/// Security is a separate control plane. Logical data RPCs cannot write sys:.
/// User resources contain identity and Role membership, never direct grants.
@$pb.GrpcServiceName('graph.v1.LanternSecurityService')
class LanternSecurityServiceClient extends $grpc.Client {
  /// The hostname for this service.
  static const $core.String defaultHost = '';

  /// OAuth scopes needed for the client.
  static const $core.List<$core.String> oauthScopes = [
    '',
  ];

  LanternSecurityServiceClient(super.channel,
      {super.options, super.interceptors});

  $grpc.ResponseFuture<$0.GetAuthCapabilitiesResponse> getAuthCapabilities(
    $0.GetAuthCapabilitiesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getAuthCapabilities, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetCurrentPrincipalResponse> getCurrentPrincipal(
    $0.GetCurrentPrincipalRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getCurrentPrincipal, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListIssuersResponse> listIssuers(
    $0.ListIssuersRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listIssuers, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetIssuerResponse> getIssuer(
    $0.GetIssuerRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getIssuer, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListRolesResponse> listRoles(
    $0.ListRolesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listRoles, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetRoleResponse> getRole(
    $0.GetRoleRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getRole, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListUsersResponse> listUsers(
    $0.ListUsersRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listUsers, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetUserResponse> getUser(
    $0.GetUserRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getUser, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListRoleAssignmentsResponse> listRoleAssignments(
    $0.ListRoleAssignmentsRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listRoleAssignments, request, options: options);
  }

  $grpc.ResponseFuture<$0.ListSecurityAuditResponse> listSecurityAudit(
    $0.ListSecurityAuditRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$listSecurityAudit, request, options: options);
  }

  $grpc.ResponseFuture<$0.GetRoleTemplatesResponse> getRoleTemplates(
    $0.GetRoleTemplatesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getRoleTemplates, request, options: options);
  }

  $grpc.ResponseFuture<$0.ExplainAccessResponse> explainAccess(
    $0.ExplainAccessRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$explainAccess, request, options: options);
  }

  $grpc.ResponseFuture<$0.ValidateIssuerResponse> validateIssuer(
    $0.ValidateIssuerRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$validateIssuer, request, options: options);
  }

  /// Nonmutating authoritative review; approval never rotates a session.
  $grpc.ResponseFuture<$0.PrepareSecurityChangesResponse>
      prepareSecurityChanges(
    $0.PrepareSecurityChangesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$prepareSecurityChanges, request,
        options: options);
  }

  $grpc.ResponseFuture<$0.BeginSecurityChangeAuthorizationResponse>
      beginSecurityChangeAuthorization(
    $0.BeginSecurityChangeAuthorizationRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$beginSecurityChangeAuthorization, request,
        options: options);
  }

  $grpc.ResponseFuture<$0.GetSecurityChangeAuthorizationResponse>
      getSecurityChangeAuthorization(
    $0.GetSecurityChangeAuthorizationRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getSecurityChangeAuthorization, request,
        options: options);
  }

  /// Plural is canonical and atomic, with request-index-aligned outcomes.
  $grpc.ResponseFuture<$0.ApplySecurityChangesResponse> applySecurityChanges(
    $0.ApplySecurityChangesRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$applySecurityChanges, request, options: options);
  }

  $grpc.ResponseFuture<$0.ApplySecurityChangeResponse> applySecurityChange(
    $0.ApplySecurityChangeRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$applySecurityChange, request, options: options);
  }

  /// Retained commit proof only; item outcomes and replay acknowledgement belong to Apply.
  $grpc.ResponseFuture<$0.GetSecurityChangeStatusResponse>
      getSecurityChangeStatus(
    $0.GetSecurityChangeStatusRequest request, {
    $grpc.CallOptions? options,
  }) {
    return $createUnaryCall(_$getSecurityChangeStatus, request,
        options: options);
  }

  // method descriptors

  static final _$getAuthCapabilities = $grpc.ClientMethod<
          $0.GetAuthCapabilitiesRequest, $0.GetAuthCapabilitiesResponse>(
      '/graph.v1.LanternSecurityService/GetAuthCapabilities',
      ($0.GetAuthCapabilitiesRequest value) => value.writeToBuffer(),
      $0.GetAuthCapabilitiesResponse.fromBuffer);
  static final _$getCurrentPrincipal = $grpc.ClientMethod<
          $0.GetCurrentPrincipalRequest, $0.GetCurrentPrincipalResponse>(
      '/graph.v1.LanternSecurityService/GetCurrentPrincipal',
      ($0.GetCurrentPrincipalRequest value) => value.writeToBuffer(),
      $0.GetCurrentPrincipalResponse.fromBuffer);
  static final _$listIssuers =
      $grpc.ClientMethod<$0.ListIssuersRequest, $0.ListIssuersResponse>(
          '/graph.v1.LanternSecurityService/ListIssuers',
          ($0.ListIssuersRequest value) => value.writeToBuffer(),
          $0.ListIssuersResponse.fromBuffer);
  static final _$getIssuer =
      $grpc.ClientMethod<$0.GetIssuerRequest, $0.GetIssuerResponse>(
          '/graph.v1.LanternSecurityService/GetIssuer',
          ($0.GetIssuerRequest value) => value.writeToBuffer(),
          $0.GetIssuerResponse.fromBuffer);
  static final _$listRoles =
      $grpc.ClientMethod<$0.ListRolesRequest, $0.ListRolesResponse>(
          '/graph.v1.LanternSecurityService/ListRoles',
          ($0.ListRolesRequest value) => value.writeToBuffer(),
          $0.ListRolesResponse.fromBuffer);
  static final _$getRole =
      $grpc.ClientMethod<$0.GetRoleRequest, $0.GetRoleResponse>(
          '/graph.v1.LanternSecurityService/GetRole',
          ($0.GetRoleRequest value) => value.writeToBuffer(),
          $0.GetRoleResponse.fromBuffer);
  static final _$listUsers =
      $grpc.ClientMethod<$0.ListUsersRequest, $0.ListUsersResponse>(
          '/graph.v1.LanternSecurityService/ListUsers',
          ($0.ListUsersRequest value) => value.writeToBuffer(),
          $0.ListUsersResponse.fromBuffer);
  static final _$getUser =
      $grpc.ClientMethod<$0.GetUserRequest, $0.GetUserResponse>(
          '/graph.v1.LanternSecurityService/GetUser',
          ($0.GetUserRequest value) => value.writeToBuffer(),
          $0.GetUserResponse.fromBuffer);
  static final _$listRoleAssignments = $grpc.ClientMethod<
          $0.ListRoleAssignmentsRequest, $0.ListRoleAssignmentsResponse>(
      '/graph.v1.LanternSecurityService/ListRoleAssignments',
      ($0.ListRoleAssignmentsRequest value) => value.writeToBuffer(),
      $0.ListRoleAssignmentsResponse.fromBuffer);
  static final _$listSecurityAudit = $grpc.ClientMethod<
          $0.ListSecurityAuditRequest, $0.ListSecurityAuditResponse>(
      '/graph.v1.LanternSecurityService/ListSecurityAudit',
      ($0.ListSecurityAuditRequest value) => value.writeToBuffer(),
      $0.ListSecurityAuditResponse.fromBuffer);
  static final _$getRoleTemplates = $grpc.ClientMethod<
          $0.GetRoleTemplatesRequest, $0.GetRoleTemplatesResponse>(
      '/graph.v1.LanternSecurityService/GetRoleTemplates',
      ($0.GetRoleTemplatesRequest value) => value.writeToBuffer(),
      $0.GetRoleTemplatesResponse.fromBuffer);
  static final _$explainAccess =
      $grpc.ClientMethod<$0.ExplainAccessRequest, $0.ExplainAccessResponse>(
          '/graph.v1.LanternSecurityService/ExplainAccess',
          ($0.ExplainAccessRequest value) => value.writeToBuffer(),
          $0.ExplainAccessResponse.fromBuffer);
  static final _$validateIssuer =
      $grpc.ClientMethod<$0.ValidateIssuerRequest, $0.ValidateIssuerResponse>(
          '/graph.v1.LanternSecurityService/ValidateIssuer',
          ($0.ValidateIssuerRequest value) => value.writeToBuffer(),
          $0.ValidateIssuerResponse.fromBuffer);
  static final _$prepareSecurityChanges = $grpc.ClientMethod<
          $0.PrepareSecurityChangesRequest, $0.PrepareSecurityChangesResponse>(
      '/graph.v1.LanternSecurityService/PrepareSecurityChanges',
      ($0.PrepareSecurityChangesRequest value) => value.writeToBuffer(),
      $0.PrepareSecurityChangesResponse.fromBuffer);
  static final _$beginSecurityChangeAuthorization = $grpc.ClientMethod<
          $0.BeginSecurityChangeAuthorizationRequest,
          $0.BeginSecurityChangeAuthorizationResponse>(
      '/graph.v1.LanternSecurityService/BeginSecurityChangeAuthorization',
      ($0.BeginSecurityChangeAuthorizationRequest value) =>
          value.writeToBuffer(),
      $0.BeginSecurityChangeAuthorizationResponse.fromBuffer);
  static final _$getSecurityChangeAuthorization = $grpc.ClientMethod<
          $0.GetSecurityChangeAuthorizationRequest,
          $0.GetSecurityChangeAuthorizationResponse>(
      '/graph.v1.LanternSecurityService/GetSecurityChangeAuthorization',
      ($0.GetSecurityChangeAuthorizationRequest value) => value.writeToBuffer(),
      $0.GetSecurityChangeAuthorizationResponse.fromBuffer);
  static final _$applySecurityChanges = $grpc.ClientMethod<
          $0.ApplySecurityChangesRequest, $0.ApplySecurityChangesResponse>(
      '/graph.v1.LanternSecurityService/ApplySecurityChanges',
      ($0.ApplySecurityChangesRequest value) => value.writeToBuffer(),
      $0.ApplySecurityChangesResponse.fromBuffer);
  static final _$applySecurityChange = $grpc.ClientMethod<
          $0.ApplySecurityChangeRequest, $0.ApplySecurityChangeResponse>(
      '/graph.v1.LanternSecurityService/ApplySecurityChange',
      ($0.ApplySecurityChangeRequest value) => value.writeToBuffer(),
      $0.ApplySecurityChangeResponse.fromBuffer);
  static final _$getSecurityChangeStatus = $grpc.ClientMethod<
          $0.GetSecurityChangeStatusRequest,
          $0.GetSecurityChangeStatusResponse>(
      '/graph.v1.LanternSecurityService/GetSecurityChangeStatus',
      ($0.GetSecurityChangeStatusRequest value) => value.writeToBuffer(),
      $0.GetSecurityChangeStatusResponse.fromBuffer);
}

@$pb.GrpcServiceName('graph.v1.LanternSecurityService')
abstract class LanternSecurityServiceBase extends $grpc.Service {
  $core.String get $name => 'graph.v1.LanternSecurityService';

  LanternSecurityServiceBase() {
    $addMethod($grpc.ServiceMethod<$0.GetAuthCapabilitiesRequest,
            $0.GetAuthCapabilitiesResponse>(
        'GetAuthCapabilities',
        getAuthCapabilities_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetAuthCapabilitiesRequest.fromBuffer(value),
        ($0.GetAuthCapabilitiesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetCurrentPrincipalRequest,
            $0.GetCurrentPrincipalResponse>(
        'GetCurrentPrincipal',
        getCurrentPrincipal_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetCurrentPrincipalRequest.fromBuffer(value),
        ($0.GetCurrentPrincipalResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.ListIssuersRequest, $0.ListIssuersResponse>(
            'ListIssuers',
            listIssuers_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.ListIssuersRequest.fromBuffer(value),
            ($0.ListIssuersResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetIssuerRequest, $0.GetIssuerResponse>(
        'GetIssuer',
        getIssuer_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetIssuerRequest.fromBuffer(value),
        ($0.GetIssuerResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListRolesRequest, $0.ListRolesResponse>(
        'ListRoles',
        listRoles_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.ListRolesRequest.fromBuffer(value),
        ($0.ListRolesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetRoleRequest, $0.GetRoleResponse>(
        'GetRole',
        getRole_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetRoleRequest.fromBuffer(value),
        ($0.GetRoleResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListUsersRequest, $0.ListUsersResponse>(
        'ListUsers',
        listUsers_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.ListUsersRequest.fromBuffer(value),
        ($0.ListUsersResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetUserRequest, $0.GetUserResponse>(
        'GetUser',
        getUser_Pre,
        false,
        false,
        ($core.List<$core.int> value) => $0.GetUserRequest.fromBuffer(value),
        ($0.GetUserResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListRoleAssignmentsRequest,
            $0.ListRoleAssignmentsResponse>(
        'ListRoleAssignments',
        listRoleAssignments_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ListRoleAssignmentsRequest.fromBuffer(value),
        ($0.ListRoleAssignmentsResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ListSecurityAuditRequest,
            $0.ListSecurityAuditResponse>(
        'ListSecurityAudit',
        listSecurityAudit_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ListSecurityAuditRequest.fromBuffer(value),
        ($0.ListSecurityAuditResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetRoleTemplatesRequest,
            $0.GetRoleTemplatesResponse>(
        'GetRoleTemplates',
        getRoleTemplates_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetRoleTemplatesRequest.fromBuffer(value),
        ($0.GetRoleTemplatesResponse value) => value.writeToBuffer()));
    $addMethod(
        $grpc.ServiceMethod<$0.ExplainAccessRequest, $0.ExplainAccessResponse>(
            'ExplainAccess',
            explainAccess_Pre,
            false,
            false,
            ($core.List<$core.int> value) =>
                $0.ExplainAccessRequest.fromBuffer(value),
            ($0.ExplainAccessResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ValidateIssuerRequest,
            $0.ValidateIssuerResponse>(
        'ValidateIssuer',
        validateIssuer_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ValidateIssuerRequest.fromBuffer(value),
        ($0.ValidateIssuerResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.PrepareSecurityChangesRequest,
            $0.PrepareSecurityChangesResponse>(
        'PrepareSecurityChanges',
        prepareSecurityChanges_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.PrepareSecurityChangesRequest.fromBuffer(value),
        ($0.PrepareSecurityChangesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.BeginSecurityChangeAuthorizationRequest,
            $0.BeginSecurityChangeAuthorizationResponse>(
        'BeginSecurityChangeAuthorization',
        beginSecurityChangeAuthorization_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.BeginSecurityChangeAuthorizationRequest.fromBuffer(value),
        ($0.BeginSecurityChangeAuthorizationResponse value) =>
            value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetSecurityChangeAuthorizationRequest,
            $0.GetSecurityChangeAuthorizationResponse>(
        'GetSecurityChangeAuthorization',
        getSecurityChangeAuthorization_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetSecurityChangeAuthorizationRequest.fromBuffer(value),
        ($0.GetSecurityChangeAuthorizationResponse value) =>
            value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ApplySecurityChangesRequest,
            $0.ApplySecurityChangesResponse>(
        'ApplySecurityChanges',
        applySecurityChanges_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ApplySecurityChangesRequest.fromBuffer(value),
        ($0.ApplySecurityChangesResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.ApplySecurityChangeRequest,
            $0.ApplySecurityChangeResponse>(
        'ApplySecurityChange',
        applySecurityChange_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.ApplySecurityChangeRequest.fromBuffer(value),
        ($0.ApplySecurityChangeResponse value) => value.writeToBuffer()));
    $addMethod($grpc.ServiceMethod<$0.GetSecurityChangeStatusRequest,
            $0.GetSecurityChangeStatusResponse>(
        'GetSecurityChangeStatus',
        getSecurityChangeStatus_Pre,
        false,
        false,
        ($core.List<$core.int> value) =>
            $0.GetSecurityChangeStatusRequest.fromBuffer(value),
        ($0.GetSecurityChangeStatusResponse value) => value.writeToBuffer()));
  }

  $async.Future<$0.GetAuthCapabilitiesResponse> getAuthCapabilities_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetAuthCapabilitiesRequest> $request) async {
    return getAuthCapabilities($call, await $request);
  }

  $async.Future<$0.GetAuthCapabilitiesResponse> getAuthCapabilities(
      $grpc.ServiceCall call, $0.GetAuthCapabilitiesRequest request);

  $async.Future<$0.GetCurrentPrincipalResponse> getCurrentPrincipal_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetCurrentPrincipalRequest> $request) async {
    return getCurrentPrincipal($call, await $request);
  }

  $async.Future<$0.GetCurrentPrincipalResponse> getCurrentPrincipal(
      $grpc.ServiceCall call, $0.GetCurrentPrincipalRequest request);

  $async.Future<$0.ListIssuersResponse> listIssuers_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ListIssuersRequest> $request) async {
    return listIssuers($call, await $request);
  }

  $async.Future<$0.ListIssuersResponse> listIssuers(
      $grpc.ServiceCall call, $0.ListIssuersRequest request);

  $async.Future<$0.GetIssuerResponse> getIssuer_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetIssuerRequest> $request) async {
    return getIssuer($call, await $request);
  }

  $async.Future<$0.GetIssuerResponse> getIssuer(
      $grpc.ServiceCall call, $0.GetIssuerRequest request);

  $async.Future<$0.ListRolesResponse> listRoles_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ListRolesRequest> $request) async {
    return listRoles($call, await $request);
  }

  $async.Future<$0.ListRolesResponse> listRoles(
      $grpc.ServiceCall call, $0.ListRolesRequest request);

  $async.Future<$0.GetRoleResponse> getRole_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetRoleRequest> $request) async {
    return getRole($call, await $request);
  }

  $async.Future<$0.GetRoleResponse> getRole(
      $grpc.ServiceCall call, $0.GetRoleRequest request);

  $async.Future<$0.ListUsersResponse> listUsers_Pre($grpc.ServiceCall $call,
      $async.Future<$0.ListUsersRequest> $request) async {
    return listUsers($call, await $request);
  }

  $async.Future<$0.ListUsersResponse> listUsers(
      $grpc.ServiceCall call, $0.ListUsersRequest request);

  $async.Future<$0.GetUserResponse> getUser_Pre($grpc.ServiceCall $call,
      $async.Future<$0.GetUserRequest> $request) async {
    return getUser($call, await $request);
  }

  $async.Future<$0.GetUserResponse> getUser(
      $grpc.ServiceCall call, $0.GetUserRequest request);

  $async.Future<$0.ListRoleAssignmentsResponse> listRoleAssignments_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ListRoleAssignmentsRequest> $request) async {
    return listRoleAssignments($call, await $request);
  }

  $async.Future<$0.ListRoleAssignmentsResponse> listRoleAssignments(
      $grpc.ServiceCall call, $0.ListRoleAssignmentsRequest request);

  $async.Future<$0.ListSecurityAuditResponse> listSecurityAudit_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ListSecurityAuditRequest> $request) async {
    return listSecurityAudit($call, await $request);
  }

  $async.Future<$0.ListSecurityAuditResponse> listSecurityAudit(
      $grpc.ServiceCall call, $0.ListSecurityAuditRequest request);

  $async.Future<$0.GetRoleTemplatesResponse> getRoleTemplates_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetRoleTemplatesRequest> $request) async {
    return getRoleTemplates($call, await $request);
  }

  $async.Future<$0.GetRoleTemplatesResponse> getRoleTemplates(
      $grpc.ServiceCall call, $0.GetRoleTemplatesRequest request);

  $async.Future<$0.ExplainAccessResponse> explainAccess_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ExplainAccessRequest> $request) async {
    return explainAccess($call, await $request);
  }

  $async.Future<$0.ExplainAccessResponse> explainAccess(
      $grpc.ServiceCall call, $0.ExplainAccessRequest request);

  $async.Future<$0.ValidateIssuerResponse> validateIssuer_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ValidateIssuerRequest> $request) async {
    return validateIssuer($call, await $request);
  }

  $async.Future<$0.ValidateIssuerResponse> validateIssuer(
      $grpc.ServiceCall call, $0.ValidateIssuerRequest request);

  $async.Future<$0.PrepareSecurityChangesResponse> prepareSecurityChanges_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.PrepareSecurityChangesRequest> $request) async {
    return prepareSecurityChanges($call, await $request);
  }

  $async.Future<$0.PrepareSecurityChangesResponse> prepareSecurityChanges(
      $grpc.ServiceCall call, $0.PrepareSecurityChangesRequest request);

  $async.Future<$0.BeginSecurityChangeAuthorizationResponse>
      beginSecurityChangeAuthorization_Pre(
          $grpc.ServiceCall $call,
          $async.Future<$0.BeginSecurityChangeAuthorizationRequest>
              $request) async {
    return beginSecurityChangeAuthorization($call, await $request);
  }

  $async.Future<$0.BeginSecurityChangeAuthorizationResponse>
      beginSecurityChangeAuthorization($grpc.ServiceCall call,
          $0.BeginSecurityChangeAuthorizationRequest request);

  $async.Future<$0.GetSecurityChangeAuthorizationResponse>
      getSecurityChangeAuthorization_Pre(
          $grpc.ServiceCall $call,
          $async.Future<$0.GetSecurityChangeAuthorizationRequest>
              $request) async {
    return getSecurityChangeAuthorization($call, await $request);
  }

  $async.Future<$0.GetSecurityChangeAuthorizationResponse>
      getSecurityChangeAuthorization($grpc.ServiceCall call,
          $0.GetSecurityChangeAuthorizationRequest request);

  $async.Future<$0.ApplySecurityChangesResponse> applySecurityChanges_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ApplySecurityChangesRequest> $request) async {
    return applySecurityChanges($call, await $request);
  }

  $async.Future<$0.ApplySecurityChangesResponse> applySecurityChanges(
      $grpc.ServiceCall call, $0.ApplySecurityChangesRequest request);

  $async.Future<$0.ApplySecurityChangeResponse> applySecurityChange_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.ApplySecurityChangeRequest> $request) async {
    return applySecurityChange($call, await $request);
  }

  $async.Future<$0.ApplySecurityChangeResponse> applySecurityChange(
      $grpc.ServiceCall call, $0.ApplySecurityChangeRequest request);

  $async.Future<$0.GetSecurityChangeStatusResponse> getSecurityChangeStatus_Pre(
      $grpc.ServiceCall $call,
      $async.Future<$0.GetSecurityChangeStatusRequest> $request) async {
    return getSecurityChangeStatus($call, await $request);
  }

  $async.Future<$0.GetSecurityChangeStatusResponse> getSecurityChangeStatus(
      $grpc.ServiceCall call, $0.GetSecurityChangeStatusRequest request);
}
