import { copySecurityContract } from "lantern-sdk/web";
import {
  type SecurityChangeRecovery,
  type PendingSecurityChange,
} from "./security-change-recovery";
export { SecurityChangeRecovery } from "./security-change-recovery";
import type {
  ApplySecurityChangesResponse,
  ExplainAccessResponse,
  GetRoleTemplatesResponse,
  ListIssuersResponse,
  ListRolesResponse,
  ListUsersResponse,
  CurrentSecurityAuditRecord,
  SecurityChange,
  SecurityIdentity,
  SecurityIssuer,
  SecurityRole,
  SecurityUser,
  SecurityVersion,
  CurrentSecurityReview,
  CurrentSecurityChangeResult,
  CurrentSecurityInvocationRejected,
  PrepareSecurityChangesResponse,
  BeginSecurityChangeAuthorizationResponse,
  GetSecurityChangeAuthorizationResponse,
} from "lantern-sdk/web";
import {
  CurrentSecurityProgress,
  CurrentSecurityDisposition,
  CurrentAuthorizationStopObservation,
  currentSecurityVersionBinding,
  currentProfileBinding,
  currentCutBinding,
  currentOriginalBinding,
  validateCurrentReview,
  validatePreparedCurrentReview,
  reconcileCurrentResult,
  SecurityAuthorizationRequirement,
  SecurityAuthorizationState,
} from "lantern-sdk/web";

export type SecurityChangeResult = CurrentSecurityChangeResult;

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
    records: CurrentSecurityAuditRecord[];
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
    review: CurrentSecurityReview,
    signal: AbortSignal,
  ): Promise<PrepareSecurityChangesResponse>;
  beginAuthorization(
    review: CurrentSecurityReview,
    signal: AbortSignal,
  ): Promise<BeginSecurityChangeAuthorizationResponse>;
  authorization(
    review: CurrentSecurityReview,
    id: Uint8Array,
    affinity: string,
    signal: AbortSignal,
  ): Promise<GetSecurityChangeAuthorizationResponse>;
  openAuthorization(): SecurityAuthorizationWindow;
  invocationRejected(
    error: unknown,
  ): CurrentSecurityInvocationRejected | undefined;
  apply(
    request: {
      currentReview: CurrentSecurityReview;
      authorizationProof?: Uint8Array;
    },
    signal: AbortSignal,
  ): Promise<ApplySecurityChangesResponse>;
  status(
    review: CurrentSecurityReview,
    signal: AbortSignal,
  ): Promise<CurrentSecurityChangeResult>;
  failure(error: unknown): SecurityFailure;
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
  audit: CurrentSecurityAuditRecord[];
  auditCursor: string;
  members: SecurityUser[];
  memberCursor: string;
  memberRole: string;
  review?: {
    label: string;
    changes: SecurityChange[];
    contract?: CurrentSecurityReview;
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
    attemptAffinity?: string;
  };
  mutation:
    | "idle"
    | "sending"
    | "unconfirmed"
    | "pending"
    | "applied"
    | "conflict"
    | "rejected";
  result?: SecurityChangeResult;
  explanation?: ExplainAccessResponse;
  recoveryBlocked?: boolean;
}
function validateVersion(
  version: SecurityVersion | undefined,
): asserts version is SecurityVersion {
  currentSecurityVersionBinding(version);
}

