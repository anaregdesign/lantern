import { describe, expect, test } from "bun:test";
import {
  ManagedLogLevel,
  LogSettingsSource,
  LogPolicyAdoptionState,
  LogSettingsApplicationState,
  LogSettingsError,
  SecurityAuthorizationRequirement,
  type GetLogSettingsResponse,
  type CurrentSecurityReview,
} from "lantern-sdk/web";
import {
  version,
  cut,
  review,
  prepare,
  result,
  acknowledgement,
  bytes,
} from "../../../../../test/current-security";
import {
  SecurityChangeRecovery,
  securityRecoveryOwner,
  logPolicyRecoveryOwner,
} from "../security/security-change-recovery";
import {
  SettingsController,
  settingsView,
  type LogSettingsPort,
} from "./settings-management";

const owner = logPolicyRecoveryOwner("https://admin.example", {
  identity: review().actor,
  version,
});
function recovery() {
  let raw: string | null = null;
  return new SecurityChangeRecovery({
    read: () => raw,
    replace: (value) => {
      raw = value;
    },
  });
}
function settings(
  patch: Partial<GetLogSettingsResponse> = {},
): GetLogSettingsResponse {
  return {
    $typeName: "graph.v1.GetLogSettingsResponse",
    schemaVersion: 1,
    member: 1,
    effectiveLevel: ManagedLogLevel.INFO,
    source: LogSettingsSource.ENVIRONMENT,
    desiredGeneration: 0n,
    appliedGeneration: 0n,
    adoption: LogPolicyAdoptionState.UNADOPTED,
    application: LogSettingsApplicationState.APPLIED,
    error: LogSettingsError.NONE,
    restartRequired: false,
    version,
    ...patch,
  };
}
function fixture(
  overrides: Partial<LogSettingsPort> = {},
  store = recovery(),
  canMutate = true,
) {
  let sends = 0;
  let reads = 0;
  let latest: CurrentSecurityReview | undefined;
  const port: LogSettingsPort = {
    settings: async () => {
      reads++;
      return settings();
    },
    prepare: async (draft) => {
      latest = prepare(draft).currentReview;
      return prepare(draft, SecurityAuthorizationRequirement.ORDINARY);
    },
    beginAuthorization: async () => {
      throw new Error("unexpected authorization");
    },
    authorization: async () => {
      throw new Error("unexpected authorization");
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
    controller: new SettingsController(
      port,
      new AbortController().signal,
      store,
      owner,
      canMutate,
    ),
    sends: () => sends,
    reads: () => reads,
    latest: () => latest,
  };
}

describe("managed log settings", () => {
  test("explicit adoption commits one typed value and separately reports pending local application", async () => {
    let reads = 0;
    const f = fixture({
      settings: async () =>
        ++reads === 1
          ? settings()
          : settings({
              desired: {
                $typeName: "graph.v1.ManagedLogPolicy",
                version: 1,
                level: ManagedLogLevel.DEBUG,
              },
              desiredGeneration: 1n,
              adoption: LogPolicyAdoptionState.MANAGED,
              application: LogSettingsApplicationState.PENDING,
            }),
    });
    const c = f.controller;
    await c.load();
    expect(settingsView(c.getSnapshot(), true).source).toBe("Environment");
    expect(settingsView(c.getSnapshot(), true).application).toBe(
      "Startup setting active",
    );
    expect(settingsView(c.getSnapshot(), true).canReview).toBe(false);
    c.editLevel(ManagedLogLevel.DEBUG);
    await c.review();
    expect(settingsView(c.getSnapshot(), true).changeId).toBe("");
    expect(f.latest()?.changes).toEqual([
      {
        $typeName: "graph.v1.SecurityChange",
        operation: {
          case: "putLogPolicy",
          value: {
            $typeName: "graph.v1.ManagedLogPolicy",
            version: 1,
            level: ManagedLogLevel.DEBUG,
          },
        },
      },
    ]);
    await c.apply();
    expect(f.sends()).toBe(1);
    expect(c.getSnapshot().message).toContain("policy was committed");
    expect(c.getSnapshot().settings?.appliedGeneration).toBe(0n);
    expect(c.getSnapshot().stale).toBe(true);
    await c.load();
    const view = settingsView(c.getSnapshot(), true);
    expect(view.application).toBe("Pending on this node");
    expect(view.effective).toBe("Info");
    expect(view.desired).toBe("Debug");
    expect(view.generation).toBe("0 / 1");
  });

  test("application and fenced-but-unadopted states do not imply adoption or a source fallback", async () => {
    const f = fixture({
      settings: async () =>
        settings({
          adoption: LogPolicyAdoptionState.FENCED_UNADOPTED,
          source: LogSettingsSource.DEFAULT,
          preparation: {
            $typeName: "graph.v1.LogPolicyPreparationReference",
            changeId: review(23).changeId,
            intentDigest: bytes(32, 24),
            actorDigest: bytes(32, 25),
          },
        }),
    });
    await f.controller.load();
    expect(settingsView(f.controller.getSnapshot(), true)).toMatchObject({
      fenced: true,
      firstAdoption: true,
      source: "Default",
      desired: "Not adopted",
      generation: "0 / 0",
      application: "Startup setting active",
      preparationId: "19:" + "17".repeat(16),
    });
    const applied = fixture({
      settings: async () =>
        settings({
          source: LogSettingsSource.MANAGED,
          effectiveLevel: ManagedLogLevel.WARN,
          desired: {
            $typeName: "graph.v1.ManagedLogPolicy",
            version: 1,
            level: ManagedLogLevel.WARN,
          },
          desiredGeneration: 3n,
          appliedGeneration: 3n,
          appliedCut: cut,
          adoption: LogPolicyAdoptionState.MANAGED,
        }),
    });
    await applied.controller.load();
    expect(settingsView(applied.controller.getSnapshot(), true)).toMatchObject({
      application: "Applied on this node",
      source: "Managed policy",
      firstAdoption: false,
      generation: "3 / 3",
    });
  });

  test("authority loss retains an explicitly stale verified value and blocks mutation", async () => {
    let available = true;
    const f = fixture({
      settings: async () => {
        if (!available) throw new Error("unavailable");
        return settings({
          source: LogSettingsSource.MANAGED,
          effectiveLevel: ManagedLogLevel.ERROR,
          desired: {
            $typeName: "graph.v1.ManagedLogPolicy",
            version: 1,
            level: ManagedLogLevel.ERROR,
          },
          desiredGeneration: 4n,
          appliedGeneration: 4n,
          adoption: LogPolicyAdoptionState.MANAGED,
          appliedCut: cut,
        });
      },
    });
    await f.controller.load();
    available = false;
    await f.controller.load();
    expect(f.controller.getSnapshot().phase).toBe("error");
    expect(f.controller.getSnapshot().stale).toBe(true);
    expect(settingsView(f.controller.getSnapshot(), true)).toMatchObject({
      source: "Managed policy",
      effective: "Error",
      blocked: true,
    });
    f.controller.editLevel(ManagedLogLevel.DEBUG);
    await f.controller.review();
    await f.controller.apply();
    expect(f.sends()).toBe(0);
  });

  test("an unknown Apply response is recovered under the same owner without a second dispatch", async () => {
    const store = recovery();
    let sends = 0;
    let original: CurrentSecurityReview | undefined;
    const f = fixture(
      {
        apply: async (request) => {
          sends++;
          original = request.currentReview;
          throw new Error("response lost");
        },
      },
      store,
    );
    await f.controller.load();
    f.controller.editLevel(ManagedLogLevel.DEBUG);
    await f.controller.review();
    await f.controller.apply();
    expect(f.controller.getSnapshot().mutation).toBe("unconfirmed");
    const beforeReads = f.reads();
    await f.controller.load();
    expect(f.reads()).toBe(beforeReads + 1);
    expect(f.controller.getSnapshot().mutation).toBe("unconfirmed");
    f.controller.dispose();
    const resumed = fixture(
      {
        status: async (r) => {
          expect(r).toEqual(original!);
          return result(r);
        },
      },
      store,
    );
    expect(resumed.controller.getSnapshot().originalId).not.toBe("");
    await resumed.controller.review();
    expect(sends).toBe(1);
    await resumed.controller.checkStatus();
    expect(resumed.controller.getSnapshot().mutation).toBe("applied");
    expect(resumed.controller.getSnapshot().version).toBeUndefined();
    await resumed.controller.load();
    expect(resumed.controller.getSnapshot().phase).toBe("ready");
    expect(sends).toBe(1);
  });

  test("read-only observations expose a partial floor without settling an unknown original", async () => {
    let current = settings();
    let prepares = 0;
    let sends = 0;
    const f = fixture({
      settings: async () => current,
      prepare: async (draft) => {
        prepares++;
        return prepare(draft);
      },
      apply: async () => {
        sends++;
        throw new Error("Original H durable; application response unknown.");
      },
    });
    await f.controller.load();
    f.controller.editLevel(ManagedLogLevel.WARN);
    await f.controller.review();
    await f.controller.apply();
    const originalId = f.controller.getSnapshot().originalId;
    current = settings({
      adoption: LogPolicyAdoptionState.FENCED_UNADOPTED,
      preparation: {
        $typeName: "graph.v1.LogPolicyPreparationReference",
        changeId: review().changeId,
        intentDigest: bytes(32, 24),
        actorDigest: bytes(32, 25),
      },
    });
    await f.controller.load();
    expect(settingsView(f.controller.getSnapshot(), true)).toMatchObject({
      fenced: true,
      blocked: true,
      application: "Startup setting active",
    });
    expect(f.controller.getSnapshot().mutation).toBe("unconfirmed");
    expect(f.controller.getSnapshot().originalId).toBe(originalId);
    // A matching current value may have another cause. Only the original's
    // retained authenticated result can settle its invocation.
    current = settings({
      effectiveLevel: ManagedLogLevel.WARN,
      source: LogSettingsSource.MANAGED,
      desired: {
        $typeName: "graph.v1.ManagedLogPolicy",
        version: 1,
        level: ManagedLogLevel.WARN,
      },
      desiredGeneration: 1n,
      appliedGeneration: 1n,
      appliedCut: cut,
      adoption: LogPolicyAdoptionState.MANAGED,
    });
    await f.controller.load();
    expect(settingsView(f.controller.getSnapshot(), true)).toMatchObject({
      effective: "Warn",
      application: "Applied on this node",
      blocked: true,
    });
    await f.controller.review();
    await f.controller.apply();
    expect(prepares).toBe(1);
    expect(sends).toBe(1);
    expect(f.controller.getSnapshot().mutation).toBe("unconfirmed");
    expect(f.controller.getSnapshot().originalId).toBe(originalId);
  });

  test("a pending security original blocks log Apply before dispatch", async () => {
    const store = recovery();
    const original = review();
    const securityOwner = securityRecoveryOwner("https://admin.example", {
      identity: original.actor,
      version,
    });
    store.stage(securityOwner, original);
    const f = fixture({}, store);
    await f.controller.load();
    f.controller.editLevel(ManagedLogLevel.DEBUG);
    await f.controller.review();
    await f.controller.apply();
    expect(f.latest()?.changes[0]?.operation.case).toBe("putLogPolicy");
    expect(f.sends()).toBe(0);
    expect(f.controller.getSnapshot().message).toContain(
      "Check the retained security original status",
    );
  });

  test("read-only credentials can inspect settings but cannot prepare or apply", async () => {
    const f = fixture({}, recovery(), false);
    await f.controller.load();
    expect(f.reads()).toBe(1);
    f.controller.editLevel(ManagedLogLevel.DEBUG);
    await f.controller.review();
    await f.controller.apply();
    expect(f.latest()).toBeUndefined();
    expect(f.sends()).toBe(0);
  });

  test("a superseded settings response cannot replace the newer observation", async () => {
    let release: (value: GetLogSettingsResponse) => void = () => {};
    let calls = 0;
    const delayed = new Promise<GetLogSettingsResponse>((resolve) => {
      release = resolve;
    });
    const f = fixture({
      settings: async () =>
        ++calls === 1
          ? delayed
          : settings({ effectiveLevel: ManagedLogLevel.WARN }),
    });
    const older = f.controller.load();
    await f.controller.load();
    release(settings());
    await older;
    expect(settingsView(f.controller.getSnapshot(), true).effective).toBe(
      "Warn",
    );
  });
});
