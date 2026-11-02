import { describe, expect, test } from "bun:test";
import {
  currentOriginalBinding,
  currentProfileBinding,
  CurrentSecurityDisposition,
  ManagedLogLevel,
  type SecurityChange,
  type CurrentSecurityReview,
} from "lantern-sdk/web";
import {
  acknowledgement,
  changes,
  prepare,
  profile,
  result,
  review as makeReview,
  version,
} from "../../../../../test/current-security";
import {
  CurrentChangeController,
  SecurityChangeRecovery,
  type CurrentChangePort,
} from "./current-change";

const owner = JSON.stringify([
  "https://api.example",
  "https://idp.example",
  "alice",
  currentProfileBinding(profile),
]);

function fixture(
  recovery?: SecurityChangeRecovery,
  describeResult?: () => string,
  nonceSeed = 10,
  recoveryOwner = owner,
) {
  const scope = new AbortController();
  let invocations = 0;
  let nonce = nonceSeed;
  const callbacks = {
    apply: async (review: CurrentSecurityReview) =>
      acknowledgement(result(review)),
    status: async (review: CurrentSecurityReview) => result(review),
  };
  const unavailable = async (): Promise<never> => {
    throw new Error("Not used by ordinary changes.");
  };
  const port: CurrentChangePort = {
    prepare: async (review) => prepare(review, undefined, nonce++),
    beginAuthorization: unavailable,
    authorization: unavailable,
    openAuthorization: () => ({ navigate() {}, close() {} }),
    invocationRejected: () => undefined,
    apply: async ({ currentReview }) => {
      invocations++;
      return callbacks.apply(currentReview);
    },
    status: (review) => callbacks.status(review),
    failure: () => "unavailable",
  };
  const controller = new CurrentChangeController(
    port,
    scope.signal,
    recovery,
    recoveryOwner,
    describeResult,
  );
  controller.publish({ phase: "ready", version });
  return { controller, port, callbacks, scope, invocations: () => invocations };
}

function memoryRecovery() {
  let raw: string | null = null;
  return new SecurityChangeRecovery({
    read: () => raw,
    replace: (value) => {
      raw = value;
    },
  });
}

