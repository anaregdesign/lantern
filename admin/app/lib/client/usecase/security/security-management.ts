import { currentSecurityVersionBinding } from "lantern-sdk/web";
import type {
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
export type SecuritySection = "issuers" | "users" | "roles";
export interface SecurityManagementPort extends CurrentChangePort {
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
}
export interface SecurityManagementState extends CurrentChangeState {
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
  constructor(
    private readonly port: SecurityManagementPort,
    private readonly section: SecuritySection,
    private readonly scope: AbortSignal,
    recovery?: SecurityChangeRecovery,
    recoveryOwner = "",
  ) {
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
      audit: [],
      auditCursor: "",
      members: [],
      memberCursor: "",
      memberRole: "",
    };
    this.unsubscribeWorkflow = this.workflow.subscribe(() => {
      this.state = { ...this.state, ...this.workflow.getSnapshot() };
      this.listeners.forEach((listener) => listener());
    });
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
