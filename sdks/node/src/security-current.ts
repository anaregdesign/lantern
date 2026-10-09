import { createRegistry, fromBinary, toBinary, type Message } from "@bufbuild/protobuf";
import {
  file_graph_v1_security,
  CurrentSecurityOriginalOutcomeSchema,
  SecurityChangeSchema,
  CurrentSecurityProgress,
  CurrentSecurityDisposition,
  CurrentAuthorizationStopObservation,
  SecurityPrincipalKind,
  type CurrentAuthorityProfile,
  type CurrentSemanticCut,
  type CurrentSecurityChangeID,
  type CurrentSecurityReview,
  type CurrentSecurityChangeResult,
  type SecurityVersion,
  type SecurityChange,
  type CurrentSessionRevocationReview,
} from "./gen/graph/v1/security_pb.js";

const securityContracts = createRegistry(file_graph_v1_security);
/** Detach retained wire contracts through their canonical protobuf codec.
 * This also normalizes Node/Bun pooled Buffer views without structuredClone's
 * host-buffer serialization behavior. Plain recovery records own these messages.
 */
export function copySecurityContract<T>(value: T): T {
  if (value === null || value === undefined || typeof value !== "object") return value;
  if (value instanceof Uint8Array) return new Uint8Array(value) as T;
  if (Array.isArray(value)) return value.map(copySecurityContract) as T;
  const typeName = (value as { $typeName?: string }).$typeName;
  if (typeName) {
    const schema = securityContracts.getMessage(typeName);
    if (!schema) throw new Error("Unknown security contract.");
    return fromBinary(schema, toBinary(schema, value as unknown as Message)) as T;
  }
  if (Object.getPrototypeOf(value) !== Object.prototype)
    throw new Error("Invalid security recovery record.");
  return Object.fromEntries(
    Object.entries(value).map(([key, item]) => [key, copySecurityContract(item)]),
  ) as T;
}

const u64 = 0xffffffffffffffffn;
function invalid(): never {
  throw new Error("Invalid current authority contract.");
}
function bytes(value: Uint8Array | undefined, size: number, nonzero = true): string {
  if (!(value instanceof Uint8Array) || value.length !== size || (nonzero && !value.some(Boolean)))
    invalid();
  return Array.from(value, (byte) => byte.toString(16).padStart(2, "0")).join("");
}
function count(value: bigint, zero = false): string {
  if (typeof value !== "bigint" || value < (zero ? 0n : 1n) || value > u64) invalid();
  return value.toString();
}

/** Complete configuration identity for cache partitioning, never authority. */
export function currentProfileBinding(profile: CurrentAuthorityProfile | undefined): string {
  if (!profile || profile.version !== 2) invalid();
  return [
    "current-v2",
    bytes(profile.domain, 32),
    bytes(profile.cohort, 32),
    bytes(profile.generation, 16),
    bytes(profile.protocol, 32),
    bytes(profile.timeProfile, 32),
    bytes(profile.membership, 32),
    bytes(profile.configuration, 32),
  ].join(":");
}

/** Equality covers the whole semantic cut; its sequence is not a global order. */
export function currentCutBinding(
  cut: CurrentSemanticCut | undefined,
  profile: CurrentAuthorityProfile | undefined,
): string {
  currentProfileBinding(profile);
  if (!cut || cut.version !== 1 || !profile) invalid();
  if (
    bytes(cut.domain, 32) !== bytes(profile.domain, 32) ||
    bytes(cut.cohort, 32) !== bytes(profile.cohort, 32) ||
    bytes(cut.generation, 16) !== bytes(profile.generation, 16)
  )
    invalid();
  return [
    "cut-v1",
    bytes(cut.domain, 32),
    bytes(cut.cohort, 32),
    bytes(cut.generation, 16),
    count(cut.sequence),
    bytes(cut.previous, 32, false),
    bytes(cut.projection, 32),
    bytes(cut.frontier, 32),
    bytes(cut.fences, 32),
    bytes(cut.policy, 32),
  ].join(":");
}

export function currentChangeIdBinding(
  id: CurrentSecurityChangeID | undefined,
  profile: CurrentAuthorityProfile | undefined,
): string {
  currentProfileBinding(profile);
  if (
    !id ||
    id.version !== 1 ||
    !profile ||
    bytes(id.domain, 32) !== bytes(profile.domain, 32) ||
    bytes(id.cohort, 32) !== bytes(profile.cohort, 32)
  )
    invalid();
  return [
    "change-v1",
    bytes(id.domain, 32),
    bytes(id.cohort, 32),
    count(id.namespace),
    bytes(id.nonce, 16),
  ].join(":");
}

/** Profile, complete cut and credential/lineage commitment form a cache key.
 * A timer or a previously returned key never grants server authorization. */
export function currentSecurityVersionBinding(version: SecurityVersion | undefined): string {
  if (!version || version.revision !== 0n || version.digest.length || version.generation.length)
    invalid();
  return [
    currentProfileBinding(version.currentProfile),
    currentCutBinding(version.currentCut, version.currentProfile),
    bytes(version.admissionBinding, 32),
  ].join("/");
}

export function validateCurrentReview(
  review: CurrentSecurityReview | undefined,
): asserts review is CurrentSecurityReview {
  if (
    !review ||
    review.changes.length < 1 ||
    review.changes.length > 64 ||
    review.actor?.kind !== SecurityPrincipalKind.OIDC ||
    !review.actor.issuer ||
    !review.actor.subject
  )
    invalid();
  currentProfileBinding(review.profile);
  currentCutBinding(review.expectedCut, review.profile);
  currentChangeIdBinding(review.changeId, review.profile);
  bytes(review.intentDigest, 32);
}

