import {
  copySecurityContract,
  currentProfileBinding,
  currentCutBinding,
  currentChangeIdBinding,
  currentOriginalBinding,
  validateCurrentReview,
  reconcileCurrentResult,
  encodeCurrentSecurityReview,
  parseCurrentSecurityReview,
  encodeCurrentSecurityChangeResult,
  parseCurrentSecurityChangeResult,
  SecurityPrincipalKind,
  type CurrentSecurityReview,
  type CurrentSecurityChangeResult,
  type GetCurrentPrincipalResponse,
} from "lantern-sdk/web";
import { normaliseBaseUrl } from "../connection/base-url";

export interface SecurityRecoveryStorage {
  read(): string | null;
  replace(value: string): void;
}
export interface PendingSecurityChange {
  review: CurrentSecurityReview;
  result?: CurrentSecurityChangeResult;
}
interface RecoveryRecord extends PendingSecurityChange {
  needsOriginalStatus: boolean;
}
const maxBytes = 1 << 20;
const maxOwners = 20;
const storageFailure =
  "Original-change recovery storage is unavailable. No new Apply was sent.";
function invalid(): never {
  throw new Error(storageFailure);
}
function object(value: unknown, keys: string[]): Record<string, unknown> {
  if (
    !value ||
    typeof value !== "object" ||
    Array.isArray(value) ||
    Object.keys(value).some((key) => !keys.includes(key))
  )
    invalid();
  return value as Record<string, unknown>;
}

/** Deployment and actor identity; current cut/session/CSRF are intentionally absent. */
export function securityRecoveryOwner(
  baseUrl: string,
  principal: Pick<GetCurrentPrincipalResponse, "identity" | "version">,
): string {
  const gateway = normaliseBaseUrl(baseUrl);
  const actor = principal.identity;
  if (
    !gateway ||
    actor?.kind !== SecurityPrincipalKind.OIDC ||
    !actor.issuer ||
    !actor.subject
  )
    invalid();
  return JSON.stringify([
    gateway,
    actor.issuer,
    actor.subject,
    currentProfileBinding(principal.version?.currentProfile),
  ]);
}
function validateOwner(owner: string, review: CurrentSecurityReview) {
  const fields: unknown = JSON.parse(owner);
  if (
    !Array.isArray(fields) ||
    fields.length !== 4 ||
    fields.some((field) => typeof field !== "string") ||
    JSON.stringify(fields) !== owner ||
    // The connection normalizer removes one trailing slash. Append its
    // delimiter before validating so retained gateway paths stay exact.
    normaliseBaseUrl(fields[0] + "/") !== fields[0] ||
    fields[1] !== review.actor?.issuer ||
    fields[2] !== review.actor?.subject ||
    fields[3] !== currentProfileBinding(review.profile)
  )
    invalid();
}
function validated(record: PendingSecurityChange): PendingSecurityChange {
  validateCurrentReview(record.review);
  // The codecs additionally reject unsupported nested enum/operation values.
  const review = parseCurrentSecurityReview(
    encodeCurrentSecurityReview(record.review),
  );
  const result = record.result
    ? reconcileCurrentResult(
        undefined,
        parseCurrentSecurityChangeResult(
          encodeCurrentSecurityChangeResult(record.result),
        ),
        review,
      )
    : undefined;
  if (
    result?.original &&
    (result.original.items.length !== review.changes.length ||
      currentCutBinding(result.original.observedCut, result.profile) !==
        currentCutBinding(review.expectedCut, review.profile))
  )
    invalid();
  return result ? { review, result } : { review };
}

