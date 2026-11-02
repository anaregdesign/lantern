import { equals } from "@bufbuild/protobuf";
import {
  OperationalGroup,
  OperationalSearchMode,
  OperationalSettingsSource,
  OperationalSettingsChangeMode,
  OperationalSettingsApplicationState,
  OperationalSettingsError,
  LogPolicyAdoptionState,
  OperationalPolicyValuesSchema,
  type OperationalPolicyValues,
  type OperationalPolicyChange,
  type GetOperationalSettingsResponse,
  type PreviewOperationalPolicyResponse,
  type SecurityChange,
  type OperationalGroupSettings,
} from "./gen/graph/v1/security_pb.js";
import {
  currentChangeIdBinding,
  currentCutBinding,
  currentSecurityVersionBinding,
} from "./security-current.js";
export {
  OperationalGroup,
  OperationalSearchMode,
  OperationalSetting,
  OperationalSettingsSource,
  OperationalSettingsChangeMode,
  OperationalSettingsApplicationState,
  OperationalSettingsError,
} from "./gen/graph/v1/security_pb.js";
export type {
  OperationalSlowRPCValues,
  OperationalRateValues,
  OperationalRequestValues,
  OperationalBackupValues,
  OperationalCapacityValues,
  OperationalLifecycleValues,
  OperationalSearchValues,
  OperationalReplicationValues,
  OperationalPolicyValues,
  OperationalPolicyChange,
  ManagedOperationalPolicy,
  OperationalGroupSettings,
  OperationalSettingProvenance,
  GetOperationalSettingsResponse,
  PreviewOperationalPolicyResponse,
  OperationalBackupRetentionPreview,
} from "./gen/graph/v1/security_pb.js";
function fail(): never {
  throw new Error("Invalid operational settings contract.");
}
const i64 = 0x7fffffffffffffffn,
  u64 = 0xffffffffffffffffn;