export type CurrentOriginalReference = Pick<
  CurrentSecurityReview,
  "profile" | "changeId" | "intentDigest"
>;

export function currentOriginalBinding(reference: CurrentOriginalReference): string {
  return [
    currentProfileBinding(reference.profile),
    currentChangeIdBinding(reference.changeId, reference.profile),
    bytes(reference.intentDigest, 32),
  ].join("/");
}

export function validateCurrentSessionReview(
  review: CurrentSessionRevocationReview | undefined,
): asserts review is CurrentSessionRevocationReview {
  if (
    !review ||
    review.actor?.kind !== SecurityPrincipalKind.OIDC ||
    !review.actor.issuer ||
    !review.actor.subject ||
    !/^[a-f0-9]{64}$/.test(review.sessionDigest)
  )
    invalid();
  currentOriginalBinding(review);
  currentCutBinding(review.expectedCut, review.profile);
  count(review.sessionLineage);
}

/** Preparation may fill only the origin-minted identity, actor and intent. */
export function validatePreparedCurrentReview(
  review: CurrentSecurityReview | undefined,
  expected: Pick<CurrentSecurityReview, "profile" | "expectedCut"> & {
    changes: readonly SecurityChange[];
  },
): asserts review is CurrentSecurityReview {
  validateCurrentReview(review);
  if (
    currentProfileBinding(review.profile) !== currentProfileBinding(expected.profile) ||
    currentCutBinding(review.expectedCut, review.profile) !==
      currentCutBinding(expected.expectedCut, expected.profile) ||
    review.changes.length !== expected.changes.length
  )
    invalid();
  review.changes.forEach((change, index) => {
    const left = toBinary(SecurityChangeSchema, change);
    const right = toBinary(SecurityChangeSchema, expected.changes[index]!);
    if (left.length !== right.length || left.some((byte, i) => byte !== right[i])) invalid();
  });
}

/** Validate evidence without converting unresolved/durable/chosen into success.
 * An original result is distinct from the caller's current effective policy. */
export function validateCurrentResult(
  result: CurrentSecurityChangeResult | undefined,
  expected: CurrentOriginalReference,
): asserts result is CurrentSecurityChangeResult {
  if (
    !result ||
    currentProfileBinding(result.profile) !== currentProfileBinding(expected.profile) ||
    currentChangeIdBinding(result.changeId, result.profile) !==
      currentChangeIdBinding(expected.changeId, expected.profile) ||
    bytes(result.intentDigest, 32) !== bytes(expected.intentDigest, 32) ||
    ![
      CurrentSecurityProgress.UNRESOLVED,
      CurrentSecurityProgress.ORIGIN_DURABLE,
      CurrentSecurityProgress.CHOSEN,
      CurrentSecurityProgress.APPLIED,
    ].includes(result.progress) ||
    ![
      CurrentAuthorizationStopObservation.NOT_OBSERVED,
      CurrentAuthorizationStopObservation.WAITING,
      CurrentAuthorizationStopObservation.OLD_CUT_NEW_AUTHORIZATIONS_STOPPED,
    ].includes(result.stopObservation)
  )
    invalid();
  const original = result.original;
  if ((result.progress === CurrentSecurityProgress.APPLIED) !== !!original) invalid();
  if (!original) {
    if (result.stopObservation !== CurrentAuthorizationStopObservation.NOT_OBSERVED) invalid();
    return;
  }
  if (
    currentChangeIdBinding(original.changeId, result.profile) !==
      currentChangeIdBinding(result.changeId, result.profile) ||
    bytes(original.intentDigest, 32) !== bytes(result.intentDigest, 32) ||
    original.disposition < CurrentSecurityDisposition.APPLIED ||
    original.disposition > CurrentSecurityDisposition.REJECTED_INVARIANT ||
    original.items.length > 64
  )
    invalid();
  bytes(original.handoffDigest, 32);
  const commit = original.commit;
  if (
    !commit ||
    commit.version !== 1 ||
    !result.profile ||
    bytes(commit.domain, 32) !== bytes(result.profile.domain, 32) ||
    bytes(commit.cohort, 32) !== bytes(result.profile.cohort, 32)
  )
    invalid();
  bytes(commit.membership, 32);
  bytes(commit.configuration, 32);
  bytes(commit.value, 32);
  count(commit.slot);
  currentCutBinding(original.observedCut, result.profile);
  currentCutBinding(original.resultingCut, result.profile);
  original.items.forEach((item, index) => {
    if (
      item.index !== index ||
      !item.kind ||
      item.disposition < CurrentSecurityDisposition.APPLIED ||
      item.disposition > CurrentSecurityDisposition.REJECTED_INVARIANT
    )
      invalid();
  });
  if (
    original.disposition !== CurrentSecurityDisposition.APPLIED &&
    result.stopObservation !== CurrentAuthorizationStopObservation.NOT_OBSERVED
  )
    invalid();
}

/** A weaker node-local observation cannot erase retained original evidence.
 * New stop observations remain process-specific; no wall-date reconstruction. */
export function reconcileCurrentResult(
  prior: CurrentSecurityChangeResult | undefined,
  next: CurrentSecurityChangeResult,
  expected: CurrentOriginalReference,
): CurrentSecurityChangeResult {
  validateCurrentResult(next, expected);
  if (prior) {
    validateCurrentResult(prior, expected);
    if (prior.original && next.original) {
      const left = toBinary(CurrentSecurityOriginalOutcomeSchema, prior.original);
      const right = toBinary(CurrentSecurityOriginalOutcomeSchema, next.original);
      if (left.length !== right.length || left.some((byte, i) => byte !== right[i])) invalid();
    }
    if (prior.progress > next.progress) return copySecurityContract(prior);
  }
  return copySecurityContract(next);
}
