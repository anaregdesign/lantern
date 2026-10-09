import { copySecurityContract } from "lantern-sdk/web";
import {
  AuthMode,
  SecurityPrincipalKind,
  currentSecurityVersionBinding,
  currentProfileBinding,
  currentCutBinding,
  validateCurrentSessionReview,
  reconcileCurrentResult,
  type CurrentLogoutRequest,
  type CurrentSessionRevocationReview,
  type SessionRevocation,
  type CurrentSecurityChangeResult,
  type GetAuthCapabilitiesResponse,
  type BrowserSession,
  type GetCurrentPrincipalResponse,
} from "lantern-sdk/web";

export interface AuthGateway {
  capabilities(signal: AbortSignal): Promise<GetAuthCapabilitiesResponse>;
  session(signal: AbortSignal): Promise<BrowserSession | null>;
  logout(
    request: CurrentLogoutRequest,
    csrf: string,
    signal: AbortSignal,
  ): Promise<SessionRevocation>;
  logoutStatus(
    review: CurrentSessionRevocationReview,
    csrf: string,
    signal: AbortSignal,
  ): Promise<CurrentSecurityChangeResult>;
  login(issuer: string): void;
  sameOriginHTTPS: boolean;
}
export type AuthState =
  | { kind: "checking"; epoch: number }
  | { kind: "error"; epoch: number; message: string }
  | { kind: "off"; epoch: number; signal: AbortSignal }
  | {
      kind: "login";
      epoch: number;
      issuers: GetAuthCapabilitiesResponse["loginIssuers"];
    }
  | {
      kind: "ready";
      epoch: number;
      principal: GetCurrentPrincipalResponse;
      canMutate: boolean;
      signal: AbortSignal;
      issuers: GetAuthCapabilitiesResponse["loginIssuers"];
    };

export function principalPartition(
  principal: GetCurrentPrincipalResponse,
): string {
  const { identity, version, expiresAt } = principal;
  if (
    !identity ||
    identity.kind !== SecurityPrincipalKind.OIDC ||
    !identity.issuer ||
    !identity.subject ||
    !version ||
    !expiresAt ||
    !/^[A-Za-z0-9_-]{43}$/.test(principal.csrfToken)
  ) {
    throw new Error("Invalid browser session response.");
  }
  return JSON.stringify([
    identity.issuer,
    identity.subject,
    currentSecurityVersionBinding(version),
  ]);
}

interface RetainedSessionRevocation {
  review: CurrentSessionRevocationReview;
  result?: CurrentSecurityChangeResult;
}

/** App-lifetime recovery metadata; never an authority or an Apply queue. */
export class SessionRevocationRecovery {
  private readonly records: RetainedSessionRevocation[] = [];
  hasRecords = () => this.records.length !== 0;
  retain(review: CurrentSessionRevocationReview): RetainedSessionRevocation {
    if (this.records.length >= 20)
      throw new Error(
        "Retained logout history is full. No new Apply was sent.",
      );
    const record = { review: copySecurityContract(review) };
    this.records.push(record);
    return record;
  }
  forPrincipal(
    principal: GetCurrentPrincipalResponse,
  ): RetainedSessionRevocation[] {
    return this.records.filter(
      ({ review }) =>
        currentProfileBinding(review.profile) ===
          currentProfileBinding(principal.version?.currentProfile) &&
        review.actor?.issuer === principal.identity?.issuer &&
        review.actor?.subject === principal.identity?.subject,
    );
  }
}

