import { expect, test } from "bun:test";
import {
  copySecurityContract,
  operationalGroupOrder,
  OperationalGroup,
  OperationalSettingsChangeMode,
  OperationalSettingsApplicationState,
  OperationalSettingsError,
  OperationalSettingsSource,
  LogPolicyAdoptionState,
  type GetOperationalSettingsResponse,
  type CurrentSecurityReview,
  type OperationalPolicyValues,
} from "lantern-sdk/web";
import {
  version,
  review,
  prepare,
  result,
  acknowledgement,
} from "../../../../../test/current-security";
import {
  SecurityChangeRecovery,
  operationalPolicyRecoveryOwner,
} from "../security/security-change-recovery";
import {
  OperationalSettingsController,
  operationalBlocked,
  retentionAcknowledgementRequired,
  type OperationalSettingsPort,
} from "./operational-settings-management";
function values(): OperationalPolicyValues {
  return {
    $typeName: "graph.v1.OperationalPolicyValues",
    group: {
      case: "slowRpc",
      value: {
        $typeName: "graph.v1.OperationalSlowRPCValues",
        thresholdMs: 500n,
      },
    },
  };
}
function settings(): GetOperationalSettingsResponse {
  const groups = operationalGroupOrder.map((group) => ({
    $typeName: "graph.v1.OperationalGroupSettings" as const,
    group,
    appliedGeneration: 0n,
    adoption: LogPolicyAdoptionState.UNADOPTED,
    changeMode: OperationalSettingsChangeMode.UNAVAILABLE,
    application: OperationalSettingsApplicationState.UNAVAILABLE,
    error: OperationalSettingsError.OWNER_UNAVAILABLE,
    restartRequired: false,
    provenance: [],
    effectiveCompatibilityFingerprint: "",
  }));
  const slow = groups[0]!;
  return {
    $typeName: "graph.v1.GetOperationalSettingsResponse",
    schemaVersion: 1,
    member: 1,
    version,
    groups: [
      {
        ...slow,
        effective: values(),
        changeMode: OperationalSettingsChangeMode.HOT,
        application: OperationalSettingsApplicationState.APPLIED,
        error: OperationalSettingsError.NONE,
        provenance: [
          {
            $typeName: "graph.v1.OperationalSettingProvenance",
            setting: 1,
            source: OperationalSettingsSource.ENVIRONMENT,
          },
        ],
      },
      ...groups.slice(1),
    ],
  };
}
function fixture(overrides: Partial<OperationalSettingsPort> = {}) {
  let raw: string | null = null,
    prepares = 0,
    sends = 0,
    latest: CurrentSecurityReview | undefined;
  const recovery = new SecurityChangeRecovery({
      read: () => raw,
      replace: (value) => {
        raw = value;
      },
    }),
    owner = operationalPolicyRecoveryOwner("https://admin.example", {
      identity: review().actor,
      version,
    });
  const port: OperationalSettingsPort = {
    settings: async () => settings(),
    preview: async () => {
      throw new Error("unexpected preview");
    },
    prepare: async (draft) => {
      prepares++;
      latest = prepare(draft).currentReview;
      return prepare(draft);
    },
    beginAuthorization: async () => {
      throw new Error("unexpected auth");
    },
    authorization: async () => {
      throw new Error("unexpected auth");
    },
    openAuthorization: () => ({ navigate() {}, close() {} }),
    invocationRejected: () => undefined,
    apply: async (request) => {
      sends++;
      return acknowledgement(result(request.currentReview));
    },
    status: async (original) => result(original),
    failure: () => "unavailable",
    ...overrides,
  };
  return {
    port,
    recovery,
    owner,
    controller: new OperationalSettingsController(
      port,
      new AbortController().signal,
      recovery,
      owner,
      true,
    ),
    prepares: () => prepares,
    sends: () => sends,
    latest: () => latest,
  };
}
test("operational initial adoption is explicit and stages exactly one atomic typed command", async () => {
  const f = fixture(),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.SLOW_RPC);
  expect(c.getSnapshot().draft).toBeUndefined();
  await c.review();
  expect(f.prepares()).toBe(0);
  c.select(OperationalGroup.SLOW_RPC, true);
  c.edit("thresholdMs", "125");
  await c.review();
  expect(f.prepares()).toBe(1);
  const op = f.latest()!.changes[0]!.operation;
  expect(op.case).toBe("putOperationalPolicy");
  if (op.case !== "putOperationalPolicy") throw new Error();
  expect(op.value.expectedGeneration).toBe(0n);
  expect(op.value.values?.group.case).toBe("slowRpc");
  expect(f.latest()!.changes).toHaveLength(1);
  await c.apply();
  expect(f.sends()).toBe(1);
});
test("operational editing starts from desired rather than lagging effective values", async () => {
  const response = settings(),
    desired = values();
  if (desired.group.case !== "slowRpc") throw new Error();
  desired.group.value.thresholdMs = 900n;
  response.groups[0]!.desired = {
    $typeName: "graph.v1.ManagedOperationalPolicy",
    version: 1,
    generation: 5n,
    values: desired,
    compatibilityFingerprint: "",
  };
  response.groups[0]!.adoption = LogPolicyAdoptionState.MANAGED;
  response.groups[0]!.application = OperationalSettingsApplicationState.PENDING;
  const f = fixture({ settings: async () => response });
  await f.controller.load();
  f.controller.select(OperationalGroup.SLOW_RPC);
  expect(f.controller.getSnapshot().draft?.thresholdMs).toBe("900");
  await f.controller.review();
  const op = f.latest()!.changes[0]!.operation;
  if (op.case !== "putOperationalPolicy") throw new Error();
  expect(op.value.expectedGeneration).toBe(5n);
});
test("operational lost response retains original across reload and blocks every group until status", async () => {
  const f = fixture({
      apply: async () => {
        throw new Error("lost response");
      },
    }),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.SLOW_RPC, true);
  await c.review();
  await c.apply();
  expect(c.getSnapshot().mutation).toBe("unconfirmed");
  const original = c.getSnapshot().originalId;
  await c.load();
  expect(c.getSnapshot().originalId).toBe(original);
  expect(operationalBlocked(c.getSnapshot(), true)).toBe(true);
  c.select(OperationalGroup.RATE, true);
  expect(c.getSnapshot().selected).toBeUndefined();
  const after = new OperationalSettingsController(
    f.port,
    new AbortController().signal,
    f.recovery,
    f.owner,
    true,
  );
  await after.load();
  expect(operationalBlocked(after.getSnapshot(), true)).toBe(true);
  await after.checkStatus();
  expect(after.getSnapshot().result?.original).toBeDefined();
  await after.load();
  expect(operationalBlocked(after.getSnapshot(), true)).toBe(false);
});
test("operational backup reduction seals acknowledgement and exact nanosecond interval", async () => {
  const response = settings(),
    backup = response.groups[3]!;
  backup.effective = {
    $typeName: "graph.v1.OperationalPolicyValues",
    group: {
      case: "backup",
      value: {
        $typeName: "graph.v1.OperationalBackupValues",
        enabled: true,
        intervalNanos: 9007199254740999n,
        retain: 3n,
      },
    },
  };
  backup.changeMode = OperationalSettingsChangeMode.WORKER_RESTART;
  backup.error = OperationalSettingsError.NONE;
  const f = fixture({
      settings: async () => response,
      preview: async () => ({
        $typeName: "graph.v1.PreviewOperationalPolicyResponse",
        schemaVersion: 1,
        group: OperationalGroup.BACKUP,
        compatibilityFingerprint: "",
        version,
        backupRetention: {
          $typeName: "graph.v1.OperationalBackupRetentionPreview",
          validBackups: 3n,
          validBytes: 300n,
          pruneBackups: 0n,
          pruneBytes: 0n,
          complete: true,
          fingerprint: "a".repeat(64),
        },
      }),
    }),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.BACKUP, true);
  expect(retentionAcknowledgementRequired(c.getSnapshot())).toBe(true);
  await c.review();
  expect(f.prepares()).toBe(0);
  await c.previewBackup();
  c.acknowledge(true);
  await c.review();
  const op = f.latest()!.changes[0]!.operation;
  if (op.case !== "putOperationalPolicy") throw new Error();
  expect(op.value.retentionReductionAcknowledged).toBe(true);
  expect(op.value.values?.group.case).toBe("backup");
});
test("operational search preview cannot authorize a review across a changed security cut", async () => {
  const response = settings(),
    search = response.groups[6]!;
  search.effective = {
    $typeName: "graph.v1.OperationalPolicyValues",
    group: {
      case: "search",
      value: {
        $typeName: "graph.v1.OperationalSearchValues",
        defaultLimit: 100n,
        maxLimit: 1000n,
        defaultMode: 1,
        defaultMinShould: 1n,
        timeoutMs: 5000n,
        maxQueryBytes: 1000n,
        maxQueryTerms: 100n,
        maxDictionaryVisits: 100n,
        maxPostingVisits: 100n,
        maxPositionVisits: 100n,
        maxExpirationVisits: 100n,
      },
    },
  };
  search.changeMode = OperationalSettingsChangeMode.COORDINATED_RESTART;
  const changed = copySecurityContract(version);
  changed.currentCut!.sequence++;
  const f = fixture({
      settings: async () => response,
      preview: async () => ({
        $typeName: "graph.v1.PreviewOperationalPolicyResponse",
        schemaVersion: 1,
        group: OperationalGroup.SEARCH,
        compatibilityFingerprint: "a".repeat(64),
        version: changed,
      }),
    }),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.SEARCH, true);
  await c.review();
  expect(f.prepares()).toBe(0);
  expect(c.getSnapshot().message).toContain("Reload");
  expect(c.getSnapshot().review).toBeUndefined();
  expect(operationalBlocked(c.getSnapshot(), true)).toBe(true);
});

