import {
  CurrentSecurityDisposition,
  ManagedLogLevel,
  LogSettingsSource,
  LogPolicyAdoptionState,
  LogSettingsApplicationState,
  LogSettingsError,
  copySecurityContract,
  type GetLogSettingsResponse,
  type CurrentSecurityChangeResult,
  type CurrentSecurityChangeID,
} from "lantern-sdk/web";
import {
  CurrentChangeController,
  type CurrentChangePort,
  type CurrentChangeState,
} from "../current-change/current-change";
import type { SecurityChangeRecovery } from "../security/security-change-recovery";

export interface LogSettingsPort extends CurrentChangePort {
  settings(signal: AbortSignal): Promise<GetLogSettingsResponse>;
}

export type SettingsState = CurrentChangeState & {
  settings?: GetLogSettingsResponse;
  draft: ManagedLogLevel;
  stale: boolean;
  originalId: string;
};

export const logLevelChoices = [
  { value: ManagedLogLevel.DEBUG, label: "Debug" },
  { value: ManagedLogLevel.INFO, label: "Info" },
  { value: ManagedLogLevel.WARN, label: "Warn" },
  { value: ManagedLogLevel.ERROR, label: "Error" },
] as const;

export function logLevelLabel(level: ManagedLogLevel): string {
  return (
    logLevelChoices.find((choice) => choice.value === level)?.label ?? "Unknown"
  );
}

function changeIdLabel(id: CurrentSecurityChangeID | undefined): string {
  return id
    ? `${id.namespace}:${Array.from(id.nonce, (byte) => byte.toString(16).padStart(2, "0")).join("")}`
    : "";
}

function resultMessage(result: CurrentSecurityChangeResult): string {
  if (!result.original)
    return "The original change is retained. Check its status before another change.";
  return result.original.disposition === CurrentSecurityDisposition.APPLIED
    ? "The log policy was committed. Reload this node's settings to check application."
    : "The original change was refused. Reload and review the current policy before another change.";
}

/** One screen's reads and draft, composed with the shared original-change owner. */
export class SettingsController {
  readonly change: CurrentChangeController;
  private state: SettingsState;
  private settings?: GetLogSettingsResponse;
  private draft = ManagedLogLevel.UNSPECIFIED;
  private stale = false;
  private disposed = false;
  private readonly listeners = new Set<() => void>();
  private readonly unsubscribe: () => void;

  constructor(
    private readonly port: LogSettingsPort,
    scope: AbortSignal,
    recovery?: SecurityChangeRecovery,
    owner = "",
    private readonly canMutate = true,
  ) {
    this.change = new CurrentChangeController(
      port,
      scope,
      recovery,
      owner,
      resultMessage,
    );
    this.state = this.snapshot();
    this.unsubscribe = this.change.subscribe(() => this.publish());
  }

  private snapshot(): SettingsState {
    const change = this.change.getSnapshot();
    return {
      ...change,
      settings: this.settings,
      draft: this.draft,
      stale: this.stale || (!!this.settings && !change.version),
      originalId: this.change.changeId(),
    };
  }
  private publish() {
    if (this.disposed) return;
    this.state = this.snapshot();
    this.listeners.forEach((listener) => listener());
  }
  getSnapshot = (): SettingsState => this.state;
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
  editLevel(value: ManagedLogLevel) {
    if (
      settingsView(this.state, this.canMutate).blocked ||
      !logLevelChoices.some((choice) => choice.value === value)
    )
      return;
    this.change.cancelReview();
    this.draft = value;
    this.publish();
  }
  async load() {
    const current = this.change.getSnapshot();
    // Observing current policy cannot settle a retained original. Reads may
    // reveal a partial version floor while that original remains status-only.
    if (current.mutation === "sending") return;
    this.change.cancelReview();
    const { ticket, signal } = this.change.operation();
    this.stale = !!this.settings;
    this.change.publish({ phase: "loading", review: undefined, message: "" });
    try {
      const settings = await this.port.settings(signal);
      if (!this.change.current(ticket)) return;
      this.settings = copySecurityContract(settings);
      this.stale = false;
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
            : "Current settings are unavailable. Any previously displayed value is stale; reload before making changes.",
      });
    }
  }
  async review() {
    if (!this.canMutate || !settingsView(this.state, this.canMutate).canReview)
      return;
    await this.change.review(`Set log level to ${logLevelLabel(this.draft)}.`, [
      {
        $typeName: "graph.v1.SecurityChange",
        operation: {
          case: "putLogPolicy",
          value: {
            $typeName: "graph.v1.ManagedLogPolicy",
            version: 1,
            level: this.draft,
          },
        },
      },
    ]);
  }
  async apply() {
    if (!this.canMutate) return;
    await this.change.apply();
  }
  async checkStatus() {
    await this.change.checkStatus();
  }
}

