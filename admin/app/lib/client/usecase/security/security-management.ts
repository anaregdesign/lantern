import {
  copySecurityContract,
  currentSecurityVersionBinding,
} from "lantern-sdk/web";
import type {
  ExplainAccessResponse,
  GetRoleTemplatesResponse,
  GetRoleResponse,
  GetUserResponse,
  GetEdgePolicyResponse,
  GetRegistrationPolicyResponse,
  SecurityEdgePolicy,
  SecurityRegistrationPolicy,
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
} from "lantern-sdk/web";
import {
  CurrentChangeController,
  type CurrentChangePort,
  type CurrentChangeState,
  type SecurityChangeRecovery,
} from "../current-change/current-change";
export {
  SecurityChangeRecovery,
  currentChangeResultMessage as securityResultMessage,
} from "../current-change/current-change";
export type {
  SecurityChangeResult,
  SecurityFailure,
  SecurityAuthorizationWindow,
} from "../current-change/current-change";
import {
  SecurityDraftRecovery,
  recoverableSecurityReview,
  type SecurityDraftIntent,
  type SecurityEditorIntent,
  type SecurityPolicyIntent,
} from "./security-draft-recovery";
export type SecuritySection = "issuers" | "users" | "roles";
export interface SecurityManagementPort extends CurrentChangePort {
  issuers(cursor: string, signal: AbortSignal): Promise<ListIssuersResponse>;
  users(cursor: string, signal: AbortSignal): Promise<ListUsersResponse>;
  roles(cursor: string, signal: AbortSignal): Promise<ListRolesResponse>;
  role(id: string, signal: AbortSignal): Promise<GetRoleResponse>;
  user(
    identity: SecurityIdentity,
    signal: AbortSignal,
  ): Promise<GetUserResponse>;
  edgePolicy(signal: AbortSignal): Promise<GetEdgePolicyResponse>;
  registrationPolicy(
    signal: AbortSignal,
  ): Promise<GetRegistrationPolicyResponse>;
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
    permission?: number,
    resourceKind?: number,
  ): Promise<ExplainAccessResponse>;
}
export interface SecurityManagementState extends CurrentChangeState {
  restoredIntent?: SecurityDraftIntent;
  restoredSelection?: {
    issuer?: SecurityIssuer;
    role?: SecurityRole;
    user?: SecurityUser;
  };
  issuers: SecurityIssuer[];
  users: SecurityUser[];
  roles: SecurityRole[];
  nextCursor: string;
  templates: SecurityRole[];
  templatePhase: "idle" | "loading" | "ready" | "error";
  templateMessage: string;
  roleCursor: string;
  policyPhase: "idle" | "loading" | "ready" | "error";
  policyMessage: string;
  edgePolicy?: SecurityEdgePolicy;
  registrationPolicy?: SecurityRegistrationPolicy;
  joiningRole?: SecurityRole;
  policyIssuers: SecurityIssuer[];
  policyIssuerCursor: string;
  audit: CurrentSecurityAuditRecord[];
  auditCursor: string;
  members: SecurityUser[];
  memberCursor: string;
  memberRole: string;
  explanation?: ExplainAccessResponse;
}
function validateVersion(
  version: SecurityVersion | undefined,
): asserts version is SecurityVersion {
  currentSecurityVersionBinding(version);
}
const workflowFields = [
  "phase",
  "message",
  "version",
  "review",
  "mutation",
  "result",
  "recoveryBlocked",
] as const;
/** Security list/editor reads composed with the shared reviewed-change owner. */
export class SecurityManagementController {
  private readonly workflow: CurrentChangeController;
  private readonly listeners = new Set<() => void>();
  private readonly unsubscribeWorkflow: () => void;
  private disposed = false;
  private state: SecurityManagementState;
  private intent?: SecurityDraftIntent;
  private retentionReady = false;
  constructor(
    private readonly port: SecurityManagementPort,
    private readonly section: SecuritySection,
    private readonly scope: AbortSignal,
    recovery?: SecurityChangeRecovery,
    recoveryOwner = "",
    private readonly drafts?: SecurityDraftRecovery,
    private readonly draftOwner?: object,
  ) {
    this.intent = draftOwner ? drafts?.read(draftOwner, section) : undefined;
    this.workflow = new CurrentChangeController(
      port,
      scope,
      recovery,
      recoveryOwner,
    );
    this.state = {
      ...this.workflow.getSnapshot(),
      issuers: [],
      users: [],
      roles: [],
      nextCursor: "",
      templates: [],
      templatePhase: "idle",
      templateMessage: "",
      roleCursor: "",
      policyPhase: "idle",
      policyMessage: "",
      policyIssuers: [],
      policyIssuerCursor: "",
      audit: [],
      auditCursor: "",
      members: [],
      memberCursor: "",
      memberRole: "",
    };
    this.unsubscribeWorkflow = this.workflow.subscribe(() => {
      this.state = { ...this.state, ...this.workflow.getSnapshot() };
      this.retain();
      this.listeners.forEach((listener) => listener());
    });
  }
  private retain() {
    if (
      !this.retentionReady ||
      !this.drafts ||
      !this.draftOwner ||
      this.disposed ||
      this.scope.aborted
    )
      return;
    if (
      !this.state.version ||
      this.state.mutation !== "idle" ||
      this.state.recoveryBlocked
    ) {
      this.drafts.clear(this.draftOwner, this.section);
      this.intent = undefined;
      return;
    }
    this.intent = {
      ...this.intent,
      binding: currentSecurityVersionBinding(this.state.version),
      policyLoaded:
        this.state.policyPhase === "ready" || !!this.intent?.policyLoaded,
      review: recoverableSecurityReview(this.state.review),
    };
    this.drafts.write(this.draftOwner, this.section, this.intent);
  }
  retainEditorIntent(editor: SecurityEditorIntent | undefined) {
    if (!this.retentionReady) return;
    this.intent = { binding: "", policyLoaded: false, ...this.intent, editor };
    this.retain();
  }
  retainPolicyIntent(policies: SecurityPolicyIntent) {
    if (!this.retentionReady) return;
    this.intent = {
      binding: "",
      policyLoaded: false,
      ...this.intent,
      policies,
    };
    this.retain();
  }
  /** Reads are serial: each owns the same cancellation/request ticket. */
  async initialize() {
    const retained = this.intent && copySecurityContract(this.intent);
    await this.load();
    if (this.disposed || this.scope.aborted) return;
    let restored: SecurityDraftIntent | undefined;
    let selection: SecurityManagementState["restoredSelection"];
    try {
      if (
        retained &&
        this.state.phase === "ready" &&
        this.state.version &&
        this.state.mutation === "idle" &&
        !this.state.recoveryBlocked
      ) {
        const matches = () =>
          this.state.version &&
          currentSecurityVersionBinding(this.state.version) ===
            retained.binding;
        if (!matches()) throw new Error("Policy binding changed");
        if (retained.policyLoaded) {
          await this.loadPolicies();
          if (this.state.policyPhase !== "ready" || !matches())
            throw new Error("Shared policy validation failed");
        }
        const editor = retained.editor;
        if (editor?.mode === "edit") {
          selection = {};
          if (editor.selectedKind === "issuers") {
            selection.issuer = this.state.issuers.find(
              (value) => value.issuer === editor.issuerKey,
            );
            if (!selection.issuer)
              throw new Error("Selected Issuer unavailable");
          } else {
            const { ticket, signal } = this.operation();
            if (editor.selectedKind === "roles" && editor.roleKey) {
              const response = await this.port.role(editor.roleKey, signal);
              validateVersion(response.version);
              if (
                !this.current(ticket) ||
                currentSecurityVersionBinding(response.version) !==
                  retained.binding ||
                !response.role
              )
                throw new Error("Selected Role changed");
              selection.role = response.role;
            } else if (editor.selectedKind === "users" && editor.userKey) {
              const response = await this.port.user(editor.userKey, signal);
              validateVersion(response.version);
              if (
                !this.current(ticket) ||
                currentSecurityVersionBinding(response.version) !==
                  retained.binding ||
                !response.user
              )
                throw new Error("Selected User changed");
              selection.user = response.user;
            } else throw new Error("Selected record missing");
          }
        }
        if (!matches() || this.disposed || this.scope.aborted) return;
        if (retained.review && !this.workflow.restoreReview(retained.review))
          throw new Error("Original status has priority");
        restored = retained;
      }
    } catch {
      if (this.disposed || this.scope.aborted) return;
      this.intent = undefined;
      this.drafts?.clear(this.draftOwner, this.section);
      if (!this.disposed && !this.scope.aborted)
        this.publish({
          message:
            "The saved editing intent could not be revalidated. Reload and review current policy before editing again.",
        });
    }
    if (this.disposed || this.scope.aborted) return;
    if (!restored) {
      this.intent = undefined;
      this.drafts?.clear(this.draftOwner, this.section);
    }
    this.retentionReady = true;
    if (restored)
      this.publish({ restoredIntent: restored, restoredSelection: selection });
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
    this.retain();
    const workflowPatch: Partial<CurrentChangeState> = {};
    for (const key of workflowFields) {
      if (key in patch) Object.assign(workflowPatch, { [key]: patch[key] });
    }
    if (Object.keys(workflowPatch).length) this.workflow.publish(workflowPatch);
    else this.listeners.forEach((listener) => listener());
  }
  private operation() {
    return this.workflow.operation();
  }
  private current(ticket: number) {
    return this.workflow.current(ticket);
  }
  dispose() {
    this.disposed = true;
    this.unsubscribeWorkflow();
    this.workflow.dispose();
    this.listeners.clear();
  }
  review(label: string, changes: SecurityChange[]) {
    return this.workflow.review(label, changes);
  }
  authorize() {
    return this.workflow.authorize();
  }
  checkAuthorization() {
    return this.workflow.checkAuthorization();
  }
  changeId() {
    return this.workflow.changeId();
  }
  cancelReview() {
    return this.workflow.cancelReview();
  }
  apply() {
    return this.workflow.apply();
  }
  checkStatus() {
    return this.workflow.checkStatus();
  }
  async load(cursor = "") {
    if (
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed"
    )
      return;
    this.workflow.cancelReview();
    const { ticket, signal } = this.operation();
    this.publish({
      phase: "loading",
      message: this.state.recoveryBlocked
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
      const rolePage =
        this.section === "users"
          ? await this.port.roles("", signal)
          : undefined;
      if (rolePage) {
        validateVersion(rolePage.version);
        if (
          currentSecurityVersionBinding(rolePage.version) !==
          currentSecurityVersionBinding(page.version)
        )
          throw new Error("Policy changed while loading Role choices.");
      }
      if (!this.current(ticket)) return;
      this.publish({
        phase: "ready",
        version: page.version,
        nextCursor: page.nextCursor,
        issuers: "issuers" in page ? page.issuers : [],
        users: "users" in page ? page.users : [],
        roles: "roles" in page ? page.roles : (rolePage?.roles ?? []),
        roleCursor:
          "roles" in page ? page.nextCursor : (rolePage?.nextCursor ?? ""),
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
  async loadPolicies(issuerCursor = "") {
    if (
      this.state.mutation === "sending" ||
      this.state.mutation === "unconfirmed"
    )
      return;
    this.workflow.cancelReview();
    const { ticket, signal } = this.operation();
    this.publish({ policyPhase: "loading", policyMessage: "" });
    try {
      const [edge, registration, issuers, joining] = await Promise.all([
        this.port.edgePolicy(signal),
        this.port.registrationPolicy(signal),
        this.port.issuers(issuerCursor, signal),
        this.port.role("personal_user", signal).catch((error: unknown) => {
          if (this.port.failure(error) === "not-found") return undefined;
          throw error;
        }),
      ]);
      validateVersion(edge.version);
      for (const response of [registration, issuers, joining]) {
        if (!response) continue;
        validateVersion(response.version);
        if (
          currentSecurityVersionBinding(edge.version) !==
          currentSecurityVersionBinding(response.version)
        )
          throw new Error("Policy changed while reading shared settings.");
      }
      if (
        !edge.policy ||
        edge.policy.version !== 1 ||
        (registration.policy && registration.policy.version !== 2)
      )
        throw new Error("Unsupported policy version");
      if (!this.current(ticket)) return;
      this.publish({
        policyPhase: "ready",
        policyMessage: "",
        edgePolicy: edge.policy,
        registrationPolicy: registration.policy,
        joiningRole: joining?.role,
        policyIssuers: issuers.issuers,
        policyIssuerCursor: issuers.nextCursor,
        version: edge.version,
      });
    } catch (error) {
      if (this.current(ticket))
        this.publish({
          policyPhase: "error",
          policyMessage:
            this.port.failure(error) === "denied"
              ? "Shared Security settings require a current Security read grant. Sign in again or ask an administrator."
              : "Shared Security settings are unavailable or changed. Reload before reviewing a policy change.",
          edgePolicy: undefined,
          registrationPolicy: undefined,
          joiningRole: undefined,
        });
    }
  }
  resetTemplates() {
    this.publish({ templates: [], templatePhase: "idle", templateMessage: "" });
  }
  async loadTemplates(prefix: string) {
    if (this.state.mutation === "sending") return;
    this.workflow.cancelReview();
    const { ticket, signal } = this.operation();
    this.publish({
      templatePhase: "loading",
      templateMessage: "",
      templates: [],
    });
    try {
      const response = await this.port.templates(prefix, signal);
      validateVersion(response.version);
      if (this.current(ticket))
        this.publish({ templates: response.roles, templatePhase: "ready" });
    } catch (error) {
      if (this.current(ticket))
        this.publish({
          templatePhase: "error",
          templateMessage:
            this.port.failure(error) === "denied"
              ? "Role templates require a current authenticated Security read grant. Sign in again or ask an administrator."
              : "Role templates are unavailable. Check the Server connection and load again.",
        });
    }
  }
  async reviewNewRole(label: string, changes: SecurityChange[], id: string) {
    this.workflow.cancelReview();
    const { ticket, signal } = this.operation();
    try {
      await this.port.role(id, signal);
      if (this.current(ticket))
        this.publish({
          message:
            "That Role ID already exists. Choose a unique new Role ID; existing Roles are edited from saved details.",
        });
    } catch (error) {
      if (!this.current(ticket)) return;
      if (this.port.failure(error) === "not-found")
        this.workflow.review(label, changes);
      else
        this.publish({
          message:
            "The new Role ID could not be checked. Reload and retry the review; no change was sent.",
        });
    }
  }
  async readSavedRole(id: string): Promise<SecurityRole | undefined> {
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.role(id, signal);
      validateVersion(response.version);
      if (!response.role || !this.current(ticket)) return;
      this.publish({
        phase: "ready",
        version: response.version,
        roles: [
          response.role,
          ...this.state.roles.filter((role) => role.id !== id),
        ],
      });
      return response.role;
    } catch {
      if (this.current(ticket))
        this.publish({
          message:
            "The original change applied, but saved Role details could not be read. Reload before editing again.",
        });
    }
  }
  async readSavedUser(
    identity: SecurityIdentity,
  ): Promise<SecurityUser | undefined> {
    const { ticket, signal } = this.operation();
    try {
      const response = await this.port.user(identity, signal);
      validateVersion(response.version);
      if (!response.user || !this.current(ticket)) return;
      this.publish({
        phase: "ready",
        version: response.version,
        users: [
          response.user,
          ...this.state.users.filter(
            (user) =>
              JSON.stringify(user.identity) !== JSON.stringify(identity),
          ),
        ],
      });
      return response.user;
    } catch {
      if (this.current(ticket))
        this.publish({
          message:
            "The original change applied, but saved User details could not be read. Reload before editing again.",
        });
    }
  }
  async loadRoleChoices(cursor = "") {
    const inspected = this.state.version;
    if (!inspected || this.state.mutation === "sending") return;
    const { ticket, signal } = this.operation();
    try {
      const page = await this.port.roles(cursor, signal);
      validateVersion(page.version);
      if (
        currentSecurityVersionBinding(page.version) !==
        currentSecurityVersionBinding(inspected)
      )
        throw new Error("Policy changed");
      if (this.current(ticket))
        this.publish({ roles: page.roles, roleCursor: page.nextCursor });
    } catch {
      if (this.current(ticket))
        this.publish({
          message:
            "Role choices changed or are unavailable. Reload security state before assigning.",
        });
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
          members: response.users,
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
    permission?: number,
    resourceKind?: number,
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
        permission,
        resourceKind,
      );
      validateVersion(response.version);
      if (this.current(ticket)) this.publish({ explanation: response });
    } catch {
      if (this.current(ticket))
        this.publish({ message: "Access explanation is unavailable." });
    }
  }
}