test("operational incomplete retention preview refuses finite adoption but permits keep-all", async () => {
  const response = settings(),
    backup = response.groups[3]!;
  backup.effective = {
    $typeName: "graph.v1.OperationalPolicyValues",
    group: {
      case: "backup",
      value: {
        $typeName: "graph.v1.OperationalBackupValues",
        enabled: true,
        intervalNanos: 300000000000n,
        retain: 3n,
      },
    },
  };
  backup.changeMode = OperationalSettingsChangeMode.WORKER_RESTART;
  backup.error = OperationalSettingsError.NONE;
  const f = fixture({
      settings: async () => response,
      preview: async () => ({
        $typeName: "graph.v1.PreviewOperationalPolicyResponse",
        schemaVersion: 1,
        group: OperationalGroup.BACKUP,
        compatibilityFingerprint: "",
        version,
        backupRetention: {
          $typeName: "graph.v1.OperationalBackupRetentionPreview",
          validBackups: 10n,
          validBytes: 100n,
          pruneBackups: 0n,
          pruneBytes: 0n,
          complete: false,
          fingerprint: "",
        },
      }),
    }),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.BACKUP, true);
  await c.previewBackup();
  c.acknowledge(true);
  await c.review();
  expect(f.prepares()).toBe(0);
  expect(c.getSnapshot().message).toContain("Complete");
  c.edit("retain", "0");
  await c.review();
  expect(f.prepares()).toBe(1);
  const op = f.latest()!.changes[0]!.operation;
  if (op.case !== "putOperationalPolicy") throw new Error();
  expect(op.value.retentionReductionAcknowledged).toBe(false);
});

