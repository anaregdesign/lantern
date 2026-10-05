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
} from "lantern-sdk/web";
import { SecurityEnforcementState } from "lantern-sdk/web";

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
  apply(
    request: {
      expectedRevision: bigint;
      changeId: Uint8Array;
      changes: SecurityChange[];
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
  };
  mutation:
    | "idle"
    | "sending"
    | "unconfirmed"
    | "pending"
    | "enforced"
    | "conflict";
  result?: SecurityChangeResult;
  explanation?: ExplainAccessResponse;
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
          this.state.mutation === "conflict" ? "idle" : this.state.mutation,
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
  review(label: string, changes: SecurityChange[]) {
    if (
      this.state.phase !== "ready" ||
      !this.state.version ||
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed" ||
      changes.length < 1 ||
      changes.length > 64
    )
      return;
    this.publish({
      review: {
        label,
        changes: structuredClone(changes),
        expectedRevision: this.state.version.revision,
      },
      message: "",
    });
  }
  changeId(): string {
    return this.pending
      ? Array.from(this.pending.changeId, (byte) =>
          byte.toString(16).padStart(2, "0"),
        ).join("")
      : "";
  }
  cancelReview() {
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
  async apply(recentAuthentication: boolean) {
    const review = this.state.review;
    const version = this.state.version;
    if (
      !recentAuthentication ||
      !review ||
      !version ||
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed" ||
      review.expectedRevision !== version.revision
    )
      return;
    const id = this.port.newChangeId();
    if (id.length !== 16 || id.every((byte) => byte === 0))
      throw new Error("Invalid change ID.");
    const { ticket, signal } = this.operation();
    this.pending = {
      ...review,
      changeId: id.slice(),
      version: structuredClone(version),
    };
    const pending = this.pending;
    this.recovery?.save(this.recoveryOwner, this.pending);
    this.publish({ mutation: "sending", message: "Applying reviewed change…" });
    try {
      const result = await this.port.apply(
        {
          expectedRevision: review.expectedRevision,
          changeId: id.slice(),
          changes: structuredClone(review.changes),
        },
        signal,
      );
      if (this.current(ticket) && this.pending === pending)
        this.acceptApply(pending, result);
    } catch (error) {
      if (!this.current(ticket)) return;
      if (this.port.failure(error) === "conflict") {
        this.pending = undefined;
        this.recovery?.clear();
        this.publish({
          mutation: "conflict",
          review: undefined,
          version: undefined,
          message: "The revision changed. Reload and review your change again.",
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