/** Owns one gateway/session lifetime. Stale promises cannot restore authority. */
export class AuthController {
  private state: AuthState = { kind: "checking", epoch: 0 };
  private readonly listeners = new Set<() => void>();
  private request?: AbortController;
  private serving = new AbortController();
  private ticket = 0;
  private blockedCSRF = "";
  private selectedScope = "";
  private scopeIdentity = "";
  private deadline?: ReturnType<typeof setTimeout>;
  private logoutMessage = "";
  getLogoutMessage = () => this.logoutMessage;
  hasLogoutRecord = () => this.logoutRecovery.hasRecords();
  constructor(
    private readonly gateway: AuthGateway,
    private readonly now = Date.now,
    private readonly logoutRecovery = new SessionRevocationRecovery(),
  ) {}
  getSnapshot = (): AuthState => this.state;
  getSelectedScope = (): string => this.selectedScope;
  selectScope(prefix: string, replaceExplicitPrefix = false) {
    const state = this.state;
    if (
      (state.kind !== "ready" && state.kind !== "off") ||
      (prefix === this.selectedScope && !replaceExplicitPrefix)
    )
      return;
    this.selectedScope = prefix;
    // Keep the original authority-expiry timer. Selecting a scope cannot
    // extend the permission lease while cancelling the previous generation.
    this.serving.abort();
    this.serving = new AbortController();
    this.publish({
      ...state,
      epoch: state.epoch + 1,
      signal: this.serving.signal,
    });
  }
  subscribe = (listener: () => void): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };
  private publish(state: AuthState) {
    this.state = state;
    this.listeners.forEach((listener) => listener());
  }
  private invalidate() {
    this.serving.abort();
    this.serving = new AbortController();
    clearTimeout(this.deadline);
    this.deadline = undefined;
  }
  suspend() {
    this.ticket++;
    this.request?.abort();
    this.invalidate();
    this.publish({ kind: "checking", epoch: this.state.epoch + 1 });
  }
  dispose() {
    this.ticket++;
    this.request?.abort();
    this.invalidate();
  }
  invalidateSession() {
    if (this.state.kind === "ready")
      this.blockedCSRF = this.state.principal.csrfToken;
    this.suspend();
  }
  async refresh(suspend = false): Promise<void> {
    if (suspend) this.suspend();
    const ticket = ++this.ticket;
    this.request?.abort();
    const request = (this.request = new AbortController());
    try {
      const caps = await this.gateway.capabilities(request.signal);
      if (ticket !== this.ticket) return;
      if (
        (caps.mode === AuthMode.OFF
          ? caps.protocolVersion !== 1
          : caps.protocolVersion !== 2) ||
        (caps.mode !== AuthMode.OFF && caps.mode !== AuthMode.OIDC)
      )
        throw new Error("Unsupported authentication protocol.");
      if (!caps.ready)
        throw new Error("Authentication service is unavailable.");
      if (caps.mode === AuthMode.OFF) {
        this.selectedScope = "";
        this.scopeIdentity = "";
        if (caps.loginIssuers.length || caps.loginPath)
          throw new Error("Invalid OFF capabilities.");
        if (this.state.kind !== "off") this.invalidate();
        this.publish({
          kind: "off",
          epoch: this.state.epoch,
          signal: this.serving.signal,
        });
        return;
      }
      currentProfileBinding(caps.currentProfile);
      if (!this.gateway.sameOriginHTTPS)
        throw new Error("OIDC requires an HTTPS gateway on the Admin origin.");
      if (
        caps.loginPath !== "/auth/login" ||
        !caps.loginIssuers.length ||
        caps.loginIssuers.some((issuer) => !issuer.issuer)
      )
        throw new Error("Invalid login configuration.");
      const session = await this.gateway.session(request.signal);
      if (ticket !== this.ticket) return;
      if (session === null) {
        this.invalidate();
        this.publish({
          kind: "login",
          epoch: this.state.epoch + 1,
          issuers: caps.loginIssuers,
        });
        return;
      }
      if (session.mode !== AuthMode.OIDC || !session.principal)
        throw new Error("Invalid browser session response.");
      const principal = session.principal;
      const partition = principalPartition(principal);
      if (
        currentProfileBinding(session.currentProfile) !==
          currentProfileBinding(caps.currentProfile) ||
        currentProfileBinding(principal.version?.currentProfile) !==
          currentProfileBinding(caps.currentProfile)
      )
        throw new Error("Session authority profile changed.");
      const identity = JSON.stringify([
        principal.identity!.issuer,
        principal.identity!.subject,
      ]);
      if (identity !== this.scopeIdentity) {
        this.selectedScope = "";
        this.scopeIdentity = identity;
      }
      if (principal.csrfToken === this.blockedCSRF) {
        this.invalidate();
        this.publish({
          kind: "login",
          epoch: this.state.epoch + 1,
          issuers: caps.loginIssuers,
        });
        return;
      }
      const expiry =
        Number(principal.expiresAt!.seconds) * 1000 +
        principal.expiresAt!.nanos / 1e6;
      const remaining = expiry - this.now();
      if (!Number.isFinite(remaining) || remaining <= 1000)
        throw new Error("Browser authority has expired.");
      const same =
        this.state.kind === "ready" &&
        principalPartition(this.state.principal) === partition &&
        this.state.principal.csrfToken === principal.csrfToken;
      const epoch = this.state.epoch + (same ? 0 : 1);
      if (!same) this.invalidate();
      clearTimeout(this.deadline);
      this.deadline = setTimeout(
        () => {
          void this.refresh(true);
        },
        Math.min(remaining - 1000, 15_000),
      );
      this.publish({
        kind: "ready",
        epoch,
        principal,
        canMutate: caps.currentOriginEnabled,
        signal: this.serving.signal,
        issuers: caps.loginIssuers,
      });
    } catch (error) {
      if (ticket !== this.ticket) return;
      this.invalidate();
      this.publish({
        kind: "error",
        epoch: this.state.epoch + 1,
        message:
          error instanceof Error
            ? error.message
            : "Authentication check failed.",
      });
    }
  }
  login(issuer: string) {
    const state = this.state;
    if (
      (state.kind !== "login" && state.kind !== "ready") ||
      !state.issuers.some((candidate) => candidate.issuer === issuer)
    )
      return;
    this.suspend();
    this.gateway.login(issuer);
  }
  async logout(): Promise<void> {
    if (this.state.kind !== "ready") return;
    const { principal, canMutate } = this.state;
    const csrf = principal.csrfToken;
    const profile = copySecurityContract(principal.version!.currentProfile!);
    this.invalidateSession();
    const ticket = this.ticket;
    const request = (this.request = new AbortController());
    let localCleared = false;
    let record: RetainedSessionRevocation | undefined;
    this.logoutMessage = "Cluster revocation has not been confirmed.";
    const payload = (
      extra: Partial<CurrentLogoutRequest>,
    ): CurrentLogoutRequest => ({
      $typeName: "graph.v1.CurrentLogoutRequest",
      profile,
      prepareOnly: false,
      localOnly: false,
      ...extra,
    });
    try {
      if (canMutate) {
        const prepared = await this.gateway.logout(
          payload({ prepareOnly: true }),
          csrf,
          request.signal,
        );
        if (ticket !== this.ticket) return;
        validateCurrentSessionReview(prepared.currentReview);
        const review = prepared.currentReview;
        if (
          currentProfileBinding(review.profile) !==
            currentProfileBinding(profile) ||
          currentCutBinding(review.expectedCut, profile) !==
            currentCutBinding(principal.version!.currentCut, profile) ||
          review.actor!.issuer !== principal.identity!.issuer ||
          review.actor!.subject !== principal.identity!.subject ||
          prepared.currentResult ||
          prepared.localCookieCleared
        )
          throw new Error("Mismatched logout review.");
        // Retain the exact origin-minted review before dispatch. It survives
        // local display invalidation, policy refresh and a new sign-in.
        record = this.logoutRecovery.retain(review);
        const response = await this.gateway.logout(
          payload({ review: copySecurityContract(review) }),
          csrf,
          request.signal,
        );
        localCleared = response.localCookieCleared;
        if (response.currentResult)
          record.result = reconcileCurrentResult(
            undefined,
            response.currentResult,
            review,
          );
      }
    } catch {
      /* possible dispatch is retained; never mint/retry another ID */
    }
    if (!localCleared) {
      try {
        localCleared = (
          await this.gateway.logout(
            payload({ localOnly: true }),
            csrf,
            request.signal,
          )
        ).localCookieCleared;
      } catch {
        /* local deletion is separately unconfirmed */
      }
    }
    if (ticket !== this.ticket) return;
    this.logoutMessage =
      (localCleared
        ? "Local sign-out confirmed. "
        : "Local sign-out could not be confirmed. ") +
      (record?.result?.original
        ? "The original cluster revocation result is retained."
        : "Cluster revocation is unconfirmed; no operation was retried.");
    await this.refresh();
  }
  async checkLogoutStatus(): Promise<void> {
    if (this.state.kind !== "ready") return;
    const { principal, signal } = this.state;
    const records = this.logoutRecovery.forPrincipal(principal);
    if (!records.length) return;
    for (const record of records) {
      try {
        const next = await this.gateway.logoutStatus(
          copySecurityContract(record.review),
          principal.csrfToken,
          signal,
        );
        if (signal.aborted) return;
        record.result = reconcileCurrentResult(
          record.result,
          next,
          record.review,
        );
      } catch {
        if (signal.aborted) return;
      }
    }
    this.logoutMessage = records.every((record) => record.result?.original)
      ? "The original cluster revocation results are available; each does not describe the new session."
      : "Original cluster revocation remains unresolved. Its identity is retained; no operation was retried.";
    this.publish({ ...this.state });
  }
}