test("operational invalid bootstrap exposes no fabricated value and accepts an explicit repair candidate", async () => {
  const response = settings(),
    rate = response.groups[1]!;
  rate.changeMode = OperationalSettingsChangeMode.HOT;
  rate.application = OperationalSettingsApplicationState.ERROR;
  rate.error = OperationalSettingsError.INVALID_POLICY;
  rate.provenance = [
    {
      $typeName: "graph.v1.OperationalSettingProvenance",
      setting: 2,
      source: OperationalSettingsSource.ENVIRONMENT,
    },
    {
      $typeName: "graph.v1.OperationalSettingProvenance",
      setting: 3,
      source: OperationalSettingsSource.DERIVED,
    },
  ];
  const f = fixture({ settings: async () => response }),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.RATE);
  expect(c.getSnapshot().settings?.groups[1]!.effective).toBeUndefined();
  expect(c.getSnapshot().draft).toEqual({ rps: "", burst: "" });
  await c.review();
  expect(f.prepares()).toBe(0);
  c.edit("rps", "1e-100");
  c.edit("burst", "1");
  await c.review();
  expect(f.prepares()).toBe(1);
});

test("operational invalid backup repair requires an explicit enabled choice", async () => {
  const response = settings(),
    backup = response.groups[3]!;
  backup.changeMode = OperationalSettingsChangeMode.WORKER_RESTART;
  backup.application = OperationalSettingsApplicationState.ERROR;
  backup.error = OperationalSettingsError.INVALID_POLICY;
  backup.provenance = [16, 17, 18].map((setting) => ({
    $typeName: "graph.v1.OperationalSettingProvenance",
    setting,
    source: OperationalSettingsSource.ENVIRONMENT,
  }));
  const f = fixture({ settings: async () => response }),
    c = f.controller;
  await c.load();
  c.select(OperationalGroup.BACKUP);
  expect(c.getSnapshot().draft?.enabled).toBe("");
  c.edit("intervalNanos", "300000000000");
  c.edit("retain", "0");
  await c.review();
  expect(f.prepares()).toBe(0);
  c.edit("enabled", "false");
  await c.review();
  expect(f.prepares()).toBe(1);
});
