import { create, fromJsonString, toJsonString, type MessageInitShape } from "@bufbuild/protobuf";
import { reflect, isReflectMessage, type ReflectMessage } from "@bufbuild/protobuf/reflect";
import {
  BrowserSessionSchema,
  SessionRevocationSchema,
  CurrentLogoutRequestSchema,
  CurrentSecurityReviewSchema,
  CurrentSecurityChangeResultSchema,
  type CurrentSecurityReview,
  type CurrentSecurityChangeResult,
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

// Generated JSON codecs preserve byte strings and full uint64 values. Reject
// unsupported nested enums/operations before using a locally retained contract.
function knownRecoveryMessage(message: ReflectMessage): void {
  if (message.desc.typeName === "graph.v1.SecurityChange" && !message.oneofCase(message.oneofs[0]!))
    throw new Error("Invalid retained security operation.");
  for (const field of message.fields) {
    if (!message.isSet(field)) continue;
    if (field.fieldKind === "enum") {
      if (!field.enum.values.some((value) => value.number === message.get(field)))
        throw new Error("Unsupported retained security enum.");
    } else if (field.fieldKind === "message") {
      knownRecoveryMessage(message.get(field));
    } else if (field.fieldKind === "list" && field.listKind === "message") {
      for (const value of message.get(field)) {
        if (!isReflectMessage(value)) throw new Error("Invalid retained security message.");
        knownRecoveryMessage(value);
      }
    } else if (field.fieldKind === "list" && field.listKind === "enum") {
      for (const value of message.get(field))
        if (!field.enum.values.some((entry) => entry.number === value))
          throw new Error("Unsupported retained security enum.");
    }
  }
}

/** Private recovery metadata; parsing never grants authority or approves Apply. */
export function parseCurrentSecurityReview(json: string): CurrentSecurityReview {
  const review = fromJsonString(CurrentSecurityReviewSchema, json);
  knownRecoveryMessage(reflect(CurrentSecurityReviewSchema, review));
  return review;
}
export function encodeCurrentSecurityReview(review: CurrentSecurityReview): string {
  knownRecoveryMessage(reflect(CurrentSecurityReviewSchema, review));
  return toJsonString(CurrentSecurityReviewSchema, review);
}
export function parseCurrentSecurityChangeResult(json: string): CurrentSecurityChangeResult {
  const result = fromJsonString(CurrentSecurityChangeResultSchema, json);
  knownRecoveryMessage(reflect(CurrentSecurityChangeResultSchema, result));
  return result;
}
export function encodeCurrentSecurityChangeResult(result: CurrentSecurityChangeResult): string {
  knownRecoveryMessage(reflect(CurrentSecurityChangeResultSchema, result));
  return toJsonString(CurrentSecurityChangeResultSchema, result);
}

export * from "./security-current.js";
