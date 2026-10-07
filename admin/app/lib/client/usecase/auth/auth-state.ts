import {
  AuthMode,
  SecurityPrincipalKind,
  type GetAuthCapabilitiesResponse,
  type BrowserSession,
  type GetCurrentPrincipalResponse,
} from "lantern-sdk/web";

export interface AuthGateway {
  capabilities(signal: AbortSignal): Promise<GetAuthCapabilitiesResponse>;
  session(signal: AbortSignal): Promise<BrowserSession | null>;
  logout(csrf: string, signal: AbortSignal): Promise<void>;
  login(issuer: string, stepUp: boolean): void;
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
    version.revision <= 0n ||
    version.digest.length !== 32 ||
    version.generation.length !== 16 ||
    !expiresAt ||
    !/^[A-Za-z0-9_-]{43}$/.test(principal.csrfToken)
  ) {
    throw new Error("Invalid browser session response.");
  }
  return JSON.stringify([
    identity.issuer,
    identity.subject,
    version.revision.toString(),
    Array.from(version.generation),
    Array.from(version.digest),
  ]);
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
  constructor(
    private readonly gateway: AuthGateway,
    private readonly now = Date.now,
  ) {}
  getSnapshot = (): AuthState => this.state;
  getSelectedScope = (): string => this.selectedScope;
  selectScope(prefix: string) {
    const state = this.state;
    if (
      (state.kind !== "ready" && state.kind !== "off") ||
      prefix === this.selectedScope
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
        caps.protocolVersion !== 1 ||
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
      if (
        !Number.isFinite(remaining) ||
        remaining <= 1000 ||
        remaining > 31_000
      )
        throw new Error("Browser authority has expired.");
      const same =
        this.state.kind === "ready" &&
        principalPartition(this.state.principal) === partition &&
        this.state.principal.csrfToken === principal.csrfToken;
      const epoch = this.state.epoch + (same ? 0 : 1);
      if (!same) this.invalidate();
      clearTimeout(this.deadline);
      this.deadline = setTimeout(() => {
        void this.refresh(true);
      }, remaining - 1000);
      this.publish({
        kind: "ready",
        epoch,
        principal,
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
  login(issuer: string, stepUp = false) {
    const state = this.state;
    if (
      (state.kind !== "login" && state.kind !== "ready") ||
      !state.issuers.some((candidate) => candidate.issuer === issuer)
    )
      return;
    this.suspend();
    this.gateway.login(issuer, stepUp);
  }
  async logout(): Promise<void> {
    if (this.state.kind !== "ready") return;
    const csrf = this.state.principal.csrfToken;
    this.invalidateSession();
    const ticket = this.ticket;
    const request = (this.request = new AbortController());
    try {
      await this.gateway.logout(csrf, request.signal);
      if (ticket === this.ticket) await this.refresh();
    } catch {
      if (ticket === this.ticket)
        this.publish({
          kind: "error",
          epoch: this.state.epoch,
          message:
            "Logout could not be confirmed. Retry the authentication check.",
        });
    }
  }
}
