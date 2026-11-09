import { copySecurityContract } from "lantern-sdk/web";
import {
  type SecurityChangeRecovery,
  type PendingSecurityChange,
} from "../security/security-change-recovery";
export { SecurityChangeRecovery } from "../security/security-change-recovery";
import type {
  ApplySecurityChangesResponse,
  SecurityChange,
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

export type SecurityFailure =
  | "conflict"
  | "denied"
  | "not-found"
  | "unavailable"
  | "invalid";
export interface CurrentChangePort {
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
  navigate(url: string, attemptAffinity: string): void;
  close(): void;
}
export interface CurrentChangeState {
  phase: "loading" | "ready" | "error";
  message: string;
  version?: SecurityVersion;
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
  recoveryBlocked?: boolean;
}
export function currentChangeResultMessage(
  result: SecurityChangeResult,
): string {
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
): CurrentChangeState["mutation"] {
  if (result.original)
    return result.original.disposition === CurrentSecurityDisposition.APPLIED
      ? "applied"
      : "rejected";
  return result.progress === CurrentSecurityProgress.UNRESOLVED
    ? "unconfirmed"
    : "pending";
}

/** Scope-bound management state. The Server interprets every policy and effect. */
export class CurrentChangeController {
  private state: CurrentChangeState = {
    phase: "loading",
    message: "",
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
    private readonly port: CurrentChangePort,
    private readonly scope: AbortSignal,
    private readonly recovery?: SecurityChangeRecovery,
    private readonly recoveryOwner = "",
    private readonly resultMessage = currentChangeResultMessage,
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
  getSnapshot = (): CurrentChangeState => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  publish(patch: Partial<CurrentChangeState>) {
    if (this.disposed || this.scope.aborted) return;
    this.state = { ...this.state, ...patch };
    this.listeners.forEach((listener) => listener());
  }
  operation() {
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
  current(ticket: number) {
    return ticket === this.ticket && !this.disposed && !this.scope.aborted;
  }
  dispose() {
    this.disposed = true;
    this.ticket++;
    this.request?.abort();
    this.authorizationWindow?.close();
    this.listeners.clear();
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
    const review: NonNullable<CurrentChangeState["review"]> = {
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
  /** Restore unsent metadata only after the caller's fresh authorized reads. */
  restoreReview(review: NonNullable<CurrentChangeState["review"]>): boolean {
    if (
      this.disposed ||
      this.scope.aborted ||
      this.state.phase !== "ready" ||
      !this.state.version ||
      this.state.mutation !== "idle" ||
      this.pending ||
      this.needsOriginalStatus ||
      this.recoveryUnavailable ||
      !review.contract ||
      currentSecurityVersionBinding(review.version) !==
        currentSecurityVersionBinding(this.state.version) ||
      review.authorizationProof
    )
      return false;
    validatePreparedCurrentReview(review.contract, {
      profile: this.state.version.currentProfile,
      expectedCut: this.state.version.currentCut,
      changes: review.changes,
    });
    this.publish({ review: copySecurityContract(review), message: "" });
    return true;
  }
  private reviewRequest(
    review: NonNullable<CurrentChangeState["review"]>,
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
    review: NonNullable<CurrentChangeState["review"]>,
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
      authorizationWindow.navigate(start.startUrl, start.attemptAffinity);
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
        : this.resultMessage(accepted),
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
}