/** One tab-local writer. Saved bytes are historical metadata, never authority. */
export class SecurityChangeRecovery {
  private records = new Map<string, PendingSecurityChange>();
  private readonly confirmed = new Set<string>();
  private readonly firstInvocations = new Set<string>();
  private unavailable = false;
  constructor(private readonly storage: SecurityRecoveryStorage) {
    try {
      const raw = storage.read();
      if (raw === null) return;
      if (new TextEncoder().encode(raw).length > maxBytes) invalid();
      const envelope = object(JSON.parse(raw), ["version", "records"]);
      if (
        envelope.version !== 1 ||
        !Array.isArray(envelope.records) ||
        envelope.records.length > maxOwners
      )
        invalid();
      const originals = new Set<string>();
      for (const value of envelope.records) {
        const row = object(value, ["owner", "review", "result"]);
        if (
          typeof row.owner !== "string" ||
          typeof row.review !== "string" ||
          (row.result !== undefined && typeof row.result !== "string")
        )
          invalid();
        const record = validated({
          review: parseCurrentSecurityReview(row.review),
          ...(typeof row.result === "string"
            ? { result: parseCurrentSecurityChangeResult(row.result) }
            : {}),
        });
        validateOwner(row.owner, record.review);
        const id =
          currentProfileBinding(record.review.profile) +
          "/" +
          currentChangeIdBinding(record.review.changeId, record.review.profile);
        if (this.records.has(row.owner) || originals.has(id)) invalid();
        originals.add(id);
        this.records.set(row.owner, record);
      }
    } catch {
      // Corrupt/unreadable bytes are preserved, never replaced with an empty store.
      this.records.clear();
      this.unavailable = true;
    }
  }
  private available() {
    if (this.unavailable) invalid();
  }
  private commit(next: Map<string, PendingSecurityChange>) {
    this.available();
    if (next.size > maxOwners)
      throw new Error(
        "Retained control history is full. No new operation was sent.",
      );
    const originals = new Set<string>();
    for (const { review } of next.values()) {
      const id =
        currentProfileBinding(review.profile) +
        "/" +
        currentChangeIdBinding(review.changeId, review.profile);
      if (originals.has(id)) invalid();
      originals.add(id);
    }
    const raw = JSON.stringify({
      version: 1,
      records: Array.from(next, ([owner, record]) => ({
        owner,
        review: encodeCurrentSecurityReview(record.review),
        ...(record.result
          ? { result: encodeCurrentSecurityChangeResult(record.result) }
          : {}),
      })),
    });
    if (new TextEncoder().encode(raw).length > maxBytes)
      throw new Error(
        "Retained control history is full. No new operation was sent.",
      );
    try {
      this.storage.replace(raw);
    } catch {
      throw new Error(storageFailure);
    }
    this.records = next;
  }
  read(owner: string): RecoveryRecord | undefined {
    this.available();
    const record = this.records.get(owner);
    return record
      ? {
          ...copySecurityContract(record),
          needsOriginalStatus: !this.confirmed.has(
            currentOriginalBinding(record.review),
          ),
        }
      : undefined;
  }
  stage(
    owner: string,
    review: CurrentSecurityReview,
    expected?: CurrentSecurityReview,
  ) {
    this.available();
    const record = validated({ review });
    validateOwner(owner, record.review);
    const prior = this.records.get(owner);
    if (
      prior &&
      (!expected ||
        currentOriginalBinding(prior.review) !==
          currentOriginalBinding(expected) ||
        !prior.result?.original ||
        !this.confirmed.has(currentOriginalBinding(prior.review)))
    )
      throw new Error(
        "Check the retained original status before another change.",
      );
    if (
      prior &&
      currentChangeIdBinding(prior.review.changeId, prior.review.profile) ===
        currentChangeIdBinding(record.review.changeId, record.review.profile)
    )
      throw new Error("A new reviewed change needs its own original identity.");
    const next = new Map(this.records);
    next.set(owner, record);
    this.commit(next);
    if (prior) {
      this.confirmed.delete(currentOriginalBinding(prior.review));
      this.firstInvocations.delete(currentOriginalBinding(prior.review));
    }
    this.firstInvocations.add(currentOriginalBinding(record.review));
  }
  update(
    owner: string,
    review: CurrentSecurityReview,
    result: CurrentSecurityChangeResult,
    freshOriginal = !!result.original,
  ) {
    this.available();
    const prior = this.records.get(owner);
    if (
      !prior ||
      currentOriginalBinding(prior.review) !== currentOriginalBinding(review)
    )
      invalid();
    const record = validated({
      review: prior.review,
      result: reconcileCurrentResult(prior.result, result, prior.review),
    });
    const next = new Map(this.records);
    next.set(owner, record);
    this.commit(next);
    const binding = currentOriginalBinding(review);
    this.firstInvocations.delete(binding);
    if (freshOriginal) this.confirmed.add(binding);
  }
  ambiguous(review: CurrentSecurityReview) {
    this.firstInvocations.delete(currentOriginalBinding(review));
  }
  clearFirstRefusal(owner: string, review: CurrentSecurityReview) {
    this.available();
    const prior = this.records.get(owner);
    const binding = currentOriginalBinding(review);
    if (
      !prior ||
      currentOriginalBinding(prior.review) !== binding ||
      !this.firstInvocations.has(binding)
    )
      invalid();
    const next = new Map(this.records);
    next.delete(owner);
    this.commit(next);
    this.firstInvocations.delete(binding);
    this.confirmed.delete(binding);
  }
}