describe("shared current change workflow", () => {
  test("a non-editor caller can review and commit with feature-specific result wording", async () => {
    const f = fixture(
      memoryRecovery(),
      () => "Policy committed; check this node's application status.",
    );
    await f.controller.review("Change log level", changes);
    expect(f.controller.getSnapshot().review?.approval).toBe("ordinary");
    await f.controller.apply();
    expect(f.invocations()).toBe(1);
    expect(f.controller.getSnapshot().mutation).toBe("applied");
    expect(f.controller.getSnapshot().version).toBeUndefined();
    expect(f.controller.getSnapshot().message).toBe(
      "Policy committed; check this node's application status.",
    );
  });

  test("an interrupted review cannot authorize an Apply", async () => {
    const f = fixture(memoryRecovery());
    let release!: (review: CurrentSecurityReview) => void;
    f.port.prepare = (review) =>
      new Promise((resolve) => {
        release = (value) => resolve(prepare({ ...review, ...value }));
      });
    const reviewing = f.controller.review("Change log level", changes);
    const inspected = f.controller.getSnapshot().review!;
    f.controller.operation();
    release({
      $typeName: "graph.v1.CurrentSecurityReview",
      profile: inspected.version.currentProfile,
      expectedCut: inspected.version.currentCut,
      intentDigest: new Uint8Array(),
      changes: inspected.changes,
    });
    await reviewing;
    await f.controller.apply();
    expect(f.invocations()).toBe(0);
    expect(f.controller.getSnapshot().review?.approval).toBe("failed");
  });

  test("two security controllers share one original-recovery lane", async () => {
    const recovery = memoryRecovery();
    const first = fixture(recovery);
    const second = fixture(recovery, undefined, 20);
    first.callbacks.apply = async () => {
      throw new Error("Response lost.");
    };
    await first.controller.review("First feature", changes);
    await second.controller.review("Second feature", changes);
    const original = first.controller.getSnapshot().review!.contract!;
    await first.controller.apply();
    await second.controller.apply();
    expect(first.invocations()).toBe(1);
    expect(second.invocations()).toBe(0);
    expect(second.controller.getSnapshot().message).toContain(
      "retained original status",
    );
    expect(currentOriginalBinding(recovery.read(owner)!.review)).toBe(
      currentOriginalBinding(original),
    );
  });

  test("an unresolved logging original preserves urgent security changes and resolves under its original ID", async () => {
    const recovery = memoryRecovery();
    const logOwner = JSON.stringify([...JSON.parse(owner), "log-policy"]);
    const logging = fixture(recovery, undefined, 40, logOwner);
    const logChanges: SecurityChange[] = [
      {
        $typeName: "graph.v1.SecurityChange",
        operation: {
          case: "putLogPolicy",
          value: {
            $typeName: "graph.v1.ManagedLogPolicy",
            version: 1,
            level: ManagedLogLevel.WARN,
          },
        },
      },
    ];
    logging.callbacks.apply = async () => {
      throw new Error("Original H retained; enrolled origin offline.");
    };
    await logging.controller.review("Adopt logging policy", logChanges);
    const original = logging.controller.getSnapshot().review!.contract!;
    await logging.controller.apply();
    expect(logging.controller.getSnapshot().mutation).toBe("unconfirmed");

    const urgent = fixture(recovery, undefined, 60);
    await urgent.controller.review("Revoke another user's sessions", [
      {
        $typeName: "graph.v1.SecurityChange",
        operation: {
          case: "revokeUserSessions",
          value: { ...original.actor!, subject: "revoked-user" },
        },
      },
    ]);
    const securityOriginal = urgent.controller.getSnapshot().review!.contract!;
    await urgent.controller.apply();
    expect(urgent.invocations()).toBe(1);
    expect(urgent.controller.getSnapshot().mutation).toBe("applied");
    expect(currentOriginalBinding(recovery.read(logOwner)!.review)).toBe(
      currentOriginalBinding(original),
    );
    expect(currentOriginalBinding(recovery.read(owner)!.review)).toBe(
      currentOriginalBinding(securityOriginal),
    );

    logging.callbacks.status = async (review) => {
      const rejected = result(
        review,
        undefined,
        CurrentSecurityDisposition.REJECTED_CAS,
      );
      rejected.original!.items[0].kind = "log_policy.put";
      return rejected;
    };
    await logging.controller.checkStatus();
    expect(logging.controller.getSnapshot().mutation).toBe("rejected");
    expect(logging.invocations()).toBe(1);
    expect(currentOriginalBinding(recovery.read(logOwner)!.review)).toBe(
      currentOriginalBinding(original),
    );
    expect(recovery.read(logOwner)!.needsOriginalStatus).toBe(false);
    expect(recovery.read(owner)!.result?.original).toBeDefined();
  });

  test("scope loss retains an unknown original for status-only recovery", async () => {
    const recovery = memoryRecovery();
    const first = fixture(recovery);
    let release!: () => void;
    first.callbacks.apply = (review) =>
      new Promise((resolve) => {
        release = () => resolve(acknowledgement(result(review)));
      });
    await first.controller.review("Change level", changes);
    const original = first.controller.getSnapshot().review!.contract!;
    const applying = first.controller.apply();
    first.scope.abort();
    first.controller.dispose();
    release();
    await applying;
    const restored = fixture(recovery);
    expect(restored.controller.getSnapshot().mutation).toBe("unconfirmed");
    await restored.controller.review("Different operation", changes);
    await restored.controller.apply();
    expect(restored.invocations()).toBe(0);
    expect(currentOriginalBinding(recovery.read(owner)!.review)).toBe(
      currentOriginalBinding(original),
    );
    await restored.controller.checkStatus();
    expect(restored.controller.getSnapshot().mutation).toBe("applied");
  });

  test("logging cannot dispatch when its matching security recovery row cannot be reserved", async () => {
    const recovery = memoryRecovery();
    let firstOwner = "";
    for (let i = 0; i < 19; i++) {
      const original = makeReview(100 + i);
      const fields: string[] = JSON.parse(owner);
      fields[0] += `/deployment-${i}`;
      const existingOwner = JSON.stringify(fields);
      if (i === 0) firstOwner = existingOwner;
      recovery.stage(existingOwner, original);
    }
    const logOwner = JSON.stringify([...JSON.parse(owner), "log-policy"]);
    const logging = fixture(recovery, undefined, 220, logOwner);
    await logging.controller.review("Adopt log policy", [
      {
        $typeName: "graph.v1.SecurityChange",
        operation: {
          case: "putLogPolicy",
          value: {
            $typeName: "graph.v1.ManagedLogPolicy",
            version: 1,
            level: ManagedLogLevel.WARN,
          },
        },
      },
    ]);
    await logging.controller.apply();
    expect(logging.invocations()).toBe(0);
    expect(logging.controller.getSnapshot().message).toContain(
      "history is full",
    );
    expect(recovery.read(logOwner)).toBeUndefined();
    expect(recovery.read(firstOwner)?.review).toEqual(makeReview(100));
  });
});