export function securityResultMessage(result: SecurityChangeResult): string {
  if (!result.original)
    return result.progress === CurrentSecurityProgress.CHOSEN
      ? "The original operation was chosen; its Apply result is not yet available."
      : result.progress === CurrentSecurityProgress.ORIGIN_DURABLE
        ? "The origin durably retained the operation; its final result is not yet available."
        : "The original operation is unresolved. Keep its identity and check status before another change.";
  if (result.original.disposition !== CurrentSecurityDisposition.APPLIED)
    return (
      "The original operation was rejected: " +
      CurrentSecurityDisposition[result.original.disposition] +
      "."
    );
  return result.stopObservation ===
    CurrentAuthorizationStopObservation.OLD_CUT_NEW_AUTHORIZATIONS_STOPPED
    ? "The original change applied. New authorizations using earlier policy have stopped; previously authorized output may still arrive."
    : "The original change applied. The stop of new authorizations using earlier policy has not yet been observed.";
}
function mutationState(
  result: SecurityChangeResult,
): SecurityManagementState["mutation"] {
  if (result.original)
    return result.original.disposition === CurrentSecurityDisposition.APPLIED
      ? "applied"
      : "rejected";
  return result.progress === CurrentSecurityProgress.UNRESOLVED
    ? "unconfirmed"
    : "pending";
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
  private needsOriginalStatus = false;
  private recoveryUnavailable = false;
  private authorizationWindow?: SecurityAuthorizationWindow;
  constructor(
    private readonly port: SecurityManagementPort,
    private readonly section: SecuritySection,
    private readonly scope: AbortSignal,
    private readonly recovery?: SecurityChangeRecovery,
    private readonly recoveryOwner = "",
  ) {
    try {
      const retained = recovery?.read(recoveryOwner);
      this.pending = retained;
      this.needsOriginalStatus = retained?.needsOriginalStatus ?? false;
    } catch {
      this.recoveryUnavailable = true;
      this.state = {
        ...this.state,
        recoveryBlocked: true,
        message:
          "Original-change recovery storage is unavailable. No new Apply can be sent.",
      };
    }
    if (this.pending) {
      const result = this.pending.result;
      this.state = {
        ...this.state,
        result,
        mutation: this.needsOriginalStatus
          ? "unconfirmed"
          : result
            ? mutationState(result)
            : "unconfirmed",
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
      message: this.recoveryUnavailable
        ? "Original-change recovery storage is unavailable. No new Apply can be sent."
        : "",
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
              : "Security state could not be loaded. Reload to review current policy.",
        });
    }
  }
  async review(label: string, changes: SecurityChange[]) {
    if (
      this.state.phase !== "ready" ||
      !this.state.version ||
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed" ||
      this.needsOriginalStatus ||
      this.recoveryUnavailable ||
      (this.pending && !this.pending.result?.original) ||
      changes.length < 1 ||
      changes.length > 64
    )
      return;
    if (this.pending && !this.pending.result?.original) return;
    this.authorizationWindow?.close();
    const review: NonNullable<SecurityManagementState["review"]> = {
      label,
      changes: copySecurityContract(changes),
      version: copySecurityContract(this.state.version),
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
      validatePreparedCurrentReview(
        prepared.currentReview,
        this.reviewRequest(review),
      );
      if (
        prepared.currentResult ||
        (prepared.requirement !== SecurityAuthorizationRequirement.ORDINARY &&
          prepared.requirement !==
            SecurityAuthorizationRequirement.REAUTHENTICATION)
      )
        throw new Error("Invalid fresh review requirement.");
      this.publish({
        review: {
          ...review,
          contract: copySecurityContract(prepared.currentReview),
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
          message: "The policy changed. Reload and review your change again.",
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
  ): CurrentSecurityReview {
    return review.contract
      ? copySecurityContract(review.contract)
      : {
          $typeName: "graph.v1.CurrentSecurityReview",
          profile: copySecurityContract(review.version.currentProfile),
          expectedCut: copySecurityContract(review.version.currentCut),
          intentDigest: new Uint8Array(),
          changes: copySecurityContract(review.changes),
        };
  }
  private pendingFromReview(
    review: NonNullable<SecurityManagementState["review"]>,
  ): PendingSecurityChange {
    validateCurrentReview(review.contract);
    return { review: copySecurityContract(review.contract) };
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
        !start.startUrl ||
        !start.attemptAffinity ||
        start.attemptAffinity.length > 512 ||
        currentProfileBinding(start.currentProfile) !==
          currentProfileBinding(review.version.currentProfile)
      )
        throw new Error("Invalid operation authorization start.");
      authorizationWindow.navigate(start.startUrl);
      this.publish({
        review: {
          ...starting,
          approval: "authenticating",
          authorizationId: start.authorizationId.slice(),
          attemptAffinity: start.attemptAffinity,
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
      !review.authorizationId ||
      !review.contract ||
      !review.attemptAffinity
    )
      return;
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.authorization(
        copySecurityContract(review.contract),
        review.authorizationId.slice(),
        review.attemptAffinity,
        signal,
      );
      if (!this.current(ticket) || this.state.review !== review) return;
      if (
        response.authorizationId.length !== 32 ||
        !review.authorizationId.every(
          (byte, i) => byte === response.authorizationId[i],
        ) ||
        !response.expiresAt ||
        currentProfileBinding(response.currentProfile) !==
          currentProfileBinding(review.contract.profile)
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
    const id = this.pending?.review.changeId;
    return id
      ? `${id.namespace}:${Array.from(id.nonce, (byte) => byte.toString(16).padStart(2, "0")).join("")}`
      : "";
  }
  cancelReview() {
    this.operation();
    this.authorizationWindow?.close();
    this.publish({ review: undefined });
  }
  private accept(pending: PendingSecurityChange, result: SecurityChangeResult) {
    const accepted = reconcileCurrentResult(
      pending.result,
      result,
      pending.review,
    );
    if (
      accepted.original &&
      (accepted.original.items.length !== pending.review.changes.length ||
        currentCutBinding(accepted.original.observedCut, accepted.profile) !==
          currentCutBinding(pending.review.expectedCut, pending.review.profile))
    )
      throw new Error("Original result differs from the reviewed operation.");
    try {
      this.recovery?.update(
        this.recoveryOwner,
        pending.review,
        accepted,
        !!result.original,
      );
    } catch {
      // Retain freshly received evidence in this controller, while the store
      // keeps its last successfully persisted pre-dispatch original.
      pending.result = accepted;
      this.needsOriginalStatus = true;
      this.publish({
        version: undefined,
        result: accepted,
        review: undefined,
        mutation: "unconfirmed",
        message:
          "The original result was received, but recovery storage could not be updated. Check original status again before another change.",
      });
      return;
    }
    pending.result = accepted;
    // A weaker fresh status cannot unlock a restored cached terminal result.
    if (result.original) this.needsOriginalStatus = false;
    this.publish({
      version: undefined,
      result: accepted,
      review: undefined,
      mutation: this.needsOriginalStatus
        ? "unconfirmed"
        : mutationState(accepted),
      message: this.needsOriginalStatus
        ? "The original is retained. Check its authenticated original status before another change."
        : securityResultMessage(accepted),
    });
  }
  async apply() {
    const review = this.state.review,
      version = this.state.version;
    if (
      !review?.contract ||
      !version ||
      this.state.mutation === "sending" ||
      this.needsOriginalStatus ||
      this.recoveryUnavailable ||
      (this.pending && !this.pending.result?.original) ||
      currentSecurityVersionBinding(review.version) !==
        currentSecurityVersionBinding(version) ||
      (review.approval !== "ordinary" && review.approval !== "approved")
    )
      return;
    const { ticket, signal } = this.operation();
    const pending = this.pendingFromReview(review);
    // Persist in the owning recovery object before any possible dispatch.
    try {
      this.recovery?.stage(
        this.recoveryOwner,
        pending.review,
        this.pending?.review,
      );
    } catch (error) {
      this.publish({
        message:
          error instanceof Error
            ? error.message
            : "Original-change recovery storage is unavailable. No new Apply was sent.",
      });
      return;
    }
    this.pending = pending;
    this.publish({ mutation: "sending", message: "Applying reviewed change…" });
    try {
      const response = await this.port.apply(
        {
          currentReview: copySecurityContract(pending.review),
          authorizationProof: review.authorizationProof?.slice(),
        },
        signal,
      );
      if (this.current(ticket) && this.pending === pending) {
        if (!response.currentResult) throw new Error("Missing current result.");
        this.accept(pending, response.currentResult);
      }
    } catch (error) {
      if (!this.current(ticket)) return;
      const refused = this.port.invocationRejected(error);
      let exact = false;
      try {
        exact =
          !!refused?.purposeRequired &&
          currentOriginalBinding(refused) ===
            currentOriginalBinding(pending.review);
      } catch {
        /* ambiguous evidence */
      }
      // This UI permits one dispatch only. The exact typed refusal therefore
      // settles this invocation; it never clears an earlier ambiguous attempt.
      if (exact) {
        try {
          this.recovery?.clearFirstRefusal(this.recoveryOwner, pending.review);
        } catch {
          this.recovery?.ambiguous(pending.review);
          this.needsOriginalStatus = true;
          this.publish({
            mutation: "unconfirmed",
            message:
              "The invocation was refused, but recovery storage could not be updated. The original is retained for status only.",
          });
          return;
        }
        this.pending = undefined;
        this.publish({
          mutation: "idle",
          result: undefined,
          review: {
            ...review,
            requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
            approval: "required",
            authorizationId: undefined,
            authorizationProof: undefined,
            attemptAffinity: undefined,
          },
          message:
            "This invocation requires reauthentication for the same reviewed change. No new identity was created.",
        });
      } else {
        this.recovery?.ambiguous(pending.review);
        this.publish({
          mutation: "unconfirmed",
          message:
            "The response was not confirmed. The full original identity is retained; check status before another change.",
        });
      }
    }
  }
  async checkStatus() {
    const pending = this.pending;
    if (!pending || this.state.mutation === "sending") return;
    const { ticket, signal } = this.operation();
    try {
      const result = await this.port.status(
        copySecurityContract(pending.review),
        signal,
      );
      if (this.current(ticket) && this.pending === pending)
        this.accept(pending, result);
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
        currentSecurityVersionBinding(inspected) !==
        currentSecurityVersionBinding(response.version)
      )
        throw new Error("Policy or credential binding changed.");
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
            "Membership inspection is unavailable or its policy changed. Reload before reviewing changes.",
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
