import {
  CurrentSecurityDisposition,
  OperationalGroup,
  OperationalSettingsChangeMode,
  OperationalSettingsError,
  copySecurityContract,
  currentSecurityVersionBinding,
  type GetOperationalSettingsResponse,
  type PreviewOperationalPolicyResponse,
  type OperationalPolicyValues,
  type OperationalGroupSettings,
  type OperationalBackupRetentionPreview,
} from "lantern-sdk/web";
import {
  CurrentChangeController,
  type CurrentChangePort,
  type CurrentChangeState,
} from "../current-change/current-change";
import type { SecurityChangeRecovery } from "../security/security-change-recovery";
import {
  operationalDefinition,
  operationalDraft,
  operationalValues,
  type OperationalDraft,
} from "./operational-settings-fields";
export interface OperationalSettingsPort extends CurrentChangePort {
  settings(signal: AbortSignal): Promise<GetOperationalSettingsResponse>;
  preview(
    values: OperationalPolicyValues,
    signal: AbortSignal,
  ): Promise<PreviewOperationalPolicyResponse>;
}
export type OperationalSettingsState = CurrentChangeState & {
  settings?: GetOperationalSettingsResponse;
  selected?: OperationalGroup;
  draft?: OperationalDraft;
  retentionAcknowledged: boolean;
  backupPreview?: OperationalBackupRetentionPreview;
  stale: boolean;
  originalId: string;
};
export function operationalBlocked(
  state: OperationalSettingsState,
  canMutate: boolean,
): boolean {
  return (
    !canMutate ||
    state.stale ||
    state.phase !== "ready" ||
    !state.version ||
    !!state.recoveryBlocked ||
    ["sending", "unconfirmed", "pending"].includes(state.mutation)
  );
}
export function operationalSelected(
  state: OperationalSettingsState,
): OperationalGroupSettings | undefined {
  return state.settings?.groups.find((group) => group.group === state.selected);
}
export function retentionAcknowledgementRequired(
  state: OperationalSettingsState,
): boolean {
  if (state.selected !== OperationalGroup.BACKUP || !state.draft) return false;
  try {
    const values = operationalValues(state.selected, state.draft);
    if (values.group.case !== "backup" || values.group.value.retain === 0n)
      return false;
    const desired = operationalSelected(state)?.desired?.values;
    return (
      desired?.group.case !== "backup" ||
      desired.group.value.retain === 0n ||
      values.group.value.retain < desired.group.value.retain
    );
  } catch {
    return false;
  }
}
/** All eight groups share one current-change/recovery owner. */
export class OperationalSettingsController {
  readonly change: CurrentChangeController;
  private settings?: GetOperationalSettingsResponse;
  private selected?: OperationalGroup;
  private draft?: OperationalDraft;
  private retentionAcknowledged = false;
  private backupPreview?: OperationalBackupRetentionPreview;
  private stale = false;
  private disposed = false;
  private state: OperationalSettingsState;
  private readonly listeners = new Set<() => void>();
  private readonly unsubscribe: () => void;
  constructor(
    private readonly port: OperationalSettingsPort,
    scope: AbortSignal,
    recovery: SecurityChangeRecovery,
    owner: string,
    private readonly canMutate: boolean,
  ) {
    this.change = new CurrentChangeController(
      port,
      scope,
      recovery,
      owner,
      (result) =>
        result.original?.disposition === CurrentSecurityDisposition.APPLIED
          ? "The operational policy was committed. Reload this node to check actual application."
          : "The original outcome is retained. Reload settings and review before another change.",
    );
    this.state = this.snapshot();
    this.unsubscribe = this.change.subscribe(() => this.publish());
  }
  private snapshot(): OperationalSettingsState {
    const change = this.change.getSnapshot();
    return {
      ...change,
      settings: this.settings,
      selected: this.selected,
      draft: this.draft,
      retentionAcknowledged: this.retentionAcknowledged,
      backupPreview: this.backupPreview,
      stale: this.stale || (!!this.settings && !change.version),
      originalId: this.change.changeId(),
    };
  }
  private publish() {
    if (this.disposed) return;
    this.state = this.snapshot();
    this.listeners.forEach((listener) => listener());
  }
  getSnapshot = () => this.state;
  subscribe = (listener: () => void) => {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  };
  dispose() {
    this.disposed = true;
    this.unsubscribe();
    this.change.dispose();
    this.listeners.clear();
  }
  select(group: OperationalGroup, useNodeValues = false) {
    if (operationalBlocked(this.state, this.canMutate)) return;
    const setting = this.settings?.groups.find((s) => s.group === group);
    if (
      !setting ||
      setting.changeMode === OperationalSettingsChangeMode.UNAVAILABLE ||
      (!setting.effective &&
        setting.error !== OperationalSettingsError.INVALID_POLICY)
    )
      return;
    this.change.cancelReview();
    this.selected = group;
    this.retentionAcknowledged = false;
    this.backupPreview = undefined;
    const values =
      setting.desired?.values ??
      (useNodeValues ? setting.effective : undefined);
    this.draft = values
      ? operationalDraft(values)
      : setting.error === OperationalSettingsError.INVALID_POLICY
        ? Object.fromEntries(
            operationalDefinition(group).fields.map((field) => [field.key, ""]),
          )
        : undefined;
    this.publish();
  }
  edit(key: string, value: string) {
    if (
      operationalBlocked(this.state, this.canMutate) ||
      !this.draft ||
      this.selected === undefined ||
      !operationalDefinition(this.selected).fields.some((f) => f.key === key) ||
      value.length > 80
    )
      return;
    this.change.cancelReview();
    this.draft = { ...this.draft, [key]: value };
    this.retentionAcknowledged = false;
    this.backupPreview = undefined;
    this.publish();
  }
  acknowledge(value: boolean) {
    if (operationalBlocked(this.state, this.canMutate)) return;
    this.change.cancelReview();
    this.retentionAcknowledged = value;
    this.publish();
  }
  async load() {
    if (this.change.getSnapshot().mutation === "sending") return;
    this.change.cancelReview();
    const { ticket, signal } = this.change.operation();
    this.stale = !!this.settings;
    this.change.publish({ phase: "loading", review: undefined, message: "" });
    try {
      const settings = await this.port.settings(signal);
      if (!this.change.current(ticket)) return;
      this.settings = copySecurityContract(settings);
      this.stale = false;
      this.draft = undefined;
      this.selected = undefined;
      this.retentionAcknowledged = false;
      this.backupPreview = undefined;
      this.change.publish({
        phase: "ready",
        version: settings.version,
        message: "",
      });
    } catch (error) {
      if (!this.change.current(ticket)) return;
      this.change.publish({
        phase: "error",
        version: undefined,
        message:
          this.port.failure(error) === "denied"
            ? "Settings management permission is required."
            : "Current operational settings are unavailable. Previously displayed values are stale; reload before making changes.",
      });
    }
  }
  async previewBackup() {
    if (
      operationalBlocked(this.state, this.canMutate) ||
      this.selected !== OperationalGroup.BACKUP ||
      !this.draft
    )
      return;
    let values: OperationalPolicyValues;
    try {
      values = operationalValues(this.selected, this.draft);
    } catch {
      this.change.publish({
        message: "Enter valid backup values before previewing retention.",
      });
      return;
    }
    this.change.cancelReview();
    const { ticket, signal } = this.change.operation();
    this.backupPreview = undefined;
    this.retentionAcknowledged = false;
    this.change.publish({
      phase: "loading",
      message: "Previewing retention on this connected node.",
    });
    try {
      const preview = await this.port.preview(values, signal);
      if (!this.change.current(ticket)) return;
      if (
        currentSecurityVersionBinding(preview.version) !==
        currentSecurityVersionBinding(this.state.version)
      ) {
        this.change.publish({
          phase: "ready",
          version: undefined,
          message:
            "Settings changed during retention preview. Reload and review again.",
        });
        return;
      }
      this.backupPreview = copySecurityContract(preview.backupRetention);
      this.change.publish({
        phase: "ready",
        message: this.backupPreview?.complete
          ? ""
          : "The connected-node catalog preview is incomplete. Resolve the catalog limit or availability, then preview again before reducing retention.",
      });
    } catch {
      if (this.change.current(ticket))
        this.change.publish({
          phase: "ready",
          message:
            "The connected-node retention preview is unavailable. No change was sent. Keep-all adoption remains available.",
        });
    }
  }
  async review() {
    if (
      operationalBlocked(this.state, this.canMutate) ||
      this.selected === undefined ||
      !this.draft
    )
      return;
    const group = this.selected,
      setting = operationalSelected(this.state);
    if (
      !setting ||
      setting.changeMode === OperationalSettingsChangeMode.UNAVAILABLE ||
      (!setting.effective &&
        setting.error !== OperationalSettingsError.INVALID_POLICY)
    )
      return;
    let previewCurrent = () => true;
    let previewStarted = false;
    try {
      const values = operationalValues(group, this.draft);
      if (
        retentionAcknowledgementRequired(this.state) &&
        !this.backupPreview?.complete
      )
        throw new Error(
          "Complete the connected-node retention preview before reducing retention.",
        );
      if (
        retentionAcknowledgementRequired(this.state) &&
        !this.retentionAcknowledged
      )
        throw new Error(
          "Acknowledge that future backup retention may remove older backups.",
        );
      let compatibilityFingerprint = "";
      if (group === OperationalGroup.SEARCH) {
        const { ticket, signal } = this.change.operation();
        previewCurrent = () => this.change.current(ticket);
        previewStarted = true;
        this.change.publish({
          phase: "loading",
          message: "Checking search compatibility.",
        });
        const preview = await this.port.preview(values, signal);
        if (!this.change.current(ticket)) return;
        if (
          currentSecurityVersionBinding(preview.version) !==
          currentSecurityVersionBinding(this.state.version)
        )
          throw new Error(
            "Settings changed during compatibility preview. Reload and review again.",
          );
        compatibilityFingerprint = preview.compatibilityFingerprint;
        this.change.publish({ phase: "ready", message: "" });
      }
      await this.change.review(
        `Update ${operationalDefinition(group).label.toLowerCase()} as one atomic policy.`,
        [
          {
            $typeName: "graph.v1.SecurityChange",
            operation: {
              case: "putOperationalPolicy",
              value: {
                $typeName: "graph.v1.OperationalPolicyChange",
                version: 1,
                expectedGeneration: setting.desired?.generation ?? 0n,
                values,
                compatibilityFingerprint,
                retentionReductionAcknowledged:
                  group === OperationalGroup.BACKUP &&
                  this.retentionAcknowledged,
              },
            },
          },
        ],
      );
    } catch (error) {
      if (!previewCurrent()) return;
      this.change.publish({
        phase: "ready",
        message: previewStarted
          ? "Search compatibility could not be confirmed. Reload and review again; no Apply was sent."
          : error instanceof Error
            ? error.message
            : "The operational values are invalid.",
      });
    }
  }
  async apply() {
    if (this.canMutate) await this.change.apply();
  }
  async checkStatus() {
    await this.change.checkStatus();
  }
}
