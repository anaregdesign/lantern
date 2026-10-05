import type {
  ApplySecurityChangesResponse,
  ExplainAccessResponse,
  GetRoleTemplatesResponse,
  GetSecurityChangeStatusResponse,
  ListIssuersResponse,
  ListRolesResponse,
  ListUsersResponse,
  SecurityAuditRecord,
  SecurityChange,
  SecurityIdentity,
  SecurityIssuer,
  SecurityRole,
  SecurityUser,
  SecurityVersion,
  SecurityChangeReview,
  PrepareSecurityChangesResponse,
  BeginSecurityChangeAuthorizationResponse,
  GetSecurityChangeAuthorizationResponse,
  SecurityOperationAuthorizationRequired,
  SecurityChangePrecommitRejected,
} from "lantern-sdk/web";
import {
  SecurityEnforcementState,
  SecurityAuthorizationRequirement,
  SecurityAuthorizationState,
  SecurityChangeRejectionReason,
} from "lantern-sdk/web";

export type SecurityApplyAcknowledgement = Pick<
  ApplySecurityChangesResponse,
  "version" | "applied" | "replayed" | "enforcement"
>;
export type SecurityChangeCommitProof = Pick<
  GetSecurityChangeStatusResponse,
  "version" | "changeId" | "enforcement"
>;
export type SecurityChangeResult = SecurityChangeCommitProof & {
  // Available only from the original Apply acknowledgement, never inferred
  // from retained commit proof or a later policy snapshot.
  applied?: boolean[];
  replayed?: boolean;
};

export type SecuritySection = "issuers" | "users" | "roles";
export type SecurityFailure =
  | "conflict"
  | "denied"
  | "not-found"
  | "unavailable"
  | "invalid";