export type SettingsView = {
  canReview: boolean;
  busy: boolean;
  blocked: boolean;
  firstAdoption: boolean;
  fenced: boolean;
  member: string;
  effective: string;
  source: string;
  desired: string;
  generation: string;
  application: string;
  applicationError: string;
  changeId: string;
  preparationId: string;
};

/** Text and action eligibility have one owner; components only render these values. */
export function settingsView(
  state: SettingsState,
  canMutate: boolean,
): SettingsView {
  const settings = state.settings;
  const blocked =
    !canMutate ||
    state.stale ||
    state.phase !== "ready" ||
    !state.version ||
    !!state.recoveryBlocked ||
    ["sending", "unconfirmed", "pending"].includes(state.mutation);
  const sources = {
    [LogSettingsSource.DEFAULT]: "Default",
    [LogSettingsSource.ENVIRONMENT]: "Environment",
    [LogSettingsSource.MANAGED]: "Managed policy",
  };
  const applications = {
    [LogSettingsApplicationState.APPLIED]:
      settings?.adoption === LogPolicyAdoptionState.MANAGED &&
      settings.appliedGeneration > 0n
        ? "Applied on this node"
        : "Startup setting active",
    [LogSettingsApplicationState.PENDING]: "Pending on this node",
    [LogSettingsApplicationState.ERROR]: "Application failed on this node",
  };
  const errors = {
    [LogSettingsError.NONE]: "",
    [LogSettingsError.INVALID_POLICY]: "The managed policy is invalid.",
    [LogSettingsError.OWNER_UNAVAILABLE]: "The logging runtime is unavailable.",
    [LogSettingsError.APPLY_FAILED]:
      "The logging runtime could not apply this policy.",
  };
  return {
    canReview:
      !blocked &&
      logLevelChoices.some((choice) => choice.value === state.draft) &&
      (settings?.adoption !== LogPolicyAdoptionState.MANAGED ||
        settings.desired?.level !== state.draft),
    busy: state.mutation === "sending",
    blocked,
    firstAdoption:
      !!settings && settings.adoption !== LogPolicyAdoptionState.MANAGED,
    fenced: settings?.adoption === LogPolicyAdoptionState.FENCED_UNADOPTED,
    member: settings ? String(settings.member) : "",
    effective: settings ? logLevelLabel(settings.effectiveLevel) : "",
    source: settings
      ? (sources[settings.source as keyof typeof sources] ?? "Unknown")
      : "",
    desired: settings?.desired
      ? logLevelLabel(settings.desired.level)
      : "Not adopted",
    generation: settings
      ? `${settings.appliedGeneration} / ${settings.desiredGeneration}`
      : "",
    application: settings
      ? (applications[settings.application as keyof typeof applications] ??
        "Unknown")
      : "",
    applicationError: settings
      ? (errors[settings.error as keyof typeof errors] ??
        "Settings could not be verified.")
      : "",
    changeId: state.originalId,
    preparationId: changeIdLabel(settings?.preparation?.changeId),
  };
}