function integer(v: bigint, min = 0n, max = i64): boolean {
  return typeof v === "bigint" && v >= min && v <= max;
}
function millis(v: bigint, zero = true): boolean {
  return integer(v, zero ? 0n : 1n, i64 / 1_000_000n);
}
function seconds(v: bigint): boolean {
  return integer(v, 0n, i64 / 1_000_000_000n);
}
function pair(def: bigint, max: bigint): boolean {
  return integer(def, 1n, 0xffffffffn) && integer(max, def, 0xffffffffn);
}
export const operationalGroupOrder = [
  OperationalGroup.SLOW_RPC,
  OperationalGroup.RATE,
  OperationalGroup.REQUEST,
  OperationalGroup.BACKUP,
  OperationalGroup.CAPACITY,
  OperationalGroup.LIFECYCLE,
  OperationalGroup.SEARCH,
  OperationalGroup.REPLICATION,
] as const;
const bounds = [
  [1, 1],
  [2, 3],
  [4, 15],
  [16, 18],
  [19, 24],
  [25, 27],
  [28, 38],
  [39, 43],
] as const;
export function operationalSettingNumbers(group: OperationalGroup): number[] {
  const range = bounds[group - 1];
  if (!range || !operationalGroupOrder.includes(group as (typeof operationalGroupOrder)[number]))
    fail();
  return Array.from({ length: range[1] - range[0] + 1 }, (_, i) => range[0] + i);
}
/** Closed typed union; runtime dependency checks remain authoritative on Server. */
export function validateOperationalValues(
  values: OperationalPolicyValues | undefined,
): OperationalGroup {
  if (!values) return fail();
  switch (values.group.case) {
    case "slowRpc":
      if (!millis(values.group.value.thresholdMs)) fail();
      return OperationalGroup.SLOW_RPC;
    case "rate": {
      const r = values.group.value;
      if (
        !Number.isFinite(r.rps) ||
        r.rps < 0 ||
        Object.is(r.rps, -0) ||
        !integer(r.burst) ||
        !((r.rps === 0 && r.burst === 0n) || (r.rps > 0 && r.burst > 0n))
      )
        fail();
      return OperationalGroup.RATE;
    }
    case "request": {
      const r = values.group.value;
      if (
        !pair(r.deleteByPrefixDefaultLimit, r.deleteByPrefixMaxLimit) ||
        !pair(r.scanDefaultLimit, r.scanMaxLimit) ||
        ![r.illuminateMaxK, r.illuminateMaxStep, r.maxBatchSize, r.maxKeyLen].every((v) =>
          integer(v),
        ) ||
        ![r.traversalMaxPushes, r.traversalMaxResults, r.traversalMaxTouchedEdges].every((v) =>
          integer(v, 1n),
        ) ||
        !millis(r.traversalTimeoutMs)
      )
        fail();
      return OperationalGroup.REQUEST;
    }
    case "backup": {
      const b = values.group.value;
      if (typeof b.enabled !== "boolean" || !integer(b.intervalNanos, 1n) || !integer(b.retain))
        fail();
      return OperationalGroup.BACKUP;
    }
    case "capacity": {
      const c = values.group.value;
      if (
        ![
          c.gcEdgeBudget,
          c.maxEdges,
          c.maxEdgeCausalEntries,
          c.maxVertexCausalEntries,
          c.maxVertices,
        ].every((v) => integer(v)) ||
        !seconds(c.gcIntervalSeconds) ||
        c.gcIntervalSeconds === 0n
      )
        fail();
      return OperationalGroup.CAPACITY;
    }
    case "lifecycle": {
      const l = values.group.value;
      if (
        !seconds(l.drainDelaySeconds) ||
        !seconds(l.shutdownTimeoutSeconds) ||
        !integer(l.maxReplicationLag, 0n, u64)
      )
        fail();
      return OperationalGroup.LIFECYCLE;
    }
    case "search": {
      const s = values.group.value;
      if (
        !pair(s.defaultLimit, s.maxLimit) ||
        ![
          OperationalSearchMode.ANY,
          OperationalSearchMode.ALL,
          OperationalSearchMode.MIN_SHOULD,
        ].includes(s.defaultMode) ||
        !integer(s.defaultMinShould, 1n, 0xffffffffn) ||
        !millis(s.timeoutMs, false) ||
        ![
          s.maxQueryBytes,
          s.maxQueryTerms,
          s.maxDictionaryVisits,
          s.maxPostingVisits,
          s.maxPositionVisits,
          s.maxExpirationVisits,
        ].every((v) => integer(v, 1n)) ||
        s.maxQueryTerms > s.maxQueryBytes
      )
        fail();
      return OperationalGroup.SEARCH;
    }
    case "replication": {
      const r = values.group.value;
      if (
        !integer(r.antiEntropyGapWarnThreshold) ||
        !millis(r.antiEntropyIntervalMs) ||
        ![r.antiEntropySubscribeTimeoutMs, r.pumpBackoffMaxMs, r.pumpBackoffMinMs].every((v) =>
          millis(v, false),
        ) ||
        r.pumpBackoffMinMs > r.pumpBackoffMaxMs
      )
        fail();
      return OperationalGroup.REPLICATION;
    }
    default:
      return fail();
  }
}
function fingerprint(group: OperationalGroup, value: string): void {
  if (group === OperationalGroup.SEARCH ? !/^[0-9a-f]{64}$/.test(value) : value !== "") fail();
}
export function validateOperationalChange(change: OperationalPolicyChange): void {
  const group = validateOperationalValues(change.values);
  if (
    change.version !== 1 ||
    !integer(change.expectedGeneration, 0n, u64 - 1n) ||
    typeof change.retentionReductionAcknowledged !== "boolean" ||
    (change.retentionReductionAcknowledged && group !== OperationalGroup.BACKUP)
  )
    fail();
  fingerprint(group, change.compatibilityFingerprint);
}
export function validateOperationalChanges(changes: readonly SecurityChange[]): void {
  for (const change of changes)
    if (change.operation.case === "putOperationalPolicy") {
      if (changes.length !== 1) fail();
      validateOperationalChange(change.operation.value);
    }
}
export function validateOperationalPreview(
  response: PreviewOperationalPolicyResponse,
  values: OperationalPolicyValues,
): void {
  if (response.schemaVersion !== 1 || response.group !== validateOperationalValues(values)) fail();
  fingerprint(response.group, response.compatibilityFingerprint);
  currentSecurityVersionBinding(response.version);
  const preview = response.backupRetention;
  if (response.group === OperationalGroup.BACKUP) {
    if (
      !preview ||
      ![preview.validBackups, preview.validBytes, preview.pruneBackups, preview.pruneBytes].every(
        (v) => integer(v, 0n, u64),
      ) ||
      preview.pruneBackups > preview.validBackups ||
      preview.pruneBytes > preview.validBytes ||
      ((preview.complete || preview.fingerprint !== "") &&
        !/^[0-9a-f]{64}$/.test(preview.fingerprint))
    )
      fail();
  } else if (preview) fail();
}
export function validateOperationalSettings(settings: GetOperationalSettingsResponse): void {
  if (
    settings.schemaVersion !== 1 ||
    !Number.isInteger(settings.member) ||
    settings.member < 1 ||
    settings.member > 0xffffffff ||
    settings.groups.length !== 8
  )
    fail();
  currentSecurityVersionBinding(settings.version);
  settings.groups.forEach((state, index) => {
    const group = operationalGroupOrder[index]!;
    if (
      state.group !== group ||
      !integer(state.appliedGeneration, 0n, u64) ||
      !Number.isInteger(state.error) ||
      state.error < OperationalSettingsError.NONE ||
      state.error > OperationalSettingsError.COMPATIBILITY_MISMATCH
    )
      fail();
    if (state.preparation) {
      currentChangeIdBinding(state.preparation.changeId, settings.version?.currentProfile);
      for (const digest of [state.preparation.intentDigest, state.preparation.actorDigest])
        if (!(digest instanceof Uint8Array) || digest.length !== 32 || !digest.some(Boolean))
          fail();
    }
    if (state.desired) {
      const d = state.desired;
      if (
        d.version !== 1 ||
        !integer(d.generation, 1n, u64) ||
        validateOperationalValues(d.values) !== group ||
        state.adoption !== LogPolicyAdoptionState.MANAGED ||
        !state.preparation
      )
        fail();
      fingerprint(group, d.compatibilityFingerprint);
    } else if (
      state.adoption !==
      (state.preparation
        ? LogPolicyAdoptionState.FENCED_UNADOPTED
        : LogPolicyAdoptionState.UNADOPTED)
    )
      fail();
    if (
      state.restartRequired &&
      (!state.desired ||
        ![
          OperationalSettingsChangeMode.PROCESS_RESTART,
          OperationalSettingsChangeMode.COORDINATED_RESTART,
        ].includes(state.changeMode))
    )
      fail();
    if (!state.effective) {
      if (
        state.effectiveCompatibilityFingerprint !== "" ||
        state.appliedGeneration !== 0n ||
        state.appliedCut
      )
        fail();
      if (
        state.changeMode === OperationalSettingsChangeMode.UNAVAILABLE &&
        state.application === OperationalSettingsApplicationState.UNAVAILABLE &&
        state.error === OperationalSettingsError.OWNER_UNAVAILABLE &&
        state.provenance.length === 0 &&
        !state.restartRequired
      )
        return;
      if (
        state.changeMode < OperationalSettingsChangeMode.HOT ||
        state.changeMode > OperationalSettingsChangeMode.COORDINATED_RESTART ||
        state.application !== OperationalSettingsApplicationState.ERROR ||
        state.error !== OperationalSettingsError.INVALID_POLICY
      )
        fail();
      validateOperationalProvenance(state, group);
      return;
    }

    if (
      validateOperationalValues(state.effective) !== group ||
      state.changeMode < OperationalSettingsChangeMode.HOT ||
      state.changeMode > OperationalSettingsChangeMode.COORDINATED_RESTART
    )
      fail();
    fingerprint(group, state.effectiveCompatibilityFingerprint);
    validateOperationalProvenance(state, group);

    const desiredGeneration = state.desired?.generation ?? 0n;
    if (state.appliedGeneration > desiredGeneration) fail();
    if (state.appliedGeneration > 0n) {
      currentCutBinding(state.appliedCut, settings.version?.currentProfile);
      if (
        !state.appliedCut ||
        !settings.version?.currentCut ||
        state.appliedCut.sequence > settings.version.currentCut.sequence ||
        (state.appliedCut.sequence === settings.version.currentCut.sequence &&
          currentCutBinding(state.appliedCut, settings.version.currentProfile) !==
            currentCutBinding(settings.version.currentCut, settings.version.currentProfile))
      )
        fail();
      if (
        state.appliedGeneration === desiredGeneration &&
        (!equals(OperationalPolicyValuesSchema, state.effective, state.desired!.values!) ||
          state.effectiveCompatibilityFingerprint !== state.desired!.compatibilityFingerprint)
      )
        fail();
    } else if (state.appliedCut) fail();
    if (state.error !== OperationalSettingsError.NONE) {
      if (state.application !== OperationalSettingsApplicationState.ERROR) fail();
    } else if (state.application === OperationalSettingsApplicationState.PENDING) {
      if (
        !state.desired ||
        state.appliedGeneration >= desiredGeneration ||
        (state.restartRequired &&
          ![
            OperationalSettingsChangeMode.PROCESS_RESTART,
            OperationalSettingsChangeMode.COORDINATED_RESTART,
          ].includes(state.changeMode))
      )
        fail();
    } else if (
      state.application !== OperationalSettingsApplicationState.APPLIED ||
      state.appliedGeneration !== desiredGeneration ||
      state.restartRequired
    )
      fail();
  });
}

function validateOperationalProvenance(
  state: OperationalGroupSettings,
  group: OperationalGroup,
): void {
  const fields = operationalSettingNumbers(group);
  if (state.provenance.length !== fields.length) fail();
  state.provenance.forEach((p, i) => {
    if (
      p.setting !== fields[i] ||
      p.source < OperationalSettingsSource.DEFAULT ||
      p.source > OperationalSettingsSource.MANAGED ||
      (p.source === OperationalSettingsSource.MANAGED) !== state.appliedGeneration > 0n
    )
      fail();
  });
}