export interface SecurityManagementPort {
  issuers(cursor: string, signal: AbortSignal): Promise<ListIssuersResponse>;
  users(cursor: string, signal: AbortSignal): Promise<ListUsersResponse>;
  roles(cursor: string, signal: AbortSignal): Promise<ListRolesResponse>;
  templates(
    prefix: string,
    signal: AbortSignal,
  ): Promise<GetRoleTemplatesResponse>;
  audit(
    cursor: string,
    signal: AbortSignal,
  ): Promise<{
    records: SecurityAuditRecord[];
    version?: SecurityVersion;
    nextCursor: string;
  }>;
  validateIssuer(issuer: SecurityIssuer, signal: AbortSignal): Promise<boolean>;
  explain(
    identity: SecurityIdentity,
    action: number,
    key: string | undefined,
    signal: AbortSignal,
    edge?: { tail: string; head: string },
  ): Promise<ExplainAccessResponse>;
  prepare(
    review: SecurityChangeReview,
    signal: AbortSignal,
  ): Promise<PrepareSecurityChangesResponse>;
  beginAuthorization(
    review: SecurityChangeReview,
    signal: AbortSignal,
  ): Promise<BeginSecurityChangeAuthorizationResponse>;
  authorization(
    id: Uint8Array,
    signal: AbortSignal,
  ): Promise<GetSecurityChangeAuthorizationResponse>;
  openAuthorization(): SecurityAuthorizationWindow;
  authorizationRequired(
    error: unknown,
  ): SecurityOperationAuthorizationRequired | undefined;
  precommitRejected(
    error: unknown,
  ): SecurityChangePrecommitRejected | undefined;
  apply(
    request: {
      expectedRevision: bigint;
      changeId: Uint8Array;
      changes: SecurityChange[];
      authorizationProof?: Uint8Array;
    },
    signal: AbortSignal,
  ): Promise<ApplySecurityChangesResponse>;
  status(
    changeId: Uint8Array,
    signal: AbortSignal,
  ): Promise<SecurityChangeCommitProof>;
  failure(error: unknown): SecurityFailure;
  newChangeId(): Uint8Array;
}
export interface SecurityAuthorizationWindow {
  navigate(url: string): void;
  close(): void;
}
export interface SecurityManagementState {
  phase: "loading" | "ready" | "error";
  message: string;
  version?: SecurityVersion;
  issuers: SecurityIssuer[];
  users: SecurityUser[];
  roles: SecurityRole[];
  nextCursor: string;
  templates: SecurityRole[];
  audit: SecurityAuditRecord[];
  auditCursor: string;
  members: SecurityUser[];
  memberCursor: string;
  memberRole: string;
  review?: {
    label: string;
    changes: SecurityChange[];
    expectedRevision: bigint;
    changeId: Uint8Array;
    version: SecurityVersion;
    approval:
      | "preparing"
      | "ordinary"
      | "required"
      | "starting"
      | "authenticating"
      | "approved"
      | "failed";
    requirement?: SecurityAuthorizationRequirement;
    authorizationId?: Uint8Array;
    authorizationProof?: Uint8Array;
    intentDigest?: Uint8Array;
  };
  mutation:
    | "idle"
    | "sending"
    | "unconfirmed"
    | "pending"
    | "enforced"
    | "conflict"
    | "rejected";
  result?: SecurityChangeResult;
  explanation?: ExplainAccessResponse;
}
function precommitRejectionMessage(
  reason: SecurityChangeRejectionReason,
): string | undefined {
  switch (reason) {
    case SecurityChangeRejectionReason.INVALID_CHANGES:
      return "The change contains invalid or duplicate operations.";
    case SecurityChangeRejectionReason.UNKNOWN_ROLE:
      return "A referenced Role does not exist.";
    case SecurityChangeRejectionReason.ISSUER_VALIDATION:
      return "Issuer discovery or signing-key validation failed.";
    case SecurityChangeRejectionReason.ENVIRONMENT_OWNED:
      return "The change would modify environment-owned security configuration.";
    case SecurityChangeRejectionReason.LAST_ADMINISTRATOR:
      return "The change would remove the last usable human administrator.";
    case SecurityChangeRejectionReason.REVISION_CONFLICT:
      return "The security revision changed.";
    default:
      return undefined;
  }
}
function validateVersion(
  version: SecurityVersion | undefined,
): asserts version is SecurityVersion {
  if (
    !version ||
    version.revision < 1n ||
    version.revision > 0xffffffffffffffffn ||
    version.digest.length !== 32 ||
    version.generation.length !== 16 ||
    version.generation.every((byte) => byte === 0)
  ) {
    throw new Error("Invalid security revision.");
  }
}
function sameGeneration(a: SecurityVersion, b: SecurityVersion): boolean {
  return a.generation.every((byte, index) => byte === b.generation[index]);
}

interface PendingSecurityChange {
  changeId: Uint8Array;
  changes: SecurityChange[];
  expectedRevision: bigint;
  version: SecurityVersion;
  result?: SecurityChangeResult;
}

// Owned by the AuthProvider lifetime, never localStorage. A policy refresh may
// discard granting views while retaining the immutable ID of a possibly sent
// control change for status-only recovery under the same browser session.
export class SecurityChangeRecovery {
  private owner = "";
  private record?: PendingSecurityChange;
  read(owner: string): PendingSecurityChange | undefined {
    if (owner !== this.owner) {
      this.clear();
      return undefined;
    }
    return this.record ? structuredClone(this.record) : undefined;
  }
  save(owner: string, record: PendingSecurityChange) {
    this.owner = owner;
    this.record = structuredClone(record);
  }
  clear() {
    this.owner = "";
    this.record = undefined;
  }
}

