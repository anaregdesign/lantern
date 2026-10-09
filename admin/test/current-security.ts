import { clone } from "@bufbuild/protobuf";
import {
  CurrentSecurityReviewSchema,
  CurrentSecurityChangeResultSchema,
} from "../../sdks/node/src/gen/graph/v1/security_pb";
import {
  CurrentAuthorizationStopObservation,
  CurrentSecurityDisposition,
  CurrentSecurityProgress,
  SecurityPrincipalKind,
  SecurityEnforcementState,
  SecurityAuthorizationRequirement,
  type CurrentAuthorityProfile,
  type CurrentSemanticCut,
  type CurrentSecurityReview,
  type CurrentSecurityChangeResult,
  type SecurityVersion,
  type SecurityChange,
  type PrepareSecurityChangesResponse,
  type ApplySecurityChangesResponse,
} from "lantern-sdk/web";
export const bytes = (length = 32, value = 1) =>
  new Uint8Array(length).fill(value);
export const profile: CurrentAuthorityProfile = {
  $typeName: "graph.v1.CurrentAuthorityProfile",
  version: 2,
  domain: bytes(),
  cohort: bytes(32, 2),
  generation: bytes(16, 3),
  protocol: bytes(32, 4),
  timeProfile: bytes(32, 5),
  membership: bytes(32, 6),
  configuration: bytes(32, 7),
};
export const cut: CurrentSemanticCut = {
  $typeName: "graph.v1.CurrentSemanticCut",
  version: 1,
  domain: profile.domain.slice(),
  cohort: profile.cohort.slice(),
  generation: profile.generation.slice(),
  sequence: 3n,
  previous: bytes(32, 8),
  projection: bytes(32, 9),
  frontier: bytes(32, 10),
  fences: bytes(32, 11),
  policy: bytes(32, 12),
};
export const version: SecurityVersion = {
  $typeName: "graph.v1.SecurityVersion",
  revision: 0n,
  digest: new Uint8Array(),
  generation: new Uint8Array(),
  currentProfile: profile,
  currentCut: cut,
  admissionBinding: bytes(32, 13),
};
export const changes: SecurityChange[] = [
  {
    $typeName: "graph.v1.SecurityChange",
    operation: { case: "deleteRole", value: "reader" },
  },
];
export function review(nonce = 7): CurrentSecurityReview {
  return clone(CurrentSecurityReviewSchema, {
    $typeName: "graph.v1.CurrentSecurityReview",
    profile,
    expectedCut: cut,
    changeId: {
      $typeName: "graph.v1.CurrentSecurityChangeID",
      version: 1,
      domain: profile.domain,
      cohort: profile.cohort,
      namespace: 19n,
      nonce: bytes(16, nonce),
    },
    actor: {
      $typeName: "graph.v1.SecurityIdentity",
      kind: SecurityPrincipalKind.OIDC,
      issuer: "https://idp.example",
      subject: "alice",
      machineName: "",
    },
    intentDigest: bytes(32, 14),
    changes,
  });
}
export function prepare(
  draft: CurrentSecurityReview,
  requirement = SecurityAuthorizationRequirement.ORDINARY,
  nonce = 7,
): PrepareSecurityChangesResponse {
  return {
    $typeName: "graph.v1.PrepareSecurityChangesResponse",
    changeId: new Uint8Array(),
    intentDigest: new Uint8Array(),
    requirement,
    currentReview: {
      ...review(nonce),
      profile: structuredClone(draft.profile),
      expectedCut: structuredClone(draft.expectedCut),
      changes: structuredClone(draft.changes),
    },
  };
}
export function result(
  r = review(),
  progress = CurrentSecurityProgress.APPLIED,
  disposition = CurrentSecurityDisposition.APPLIED,
): CurrentSecurityChangeResult {
  return clone(CurrentSecurityChangeResultSchema, {
    $typeName: "graph.v1.CurrentSecurityChangeResult",
    profile: r.profile,
    changeId: r.changeId,
    intentDigest: r.intentDigest,
    progress,
    stopObservation: CurrentAuthorizationStopObservation.NOT_OBSERVED,
    original:
      progress === CurrentSecurityProgress.APPLIED
        ? {
            $typeName: "graph.v1.CurrentSecurityOriginalOutcome",
            changeId: r.changeId,
            intentDigest: r.intentDigest,
            handoffDigest: bytes(32, 15),
            disposition,
            commit: {
              $typeName: "graph.v1.CurrentControlCommit",
              version: 1,
              domain: profile.domain,
              cohort: profile.cohort,
              membership: bytes(32, 16),
              configuration: bytes(32, 17),
              slot: 11n,
              value: bytes(32, 18),
            },
            items: r.changes.map((_, index) => ({
              $typeName: "graph.v1.CurrentSecurityItemOutcome" as const,
              index,
              kind: "delete_role",
              disposition,
            })),
            observedCut: r.expectedCut,
            resultingCut: { ...cut, sequence: 4n, projection: bytes(32, 19) },
          }
        : undefined,
  });
}
export function acknowledgement(
  currentResult = result(),
): ApplySecurityChangesResponse {
  return {
    $typeName: "graph.v1.ApplySecurityChangesResponse",
    applied: [],
    replayed: false,
    enforcement: SecurityEnforcementState.UNSPECIFIED,
    currentResult,
  };
}
