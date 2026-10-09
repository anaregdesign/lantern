import { create, fromJsonString, toJsonString, type MessageInitShape } from "@bufbuild/protobuf";
import {
  BrowserSessionSchema,
  SessionRevocationSchema,
  CurrentLogoutRequestSchema,
} from "./gen/graph/v1/security_pb.js";
export {
  AuthMode,
  SecurityAction,
  SecurityEffect,
  SecurityEnforcementState,
  SecurityPrincipalKind,
  SecurityPrincipalState,
  SecurityAuthorizationRequirement,
  SecurityAuthorizationState,
  SecurityChangeRejectionReason,
  CurrentSecurityDisposition,
  CurrentSecurityProgress,
  CurrentAuthorizationStopObservation,
} from "./gen/graph/v1/security_pb.js";
export type {
  SecurityOperationAuthorizationRequired,
  SecurityChangePrecommitRejected,
  GetAuthCapabilitiesResponse,
  LoginIssuer,
  GetCurrentPrincipalResponse,
  BrowserSession,
  SessionRevocation,
  SecurityIdentity,
  SecurityRole,
  SecurityRule,
  SecurityPrefixPair,
  SecurityEdgeIdentity,
  SecurityUser,
  SecurityIssuer,
  SecurityRoleAssignment,
  SecurityVersion,
  SecurityChange,
  SecurityAuditRecord,
  ApplySecurityChangesResponse,
  GetSecurityChangeStatusResponse,
  GetRoleTemplatesResponse,
  ListRolesResponse,
  ListIssuersResponse,
  ListUsersResponse,
  ExplainAccessResponse,
  SecurityChangeReview,
  PrepareSecurityChangesResponse,
  BeginSecurityChangeAuthorizationResponse,
  GetSecurityChangeAuthorizationResponse,
  CurrentAuthorityProfile,
  CurrentSemanticCut,
  CurrentSecurityChangeID,
  CurrentSecurityReview,
  CurrentSecurityChangeResult,
  CurrentSecurityOriginalOutcome,
  CurrentSecurityItemOutcome,
  CurrentSecurityAuditRecord,
  CurrentSecurityInvocationRejected,
  CurrentSessionRevocationReview,
  CurrentLogoutRequest,
} from "./gen/graph/v1/security_pb.js";
export function parseBrowserSession(json: string) {
  return fromJsonString(BrowserSessionSchema, json);
}
export function parseSessionRevocation(json: string) {
  return fromJsonString(SessionRevocationSchema, json);
}
export function encodeCurrentLogoutRequest(
  request: MessageInitShape<typeof CurrentLogoutRequestSchema>,
): string {
  return toJsonString(CurrentLogoutRequestSchema, create(CurrentLogoutRequestSchema, request));
}

export * from "./security-current.js";