/** Scope-bound management state. The Server interprets every policy and effect. */
export class SecurityManagementController {
  private state: SecurityManagementState = {
    phase: "loading",
    message: "",
    issuers: [],
    users: [],
    roles: [],
    nextCursor: "",
    templates: [],
    audit: [],
    auditCursor: "",
    members: [],
    memberCursor: "",
    memberRole: "",
    mutation: "idle",
  };
  private readonly listeners = new Set<() => void>();
  private request?: AbortController;
  private ticket = 0;
  private disposed = false;
  private pending?: PendingSecurityChange;
  private authorizationWindow?: SecurityAuthorizationWindow;
  constructor(
    private readonly port: SecurityManagementPort,
    private readonly section: SecuritySection,
    private readonly scope: AbortSignal,
    private readonly recovery?: SecurityChangeRecovery,
    private readonly recoveryOwner = "",
  ) {
    this.pending = recovery?.read(recoveryOwner);
    if (this.pending) {
      const result = this.pending.result;
      this.state = {
        ...this.state,
        result,
        mutation: !result
          ? "unconfirmed"
          : result.enforcement === SecurityEnforcementState.ENFORCED
            ? "enforced"
            : "pending",
        message:
          "A prior control change is retained. Check its original status before another change.",
      };
    }
  }
  getSnapshot = (): SecurityManagementState => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private publish(patch: Partial<SecurityManagementState>) {
    if (this.disposed || this.scope.aborted) return;
    this.state = { ...this.state, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  private operation() {
    if (this.disposed || this.scope.aborted)
      throw new Error("Authentication scope changed.");
    const review = this.state.review;
    if (review?.approval === "preparing") {
      // A scope read can interrupt preflight. Retire its preparation without
      // authorizing Apply, and ignore its eventual response through the ticket.
      this.publish({
        review: { ...review, approval: "failed" },
        message:
          "The review check was interrupted. No Apply was sent; review the change again before applying.",
      });
    }
    if (review?.approval === "starting") {
      // Every scope operation may supersede Begin, including audit/member
      // reads. Close only the owned window and leave this exact review usable.
      this.authorizationWindow?.close();
      this.authorizationWindow = undefined;
      this.publish({
        review: {
          ...review,
          approval: "required",
          authorizationId: undefined,
          authorizationProof: undefined,
        },
        message:
          "The approval request was interrupted. Reauthenticate this reviewed change when ready; no Apply was sent.",
      });
    }
    this.request?.abort();
    this.request = new AbortController();
    return {
      ticket: ++this.ticket,
      signal: AbortSignal.any([this.scope, this.request.signal]),
    };
  }
  private current(ticket: number) {
    return ticket === this.ticket && !this.disposed && !this.scope.aborted;
  }
  dispose() {
    this.disposed = true;
    this.ticket++;
    this.request?.abort();
    this.authorizationWindow?.close();
    this.listeners.clear();
  }
  async load(cursor = "") {
    if (
      this.pending &&
      (this.state.mutation === "sending" ||
        this.state.mutation === "unconfirmed")
    )
      return;
    const { ticket, signal } = this.operation();
    this.authorizationWindow?.close();
    this.authorizationWindow = undefined;
    this.publish({
      phase: "loading",
      message: "",
      review: undefined,
      explanation: undefined,
    });
    try {
      const page = await (this.section === "issuers"
        ? this.port.issuers(cursor, signal)
        : this.section === "users"
          ? this.port.users(cursor, signal)
          : this.port.roles(cursor, signal));
      validateVersion(page.version);
      if (!this.current(ticket)) return;
      this.publish({
        phase: "ready",
        version: page.version,
        nextCursor: page.nextCursor,
        issuers: "issuers" in page ? page.issuers : [],
        users: "users" in page ? page.users : [],
        roles: "roles" in page ? page.roles : [],
        mutation:
          this.state.mutation === "conflict" ||
          this.state.mutation === "rejected"
            ? "idle"
            : this.state.mutation,
      });
    } catch (error) {
      if (this.current(ticket))
        this.publish({
          phase: "error",
          version: undefined,
          message:
            this.port.failure(error) === "denied"
              ? "Security management permission is required."
              : "Security state could not be loaded. Reload to review the current revision.",
        });
    }
  }
  async review(label: string, changes: SecurityChange[]) {
    if (
      this.state.phase !== "ready" ||
      !this.state.version ||
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed" ||
      changes.length < 1 ||
      changes.length > 64
    )
      return;
    const id = this.port.newChangeId();
    if (id.length !== 16 || id.every((byte) => byte === 0))
      throw new Error("Invalid change ID.");
    this.authorizationWindow?.close();
    const review: NonNullable<SecurityManagementState["review"]> = {
      label,
      changes: structuredClone(changes),
      expectedRevision: this.state.version.revision,
      changeId: id.slice(),
      version: structuredClone(this.state.version),
      approval: "preparing",
    };
    const { ticket, signal } = this.operation();
    this.publish({
      review,
      message: "Checking the reviewed change with Server…",
    });
    try {
      const prepared = await this.port.prepare(
        this.reviewRequest(review),
        signal,
      );
      if (!this.current(ticket) || this.state.review !== review) return;
      validateVersion(prepared.expectedVersion);
      if (
        !sameGeneration(prepared.expectedVersion, review.version) ||
        prepared.expectedVersion.revision !== review.expectedRevision ||
        !prepared.expectedVersion.digest.every(
          (byte, i) => byte === review.version.digest[i],
        ) ||
        prepared.changeId.length !== 16 ||
        !review.changeId.every((byte, i) => byte === prepared.changeId[i]) ||
        prepared.intentDigest.length !== 32 ||
        prepared.intentDigest.every((byte) => byte === 0) ||
        (prepared.requirement !== SecurityAuthorizationRequirement.ORDINARY &&
          prepared.requirement !==
            SecurityAuthorizationRequirement.REAUTHENTICATION)
      )
        throw new Error("Invalid reviewed change requirement.");
      if (prepared.retainedCommit) {
        const pending = this.pendingFromReview(review);
        this.pending = pending;
        this.recovery?.save(this.recoveryOwner, pending);
        this.acceptStatus(pending, prepared.retainedCommit);
        return;
      }
      this.publish({
        review: {
          ...review,
          intentDigest: prepared.intentDigest.slice(),
          requirement: prepared.requirement,
          approval:
            prepared.requirement === SecurityAuthorizationRequirement.ORDINARY
              ? "ordinary"
              : "required",
        },
        message: "",
      });
    } catch (error) {
      if (!this.current(ticket) || this.state.review !== review) return;
      if (this.port.failure(error) === "conflict")
        this.publish({
          review: undefined,
          version: undefined,
          mutation: "conflict",
          message: "The revision changed. Reload and review your change again.",
        });
      else
        this.publish({
          review: { ...review, approval: "failed" },
          message:
            "The reviewed change could not be checked. No Apply was sent; review again before applying.",
        });
    }
  }
  private reviewRequest(
    review: NonNullable<SecurityManagementState["review"]>,
  ): SecurityChangeReview {
    return {
      $typeName: "graph.v1.SecurityChangeReview",
      expectedVersion: structuredClone(review.version),
      changeId: review.changeId.slice(),
      changes: structuredClone(review.changes),
    };
  }
  private pendingFromReview(
    review: NonNullable<SecurityManagementState["review"]>,
  ): PendingSecurityChange {
    return {
      changes: structuredClone(review.changes),
      expectedRevision: review.expectedRevision,
      changeId: review.changeId.slice(),
      version: structuredClone(review.version),
    };
  }
  async authorize() {
    const review = this.state.review;
    if (
      !review ||
      review.requirement !==
        SecurityAuthorizationRequirement.REAUTHENTICATION ||
      (review.approval !== "required" && review.approval !== "failed")
    )
      return;
    const { ticket, signal } = this.operation();
    const starting = {
      ...review,
      approval: "starting" as const,
      authorizationId: undefined,
      authorizationProof: undefined,
    };
    this.publish({ review: starting, message: "" });
    try {
      this.authorizationWindow?.close();
      const authorizationWindow = this.port.openAuthorization();
      this.authorizationWindow = authorizationWindow;
      const start = await this.port.beginAuthorization(
        this.reviewRequest(starting),
        signal,
      );
      if (!this.current(ticket) || this.state.review !== starting) {
        authorizationWindow.close();
        return;
      }
      if (
        start.authorizationId.length !== 32 ||
        start.authorizationId.every((byte) => byte === 0) ||
        !start.expiresAt ||
        !start.startUrl
      )
        throw new Error("Invalid operation authorization start.");
      authorizationWindow.navigate(start.startUrl);
      this.publish({
        review: {
          ...starting,
          approval: "authenticating",
          authorizationId: start.authorizationId.slice(),
        },
        message:
          "Complete reauthentication in the opened window, then check the approval here. Your ordinary session remains active.",
      });
    } catch {
      if (this.current(ticket) && this.state.review === starting) {
        this.authorizationWindow?.close();
        this.publish({
          review: { ...starting, approval: "failed" },
          message:
            "Operation reauthentication could not be started. Your reviewed change was not applied.",
        });
      }
    }
  }
  async checkAuthorization() {
    const review = this.state.review;
    if (
      !review ||
      review.approval !== "authenticating" ||
      !review.authorizationId
    )
      return;
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.authorization(
        review.authorizationId.slice(),
        signal,
      );
      if (!this.current(ticket) || this.state.review !== review) return;
      if (
        response.authorizationId.length !== 32 ||
        !review.authorizationId.every(
          (byte, i) => byte === response.authorizationId[i],
        ) ||
        !response.expiresAt
      )
        throw new Error("Invalid operation authorization response.");
      if (
        response.state === SecurityAuthorizationState.PENDING &&
        response.authorizationProof.length === 0
      ) {
        this.publish({
          message:
            "Reauthentication is still pending. Complete it in the opened window, then check again.",
        });
        return;
      }
      if (
        response.state !== SecurityAuthorizationState.APPROVED ||
        response.authorizationProof.length !== 32 ||
        response.authorizationProof.every((byte) => byte === 0)
      )
        throw new Error("Operation was not approved.");
      this.authorizationWindow?.close();
      this.publish({
        review: {
          ...review,
          approval: "approved",
          authorizationProof: response.authorizationProof.slice(),
        },
        message:
          "Reauthentication approved this exact reviewed change. Apply when ready.",
      });
    } catch {
      if (this.current(ticket) && this.state.review === review) {
        this.authorizationWindow?.close();
        this.publish({
          review: {
            ...review,
            approval: "failed",
            authorizationId: undefined,
            authorizationProof: undefined,
          },
          message:
            "Operation approval is unavailable or was refused. Your ordinary session remains active; no Apply was sent.",
        });
      }
    }
  }
  changeId(): string {
    return this.pending
      ? Array.from(this.pending.changeId, (byte) =>
          byte.toString(16).padStart(2, "0"),
        ).join("")
      : "";
  }
  cancelReview() {
    this.operation();
    this.authorizationWindow?.close();
    this.publish({ review: undefined });
  }
  private accept(pending: PendingSecurityChange, result: SecurityChangeResult) {
    pending.result = structuredClone(result);
    this.recovery?.save(this.recoveryOwner, pending);
    this.publish({
      version: undefined,
      result,
      review: undefined,
      mutation:
        result.enforcement === SecurityEnforcementState.ENFORCED
          ? "enforced"
          : "pending",
      message:
        result.enforcement === SecurityEnforcementState.ENFORCED
          ? "Change enforced."
          : "Change committed. Cluster enforcement is pending; allow up to 35 seconds while previous authority expires.",
    });
  }
  private validateCommit(
    pending: PendingSecurityChange,
    version: SecurityVersion | undefined,
    enforcement: SecurityEnforcementState,
  ): asserts version is SecurityVersion {
    validateVersion(version);
    if (
      !sameGeneration(pending.version, version) ||
      // The present fixed writer commits exactly one CAS revision per batch.
      version.revision !== pending.expectedRevision + 1n ||
      (enforcement !== SecurityEnforcementState.COMMITTED_PENDING &&
        enforcement !== SecurityEnforcementState.ENFORCED)
    ) {
      throw new Error("Invalid retained commit version.");
    }
  }
  private acceptApply(
    pending: PendingSecurityChange,
    result: SecurityApplyAcknowledgement,
  ) {
    this.validateCommit(pending, result.version, result.enforcement);
    if (
      result.applied.length !== pending.changes.length ||
      result.applied.some((applied) => typeof applied !== "boolean") ||
      typeof result.replayed !== "boolean"
    ) {
      throw new Error("Invalid change acknowledgement.");
    }
    this.accept(pending, { ...result, changeId: pending.changeId.slice() });
  }
  private acceptStatus(
    pending: PendingSecurityChange,
    proof: SecurityChangeCommitProof,
  ) {
    this.validateCommit(pending, proof.version, proof.enforcement);
    const retained = pending.result;
    if (
      proof.changeId.length !== 16 ||
      !pending.changeId.every((byte, i) => byte === proof.changeId[i]) ||
      (retained &&
        (retained.version!.revision !== proof.version.revision ||
          !retained.version!.digest.every(
            (byte, i) => byte === proof.version!.digest[i],
          )))
    ) {
      throw new Error(
        "Retained commit proof does not match the original change.",
      );
    }
    this.accept(pending, {
      ...proof,
      applied: retained?.applied,
      replayed: retained?.replayed,
    });
  }
  async apply() {
    const review = this.state.review;
    const version = this.state.version;
    if (
      !review ||
      !version ||
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed" ||
      review.expectedRevision !== version.revision ||
      !sameGeneration(review.version, version) ||
      !review.version.digest.every((byte, i) => byte === version.digest[i]) ||
      (review.approval !== "ordinary" && review.approval !== "approved")
    )
      return;
    const id = review.changeId;
    const { ticket, signal } = this.operation();
    this.pending = this.pendingFromReview(review);
    const pending = this.pending;
    this.recovery?.save(this.recoveryOwner, this.pending);
    this.publish({ mutation: "sending", message: "Applying reviewed change…" });
    try {
      const result = await this.port.apply(
        {
          expectedRevision: review.expectedRevision,
          changeId: id.slice(),
          changes: structuredClone(review.changes),
          authorizationProof: review.authorizationProof?.slice(),
        },
        signal,
      );
      if (this.current(ticket) && this.pending === pending)
        this.acceptApply(pending, result);
    } catch (error) {
      if (!this.current(ticket)) return;
      const refused = this.port.authorizationRequired(error);
      const rejected = this.port.precommitRejected(error);
      const refusedVersion = refused?.expectedVersion;
      // Only this first, bound invocation is settled by the typed refusal.
      // An older ambiguous command cannot reach Apply and remains status-only.
      if (
        refused &&
        !rejected &&
        refusedVersion &&
        review.intentDigest &&
        refused.changeId.length === 16 &&
        pending.changeId.every((byte, i) => byte === refused.changeId[i]) &&
        refusedVersion.revision === pending.expectedRevision &&
        refusedVersion.digest.length === 32 &&
        pending.version.digest.every(
          (byte, i) => byte === refusedVersion.digest[i],
        ) &&
        refusedVersion.generation.length === 16 &&
        sameGeneration(pending.version, refusedVersion) &&
        refused.intentDigest.length === 32 &&
        review.intentDigest.every((byte, i) => byte === refused.intentDigest[i])
      ) {
        this.pending = undefined;
        this.recovery?.clear();
        this.publish({
          mutation: "idle",
          result: undefined,
          review: {
            ...review,
            requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
            approval: "required",
            authorizationId: undefined,
            authorizationProof: undefined,
          },
          message:
            "This Apply was refused before commit. Reauthenticate the same reviewed change, then apply when ready.",
        });
      } else if (
        !refused &&
        rejected &&
        rejected.changeId.length === 16 &&
        pending.changeId.every((byte, i) => byte === rejected.changeId[i]) &&
        rejected.expectedRevision === pending.expectedRevision &&
        precommitRejectionMessage(rejected.reason)
      ) {
        this.pending = undefined;
        this.recovery?.clear();
        this.publish({
          mutation:
            rejected.reason === SecurityChangeRejectionReason.REVISION_CONFLICT
              ? "conflict"
              : "rejected",
          result: undefined,
          review: undefined,
          version: undefined,
          message:
            precommitRejectionMessage(rejected.reason)! +
            " This Apply was refused before commit. Reload, correct the draft, and review a new change before applying.",
        });
      } else {
        this.publish({
          mutation: "unconfirmed",
          message:
            "The response was not confirmed. Check the original change status before making another change.",
        });
      }
    }
  }
  async checkStatus() {
    const pending = this.pending;
    if (!pending || this.state.mutation === "sending") return;
    const { ticket, signal } = this.operation();
    try {
      const result = await this.port.status(pending.changeId.slice(), signal);
      if (this.current(ticket) && this.pending === pending)
        this.acceptStatus(pending, result);
    } catch {
      if (this.current(ticket))
        this.publish({
          message:
            "Change status is unavailable. The original change ID is retained; no mutation was resent.",
        });
    }
  }
  async loadTemplates(prefix: string) {
    if (this.state.mutation === "sending") return;
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.templates(prefix, signal);
      validateVersion(response.version);
      if (this.current(ticket)) this.publish({ templates: response.roles });
    } catch {
      if (this.current(ticket))
        this.publish({ message: "Role templates are unavailable." });
    }
  }
  async loadRoleMembers(roleId: string, cursor = "") {
    if (this.state.mutation === "sending" || !this.state.version || !roleId)
      return;
    const inspected = this.state.version;
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.users(cursor, signal);
      validateVersion(response.version);
      if (
        !sameGeneration(inspected, response.version) ||
        inspected.revision !== response.version.revision ||
        !inspected.digest.every(
          (byte, i) => byte === response.version!.digest[i],
        )
      )
        throw new Error("Revision changed.");
      if (this.current(ticket))
        this.publish({
          memberRole: roleId,
          memberCursor: response.nextCursor,
          members: response.users.filter((user) =>
            user.assignments.some((assignment) => assignment.roleId === roleId),
          ),
        });
    } catch {
      if (this.current(ticket))
        this.publish({
          members: [],
          memberCursor: "",
          memberRole: "",
          message:
            "Membership inspection is unavailable or its revision changed. Reload before reviewing changes.",
        });
    }
  }
  async loadAudit(cursor = "") {
    if (this.state.mutation === "sending") return;
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.audit(cursor, signal);
      validateVersion(response.version);
      if (this.current(ticket))
        this.publish({
          audit: response.records,
          auditCursor: response.nextCursor,
        });
    } catch {
      if (this.current(ticket))
        this.publish({ message: "Audit records are unavailable." });
    }
  }
  async validateIssuer(issuer: SecurityIssuer): Promise<boolean> {
    if (this.state.mutation === "sending") return false;
    const { ticket, signal } = this.operation();
    try {
      const valid = await this.port.validateIssuer(issuer, signal);
      if (!this.current(ticket)) return false;
      this.publish({
        message: valid
          ? "Issuer metadata validated by the Server."
          : "Issuer validation failed.",
      });
      return valid;
    } catch {
      if (this.current(ticket))
        this.publish({ message: "Issuer validation failed." });
      return false;
    }
  }
  async explain(
    identity: SecurityIdentity,
    action: number,
    key?: string,
    edge?: { tail: string; head: string },
  ) {
    if (this.state.mutation === "sending") return;
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.explain(
        identity,
        action,
        key,
        signal,
        edge,
      );
      validateVersion(response.version);
      if (this.current(ticket)) this.publish({ explanation: response });
    } catch {
      if (this.current(ticket))
        this.publish({ message: "Access explanation is unavailable." });
    }
  }
}
