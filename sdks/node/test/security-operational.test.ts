import { expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { SecurityClient } from "../src/security.js";
import { encodeCurrentSecurityReview, parseCurrentSecurityReview } from "../src/security-types.js";
import {
  operationalGroupOrder,
  validateOperationalChange,
  validateOperationalSettings,
  validateOperationalPreview,
} from "../src/security-operational.js";
import {
  LanternSecurityService,
  CurrentSecurityReviewSchema,
  OperationalPolicyChangeSchema,
  OperationalPolicyValuesSchema,
  GetOperationalSettingsResponseSchema,
  LogPolicyAdoptionState,
  OperationalGroup,
  OperationalSettingsApplicationState,
  OperationalSettingsChangeMode,
  OperationalSettingsError,
  OperationalSettingsSource,
} from "../src/gen/graph/v1/security_pb.js";
const bytes = (size = 32, value = 1) => new Uint8Array(size).fill(value);
function settings() {
  const profile = {
    version: 2,
    domain: bytes(),
    cohort: bytes(32, 2),
    generation: bytes(16, 3),
    protocol: bytes(32, 4),
    timeProfile: bytes(32, 5),
    membership: bytes(32, 6),
    configuration: bytes(32, 7),
  };
  const cut = {
    version: 1,
    domain: profile.domain,
    cohort: profile.cohort,
    generation: profile.generation,
    sequence: 4n,
    previous: bytes(),
    projection: bytes(),
    frontier: bytes(),
    fences: bytes(),
    policy: bytes(),
  };
  return create(GetOperationalSettingsResponseSchema, {
    schemaVersion: 1,
    member: 1,
    version: { currentProfile: profile, currentCut: cut, admissionBinding: bytes() },
    groups: operationalGroupOrder.map((group) => ({
      group,
      adoption: LogPolicyAdoptionState.UNADOPTED,
      changeMode: OperationalSettingsChangeMode.UNAVAILABLE,
      application: OperationalSettingsApplicationState.UNAVAILABLE,
      error: OperationalSettingsError.OWNER_UNAVAILABLE,
    })),
  });
}
function pending() {
  const response = settings(),
    state = response.groups[0]!,
    profile = response.version!.currentProfile!;
  state.preparation = {
    $typeName: "graph.v1.LogPolicyPreparationReference",
    changeId: {
      $typeName: "graph.v1.CurrentSecurityChangeID",
      version: 1,
      domain: profile.domain,
      cohort: profile.cohort,
      namespace: 9007199254740999n,
      nonce: bytes(16),
    },
    intentDigest: bytes(),
    actorDigest: bytes(),
  };
  state.adoption = LogPolicyAdoptionState.MANAGED;
  state.changeMode = OperationalSettingsChangeMode.PROCESS_RESTART;
  state.application = OperationalSettingsApplicationState.PENDING;
  state.error = OperationalSettingsError.NONE;
  state.restartRequired = true;
  state.effective = create(OperationalPolicyValuesSchema, {
    group: { case: "slowRpc", value: { thresholdMs: 500n } },
  });
  state.desired = {
    $typeName: "graph.v1.ManagedOperationalPolicy",
    version: 1,
    generation: 2n,
    values: create(OperationalPolicyValuesSchema, {
      group: { case: "slowRpc", value: { thresholdMs: 1000n } },
    }),
    compatibilityFingerprint: "",
  };
  state.provenance = [
    {
      $typeName: "graph.v1.OperationalSettingProvenance",
      setting: 1,
      source: OperationalSettingsSource.ENVIRONMENT,
    },
  ];
  return response;
}
test("typed operational client keeps unavailable groups and restart-pending evidence distinct", async () => {
  let response = pending();
  const client = SecurityClient.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternSecurityService, { getOperationalSettings: () => response }),
    ),
  );
  expect((await client.getOperationalSettings()).groups[0]!.restartRequired).toBe(true);
  expect((await client.getOperationalSettings()).groups[1]!.effective).toBeUndefined();
  response = settings();
  expect((await client.getOperationalSettings()).groups).toHaveLength(8);
});
test("operational settings reject inconsistent application, foreign cut, missing groups and fabricated defaults", () => {
  const edits = [
    (r: ReturnType<typeof pending>) => r.groups.pop(),
    (r: ReturnType<typeof pending>) => {
      r.groups[1]!.group = OperationalGroup.SLOW_RPC;
    },
    (r: ReturnType<typeof pending>) => {
      r.groups[0]!.appliedGeneration = 3n;
    },
    (r: ReturnType<typeof pending>) => {
      r.groups[0]!.application = OperationalSettingsApplicationState.APPLIED;
    },
    (r: ReturnType<typeof pending>) => {
      r.groups[1]!.effective = r.groups[0]!.effective;
    },
    (r: ReturnType<typeof pending>) => {
      r.groups[0]!.provenance[0]!.source = OperationalSettingsSource.MANAGED;
    },
    (r: ReturnType<typeof pending>) => {
      r.groups[0]!.preparation = undefined;
    },
  ];
  for (const edit of edits) {
    const r = pending();
    edit(r);
    expect(() => validateOperationalSettings(r)).toThrow();
  }
  const r = pending(),
    state = r.groups[0]!;
  state.appliedGeneration = 1n;
  state.appliedCut = structuredClone(r.version!.currentCut);
  state.appliedCut!.generation[0] = 99;
  state.provenance[0]!.source = OperationalSettingsSource.MANAGED;
  expect(() => validateOperationalSettings(r)).toThrow();
});
test("recovery rejects empty nested unions, future versions, mixed commands and non-Search fingerprints", () => {
  const valid = create(OperationalPolicyChangeSchema, {
    version: 1,
    expectedGeneration: 9007199254740999n,
    values: { group: { case: "slowRpc", value: { thresholdMs: 500n } } },
  });
  const review = create(CurrentSecurityReviewSchema, {
    changes: [{ operation: { case: "putOperationalPolicy", value: valid } }],
  });
  const encoded = encodeCurrentSecurityReview(review);
  expect(parseCurrentSecurityReview(encoded).changes).toEqual(review.changes);
  expect(encoded).toContain("9007199254740999");
  for (const edit of [
    (c: typeof valid) => {
      c.values = create(OperationalPolicyValuesSchema);
    },
    (c: typeof valid) => {
      c.version = 2;
    },
    (c: typeof valid) => {
      c.compatibilityFingerprint = "a".repeat(64);
    },
    (c: typeof valid) => {
      c.retentionReductionAcknowledged = true;
    },
  ]) {
    const changed = structuredClone(valid);
    edit(changed);
    expect(() => validateOperationalChange(changed)).toThrow();
  }
  review.changes.push({
    $typeName: "graph.v1.SecurityChange",
    operation: { case: "deleteRole", value: "reader" },
  });
  expect(() => encodeCurrentSecurityReview(review)).toThrow();
  expect(() => parseCurrentSecurityReview(encoded.replace('"version":1', '"version":2'))).toThrow();
});
test("rate pair and exact duration bounds are validated without integer rounding", () => {
  const rate = create(OperationalPolicyChangeSchema, {
    version: 1,
    values: { group: { case: "rate", value: { rps: 0.25, burst: 1n } } },
  });
  expect(() => validateOperationalChange(rate)).not.toThrow();
  rate.values!.group = {
    case: "rate",
    value: { $typeName: "graph.v1.OperationalRateValues", rps: 0, burst: 1n },
  };
  expect(() => validateOperationalChange(rate)).toThrow();
  const backup = create(OperationalPolicyChangeSchema, {
    version: 1,
    values: { group: { case: "backup", value: { intervalNanos: 9007199254740999n, retain: 0n } } },
  });
  expect(() => validateOperationalChange(backup)).not.toThrow();
  backup.expectedGeneration = 0xffffffffffffffffn;
  expect(() => validateOperationalChange(backup)).toThrow();
});

