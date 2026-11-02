import { validateOperationalChanges } from "./security-operational.js";
import { create, fromJsonString, toJsonString, type MessageInitShape } from "@bufbuild/protobuf";
import { reflect, isReflectMessage, type ReflectMessage } from "@bufbuild/protobuf/reflect";
import {
  currentChangeIdBinding,
  currentCutBinding,
  currentSecurityVersionBinding,
} from "./security-current.js";
import {
  BrowserSessionSchema,
  SessionRevocationSchema,
  CurrentLogoutRequestSchema,
  CurrentSecurityReviewSchema,
  CurrentSecurityChangeResultSchema,
  type CurrentSecurityReview,
  type CurrentSecurityChangeResult,
  type GetCurrentPrincipalResponse,
  SecurityPrincipalKind,
  ManagedLogLevel,
  LogSettingsSource,
  LogPolicyAdoptionState,
  LogSettingsApplicationState,
  LogSettingsError,
  type GetLogSettingsResponse,
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
  ManagedLogLevel,
  LogSettingsSource,
  LogPolicyAdoptionState,
  LogSettingsApplicationState,
  LogSettingsError,
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
  SecurityPrincipalUIDPrefix,
  SecurityRegistrationPolicy,
  SecurityRegistrationProfile,
  GetRegistrationPolicyResponse,
  ManagedLogPolicy,
  LogPolicyPreparationReference,
  GetLogSettingsResponse,
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
  const session = fromJsonString(BrowserSessionSchema, json);
  currentUserId(session.principal);
  return session;
}

/** Server-installed self ID; absent for signed-out, UID-less or machine state.
 * This metadata grants no permissions. Never infer it from subject or email. */
export function currentUserId(
  principal: Pick<GetCurrentPrincipalResponse, "identity" | "userId"> | undefined,
): string | undefined {
  const id = principal?.userId;
  if (id === undefined) return undefined;
  if (
    !/^[0-9a-f]{32}$/.test(id) ||
    /^0{32}$/.test(id) ||
    principal?.identity?.kind !== SecurityPrincipalKind.OIDC
  )
    throw new Error("Invalid current user ID.");
  return id;
}

/** Validate contacted-node observations without inferring cluster application. */
export function validateLogSettings(settings: GetLogSettingsResponse): void {
  const fail = (): never => {
    throw new Error("Invalid local log settings contract.");
  };
  const levels = [
    ManagedLogLevel.DEBUG,
    ManagedLogLevel.INFO,
    ManagedLogLevel.WARN,
    ManagedLogLevel.ERROR,
  ];
  const sources = [
    LogSettingsSource.DEFAULT,
    LogSettingsSource.ENVIRONMENT,
    LogSettingsSource.MANAGED,
  ];
  const errors = [
    LogSettingsError.NONE,
    LogSettingsError.INVALID_POLICY,
    LogSettingsError.OWNER_UNAVAILABLE,
    LogSettingsError.APPLY_FAILED,
  ];
  const u64 = 0xffffffffffffffffn;
  if (
    settings.schemaVersion !== 1 ||
    !Number.isInteger(settings.member) ||
    settings.member < 1 ||
    settings.member > 0xffffffff ||
    !levels.includes(settings.effectiveLevel) ||
    !sources.includes(settings.source) ||
    !errors.includes(settings.error) ||
    typeof settings.desiredGeneration !== "bigint" ||
    settings.desiredGeneration < 0n ||
    settings.desiredGeneration > u64 ||
    typeof settings.appliedGeneration !== "bigint" ||
    settings.appliedGeneration < 0n ||
    settings.appliedGeneration > settings.desiredGeneration
  )
    fail();
  currentSecurityVersionBinding(settings.version);
  if (settings.preparation) {
    if (settings.adoption === LogPolicyAdoptionState.UNADOPTED) fail();
    currentChangeIdBinding(settings.preparation.changeId, settings.version?.currentProfile);
    for (const digest of [settings.preparation.intentDigest, settings.preparation.actorDigest]) {
      if (!(digest instanceof Uint8Array) || digest.length !== 32 || !digest.some(Boolean)) fail();
    }
  } else if (settings.adoption === LogPolicyAdoptionState.FENCED_UNADOPTED) fail();
  if (settings.desired) {
    if (
      settings.desired.version !== 1 ||
      !levels.includes(settings.desired.level) ||
      settings.desiredGeneration === 0n ||
      settings.adoption !== LogPolicyAdoptionState.MANAGED
    )
      fail();
  } else if (
    settings.desiredGeneration !== 0n ||
    ![LogPolicyAdoptionState.UNADOPTED, LogPolicyAdoptionState.FENCED_UNADOPTED].includes(
      settings.adoption,
    )
  )
    fail();
  if ((settings.source === LogSettingsSource.MANAGED) !== settings.appliedGeneration > 0n) fail();
  if (settings.appliedGeneration > 0n) {
    currentCutBinding(settings.appliedCut, settings.version?.currentProfile);
    if (
      !settings.appliedCut ||
      !settings.version?.currentCut ||
      settings.appliedCut.sequence > settings.version.currentCut.sequence
    )
      fail();
    if (
      settings.appliedGeneration === settings.desiredGeneration &&
      settings.effectiveLevel !== settings.desired?.level
    )
      fail();
  } else if (settings.appliedCut !== undefined) fail();
  if (settings.error !== LogSettingsError.NONE) {
    if (settings.application !== LogSettingsApplicationState.ERROR) fail();
  } else if (settings.application === LogSettingsApplicationState.PENDING) {
    if (!settings.desired || settings.appliedGeneration >= settings.desiredGeneration) fail();
  } else if (
    settings.application !== LogSettingsApplicationState.APPLIED ||
    settings.appliedGeneration !== settings.desiredGeneration ||
    settings.restartRequired
  )
    fail();
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
  if (
    ["graph.v1.SecurityChange", "graph.v1.OperationalPolicyValues"].includes(
      message.desc.typeName,
    ) &&
    !message.oneofCase(message.oneofs[0]!)
  )
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
  validateOperationalChanges(review.changes);
  return review;
}
export function encodeCurrentSecurityReview(review: CurrentSecurityReview): string {
  validateOperationalChanges(review.changes);
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

export * from "./security-operational.js";
