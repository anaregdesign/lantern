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
  ManagedLogLevel,
  validateOperationalChange,
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
/** A single bounded log-policy lane in the same app-lifetime recovery store. */
export function logPolicyRecoveryOwner(
  baseUrl: string,
  principal: Pick<GetCurrentPrincipalResponse, "identity" | "version">,
): string {
  const fields: string[] = JSON.parse(
    securityRecoveryOwner(baseUrl, principal),
  );
  return JSON.stringify([...fields, "log-policy"]);
}

/** All eight operational groups share one bounded non-security lane. */
export function operationalPolicyRecoveryOwner(
  baseUrl: string,
  principal: Pick<GetCurrentPrincipalResponse, "identity" | "version">,
): string {
  const fields: string[] = JSON.parse(
    securityRecoveryOwner(baseUrl, principal),
  );
  return JSON.stringify([...fields, "operational-policy"]);
}

function logPolicyOnly(review: CurrentSecurityReview): boolean {
  return (
    review.changes.length === 1 &&
    review.changes[0].operation.case === "putLogPolicy"
  );
}

function validateOwner(owner: string, review: CurrentSecurityReview) {
  const fields: unknown = JSON.parse(owner);
  if (
    !Array.isArray(fields) ||
    (fields.length !== 4 && fields.length !== 5) ||
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
  if (fields.length === 5) {
    const operation = review.changes[0]?.operation;
    if (fields[4] === "operational-policy") {
      if (
        review.changes.length !== 1 ||
        operation?.case !== "putOperationalPolicy"
      )
        invalid();
      validateOperationalChange(operation.value);
    } else if (
      fields[4] !== "log-policy" ||
      !logPolicyOnly(review) ||
      operation?.case !== "putLogPolicy" ||
      operation.value.version !== 1 ||
      ![
        ManagedLogLevel.DEBUG,
        ManagedLogLevel.INFO,
        ManagedLogLevel.WARN,
        ManagedLogLevel.ERROR,
      ].includes(operation.value.level)
    )
      invalid();
  }
  return fields as string[];
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

// Policy lanes without their matching security row account for that future
// row once per actor/deployment, even when both lanes exist. They must not
// occupy the last slot needed by urgent security work. Existing status records
// remain readable/updatable.
function historySlots(
  records: ReadonlyMap<string, PendingSecurityChange>,
): number {
  const reservedSecurityOwners = new Set<string>();
  for (const owner of records.keys()) {
    const fields: string[] = JSON.parse(owner);
    if (fields.length === 5) {
      const securityOwner = JSON.stringify(fields.slice(0, 4));
      if (!records.has(securityOwner))
        reservedSecurityOwners.add(securityOwner);
    }
  }
  return records.size + reservedSecurityOwners.size;
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
        const fields = validateOwner(row.owner, record.review);
        // This one explicit lane upgrade changes only unsigned local indexing.
        // Exact originals remain intact and persisted raw bytes are replaced
        // only by the next successful atomic commit before any new dispatch.
        const recordOwner =
          fields.length === 4 && logPolicyOnly(record.review)
            ? JSON.stringify([...fields, "log-policy"])
            : row.owner;
        validateOwner(recordOwner, record.review);
        const id =
          currentProfileBinding(record.review.profile) +
          "/" +
          currentChangeIdBinding(record.review.changeId, record.review.profile);
        if (this.records.has(recordOwner) || originals.has(id)) invalid();
        originals.add(id);
        this.records.set(recordOwner, record);
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
  private commit(
    next: Map<string, PendingSecurityChange>,
    mode: "new-original" | "retained",
  ) {
    this.available();
    if (
      next.size > maxOwners ||
      (mode === "new-original" && historySlots(next) > maxOwners)
    )
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
    const fields = validateOwner(owner, record.review);
    if (
      fields.length === 4 &&
      record.review.changes.some((change) =>
        ["putLogPolicy", "putOperationalPolicy"].includes(
          change.operation.case ?? "",
        ),
      )
    ) {
      throw new Error("Policy changes require their own recovery lane.");
    }
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
    if (fields.length === 5) {
      const securityOriginal = this.records.get(
        JSON.stringify(fields.slice(0, 4)),
      );
      if (
        securityOriginal &&
        (!securityOriginal.result?.original ||
          !this.confirmed.has(currentOriginalBinding(securityOriginal.review)))
      ) {
        throw new Error(
          "Check the retained security original status before a logging change.",
        );
      }
    }
    const next = new Map(this.records);
    next.set(owner, record);
    this.commit(next, "new-original");
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
    this.commit(next, "retained");
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
    this.commit(next, "retained");
    this.firstInvocations.delete(binding);
    this.confirmed.delete(binding);
  }
}