test("backup preview is a bounded connected-node sample, not a frozen deletion catalog", () => {
  const values = create(OperationalPolicyValuesSchema, {
    group: { case: "backup", value: { intervalNanos: 300n, retain: 2n } },
  });
  const preview = {
    $typeName: "graph.v1.PreviewOperationalPolicyResponse" as const,
    schemaVersion: 1,
    group: OperationalGroup.BACKUP,
    compatibilityFingerprint: "",
    version: settings().version,
    backupRetention: {
      $typeName: "graph.v1.OperationalBackupRetentionPreview" as const,
      validBackups: 9007199254740999n,
      validBytes: 1000n,
      pruneBackups: 1n,
      pruneBytes: 100n,
      complete: true,
      fingerprint: "a".repeat(64),
    },
  };
  expect(() => validateOperationalPreview(preview, values)).not.toThrow();
  preview.backupRetention.pruneBytes = 1001n;
  expect(() => validateOperationalPreview(preview, values)).toThrow();
  preview.backupRetention.pruneBytes = 100n;
  preview.backupRetention.complete = false;
  preview.backupRetention.fingerprint = "";
  expect(() => validateOperationalPreview(preview, values)).not.toThrow();
});

test("invalid bootstrap retains its owner and bounded sources without encoding nonfinite effective values", () => {
  const r = settings(),
    rate = r.groups[1]!;
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
  expect(() => validateOperationalSettings(r)).not.toThrow();
  rate.restartRequired = true;
  expect(() => validateOperationalSettings(r)).toThrow();
});
test("same-sequence applied cuts require every current-cut digest to match", () => {
  const r = pending(),
    state = r.groups[0]!;
  state.effective = state.desired!.values;
  state.appliedGeneration = state.desired!.generation;
  state.appliedCut = structuredClone(r.version!.currentCut);
  state.provenance[0]!.source = OperationalSettingsSource.MANAGED;
  state.application = OperationalSettingsApplicationState.APPLIED;
  state.restartRequired = false;
  expect(() => validateOperationalSettings(r)).not.toThrow();
  state.appliedCut!.projection[0]++;
  expect(() => validateOperationalSettings(r)).toThrow();
});

test("managed Search capability fields cannot wrap uint32 at the public boundary", () => {
  const search = create(OperationalPolicyValuesSchema, {
    group: {
      case: "search",
      value: {
        defaultLimit: 100n,
        maxLimit: 1000n,
        defaultMode: 1,
        defaultMinShould: 1n,
        timeoutMs: 0xffffffffn,
        maxQueryBytes: 0xffffffffn,
        maxQueryTerms: 0xffffffffn,
        maxDictionaryVisits: 100n,
        maxPostingVisits: 100n,
        maxPositionVisits: 100n,
        maxExpirationVisits: 100n,
      },
    },
  });
  const change = create(OperationalPolicyChangeSchema, {
    version: 1,
    values: search,
    compatibilityFingerprint: "a".repeat(64),
  });
  expect(() => validateOperationalChange(change)).not.toThrow();
  for (const field of ["timeoutMs", "maxQueryBytes", "maxQueryTerms"] as const) {
    const candidate = structuredClone(change);
    if (candidate.values?.group.case !== "search") throw new Error();
    candidate.values.group.value[field] = 0x100000000n;
    expect(() => validateOperationalChange(candidate)).toThrow();
  }
});
