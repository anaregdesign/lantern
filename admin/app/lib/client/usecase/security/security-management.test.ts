import { describe, expect, test } from "bun:test";
import {
  SecurityEnforcementState,
  SecurityAuthorizationRequirement,
  SecurityAuthorizationState,
  type SecurityChange,
  type SecurityVersion,
} from "lantern-sdk/web";
import {
  SecurityManagementController,
  SecurityChangeRecovery,
  type SecurityManagementPort,
} from "./security-management";

const version: SecurityVersion = {
  $typeName: "graph.v1.SecurityVersion",
  revision: 3n,
  digest: new Uint8Array(32).fill(1),
  generation: new Uint8Array(16).fill(2),
};
const changes: SecurityChange[] = [
  {
    $typeName: "graph.v1.SecurityChange",
    operation: { case: "deleteRole", value: "reader" },
  },
];
const changeId = new Uint8Array(16).fill(7);
const result = {
  $typeName: "graph.v1.ApplySecurityChangesResponse" as const,
  version: { ...version, revision: 4n },
  applied: [true],
  replayed: false,
  enforcement: SecurityEnforcementState.COMMITTED_PENDING,
};
const proof = {
  $typeName: "graph.v1.GetSecurityChangeStatusResponse" as const,
  version: result.version,
  changeId,
  enforcement: SecurityEnforcementState.COMMITTED_PENDING,
};
function fixture(
  overrides: Partial<SecurityManagementPort> = {},
  scope = new AbortController(),
  recovery?: SecurityChangeRecovery,
  owner = "browser-session",
) {
  const port: SecurityManagementPort = {
    roles: async () => ({
      $typeName: "graph.v1.ListRolesResponse",
      roles: [],
      version,
      nextCursor: "",
    }),
    issuers: async () => ({
      $typeName: "graph.v1.ListIssuersResponse",
      issuers: [],
      version,
      nextCursor: "",
    }),
    users: async () => ({
      $typeName: "graph.v1.ListUsersResponse",
      users: [],
      version,
      nextCursor: "",
    }),
    templates: async () => ({
      $typeName: "graph.v1.GetRoleTemplatesResponse",
      roles: [],
      version,
      nextCursor: "",
    }),
    audit: async () => ({ records: [], version, nextCursor: "" }),
    validateIssuer: async () => true,
    explain: async () => ({
      $typeName: "graph.v1.ExplainAccessResponse",
      allowed: false,
      matches: [],
      version,
    }),
    prepare: async (review) => ({
      $typeName: "graph.v1.PrepareSecurityChangesResponse",
      expectedVersion: review.expectedVersion,
      changeId: review.changeId,
      intentDigest: new Uint8Array(32).fill(4),
      requirement: SecurityAuthorizationRequirement.ORDINARY,
    }),
    beginAuthorization: async () => ({
      $typeName: "graph.v1.BeginSecurityChangeAuthorizationResponse",
      authorizationId: new Uint8Array(32).fill(8),
      startUrl:
        "https://admin.example/auth/management-authorization/" + "A".repeat(43),
      expiresAt: {
        $typeName: "google.protobuf.Timestamp",
        seconds: 3000000000n,
        nanos: 0,
      },
    }),
    authorization: async (id) => ({
      $typeName: "graph.v1.GetSecurityChangeAuthorizationResponse",
      authorizationId: id,
      state: SecurityAuthorizationState.APPROVED,
      authorizationProof: new Uint8Array(32).fill(9),
      expiresAt: {
        $typeName: "google.protobuf.Timestamp",
        seconds: 3000000000n,
        nanos: 0,
      },
    }),
    openAuthorization: () => ({ navigate() {}, close() {} }),
    authorizationRequired: () => undefined,
    apply: async () => result,
    status: async () => proof,
    newChangeId: () => new Uint8Array(16).fill(7),
    failure: () => "unavailable",
    ...overrides,
  };
  return new SecurityManagementController(
    port,
    "roles",
    scope.signal,
    recovery,
    owner,
  );
}
describe("reviewed security changes", () => {
  for (const [read, supersede] of [
    [
      "audit",
      (controller: SecurityManagementController) => controller.loadAudit(),
    ],
    [
      "templates",
      (controller: SecurityManagementController) =>
        controller.loadTemplates("tenant:"),
    ],
    [
      "members",
      (controller: SecurityManagementController) =>
        controller.loadRoleMembers("reader"),
    ],
  ] as const) {
    for (const outcome of ["resolve", "reject"] as const) {
      test(`${read} supersession retires repeated delayed Prepare ${outcome} and requires fresh review`, async () => {
        const settlements: (() => void)[] = [];
        const signals: AbortSignal[] = [];
        let prepares = 0,
          ids = 0,
          applied = 0;
        const controller = fixture({
          newChangeId: () => new Uint8Array(16).fill(7 + ids++),
          prepare: (review, signal) => {
            const response = {
              $typeName: "graph.v1.PrepareSecurityChangesResponse" as const,
              expectedVersion: review.expectedVersion,
              changeId: review.changeId,
              intentDigest: new Uint8Array(32).fill(4),
              requirement: SecurityAuthorizationRequirement.ORDINARY,
            };
            if (++prepares > 2) return Promise.resolve(response);
            signals.push(signal);
            // Settling after cancellation exercises an already in-flight response.
            return new Promise((resolve, reject) => {
              settlements.push(() =>
                outcome === "resolve"
                  ? resolve(response)
                  : reject(new Error("interrupted")),
              );
            });
          },
          apply: async () => {
            applied++;
            return result;
          },
        });
        await controller.load();
        let originalID: Uint8Array | undefined;
        for (let attempt = 0; attempt < 2; attempt++) {
          const preparing = controller.review("Original", changes);
          expect(controller.getSnapshot().review?.approval).toBe("preparing");
          const original = controller.getSnapshot().review!;
          originalID = original.changeId;
          await supersede(controller);
          expect(signals[attempt].aborted).toBe(true);
          expect(controller.getSnapshot().review?.approval).toBe("failed");
          expect(controller.getSnapshot().review?.changeId).toEqual(
            original.changeId,
          );
          expect(controller.getSnapshot().review?.changes).toEqual(
            original.changes,
          );
          expect(controller.getSnapshot().review?.version).toEqual(
            original.version,
          );
          settlements[attempt]();
          await preparing;
          expect(controller.getSnapshot().review?.approval).toBe("failed");
          await controller.apply();
          expect(applied).toBe(0);
        }
        await controller.review("Reviewed again", changes);
        expect(controller.getSnapshot().review?.approval).toBe("ordinary");
        expect(controller.getSnapshot().review?.changeId).not.toEqual(
          originalID,
        );
        expect(applied).toBe(0);
        await controller.apply();
        expect(applied).toBe(1);
        controller.dispose();
      });
    }
  }
  for (const outcome of ["resolve", "reject"] as const) {
    test(`cancelling a delayed Prepare ignores its later ${outcome}`, async () => {
      let settle = () => {};
      let applied = 0;
      const controller = fixture({
        prepare: (review) =>
          new Promise((resolve, reject) => {
            settle = () =>
              outcome === "resolve"
                ? resolve({
                    $typeName: "graph.v1.PrepareSecurityChangesResponse",
                    expectedVersion: review.expectedVersion,
                    changeId: review.changeId,
                    intentDigest: new Uint8Array(32).fill(4),
                    requirement: SecurityAuthorizationRequirement.ORDINARY,
                  })
                : reject(new Error("cancelled"));
          }),
        apply: async () => {
          applied++;
          return result;
        },
      });
      await controller.load();
      const preparing = controller.review("Cancelled", changes);
      controller.cancelReview();
      settle();
      await preparing;
      expect(controller.getSnapshot().review).toBeUndefined();
      await controller.apply();
      expect(applied).toBe(0);
      controller.dispose();
    });
  }
  test("audit supersession rejects delayed Begin and closes its popup without stranding the review", async () => {
    let closed = 0,
      begins = 0;
    const controller = fixture({
      prepare: async (review) => ({
        $typeName: "graph.v1.PrepareSecurityChangesResponse",
        expectedVersion: review.expectedVersion,
        changeId: review.changeId,
        intentDigest: new Uint8Array(32).fill(4),
        requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
      }),
      beginAuthorization: (_review, signal) => {
        begins++;
        if (begins === 1)
          return new Promise((_resolve, reject) =>
            signal.addEventListener(
              "abort",
              () => reject(new Error("interrupted")),
              { once: true },
            ),
          );
        return Promise.resolve({
          $typeName: "graph.v1.BeginSecurityChangeAuthorizationResponse",
          authorizationId: new Uint8Array(32).fill(8),
          startUrl:
            "https://admin.example/auth/management-authorization/" +
            "A".repeat(43),
          expiresAt: {
            $typeName: "google.protobuf.Timestamp",
            seconds: 3000000000n,
            nanos: 0,
          },
        });
      },
      openAuthorization: () => ({
        navigate() {},
        close() {
          closed++;
        },
      }),
    });
    await controller.load();
    await controller.review("Original", changes);
    const starting = controller.authorize();
    await controller.loadAudit();
    await starting;
    expect(closed).toBe(1);
    expect(controller.getSnapshot().review?.approval).toBe("required");
    expect(controller.getSnapshot().review?.changeId).toEqual(changeId);
    await controller.authorize();
    expect(controller.getSnapshot().review?.approval).toBe("authenticating");
    expect(begins).toBe(2);
    controller.dispose();
  });
  test("expired first Apply reacquires approval for the same ID, while older ambiguity stays status-only", async () => {
    const refused = {
      $typeName: "graph.v1.SecurityOperationAuthorizationRequired" as const,
      changeId,
      expectedVersion: version,
      intentDigest: new Uint8Array(32).fill(4),
    };
    let sent = 0,
      ids = 0;
    const controller = fixture({
      prepare: async (review) => ({
        $typeName: "graph.v1.PrepareSecurityChangesResponse",
        expectedVersion: review.expectedVersion,
        changeId: review.changeId,
        intentDigest: refused.intentDigest,
        requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
      }),
      newChangeId: () => {
        ids++;
        return changeId;
      },
      authorizationRequired: (error) =>
        error === refused ? refused : undefined,
      apply: async () => {
        sent++;
        if (sent === 1) throw refused;
        return result;
      },
    });
    await controller.load();
    await controller.review("High impact", changes);
    await controller.authorize();
    await controller.checkAuthorization();
    await controller.apply();
    expect(controller.getSnapshot().mutation).toBe("idle");
    expect(controller.getSnapshot().review?.approval).toBe("required");
    expect(controller.getSnapshot().review?.changeId).toEqual(changeId);
    await controller.authorize();
    await controller.checkAuthorization();
    await controller.apply();
    expect(sent).toBe(2);
    expect(ids).toBe(1);
    controller.dispose();

    const recovery = new SecurityChangeRecovery();
    sent = 0;
    const ambiguous = fixture(
      {
        apply: async () => {
          sent++;
          throw new Error("response lost");
        },
        status: async () => {
          throw refused;
        },
        authorizationRequired: (error) =>
          error === refused ? refused : undefined,
      },
      new AbortController(),
      recovery,
    );
    await ambiguous.load();
    await ambiguous.review("Original", changes);
    await ambiguous.apply();
    await ambiguous.checkStatus();
    expect(ambiguous.getSnapshot().mutation).toBe("unconfirmed");
    await ambiguous.review("New", changes);
    await ambiguous.authorize();
    await ambiguous.apply();
    expect(sent).toBe(1);
    ambiguous.dispose();
    const remounted = fixture(
      { authorizationRequired: () => refused },
      new AbortController(),
      recovery,
    );
    expect(remounted.getSnapshot().mutation).toBe("unconfirmed");
    await remounted.apply();
    expect(remounted.getSnapshot().review).toBeUndefined();
    remounted.dispose();
  });
  test("authorization-required detail must match every field of this exact reviewed command", async () => {
    const exact = {
      $typeName: "graph.v1.SecurityOperationAuthorizationRequired" as const,
      changeId,
      expectedVersion: version,
      intentDigest: new Uint8Array(32).fill(4),
    };
    for (const detail of [
      { ...exact, changeId: new Uint8Array(16).fill(8) },
      { ...exact, intentDigest: new Uint8Array(32).fill(5) },
      { ...exact, expectedVersion: { ...version, revision: 4n } },
      {
        ...exact,
        expectedVersion: { ...version, digest: new Uint8Array(32).fill(8) },
      },
      {
        ...exact,
        expectedVersion: { ...version, generation: new Uint8Array(16).fill(8) },
      },
    ]) {
      const controller = fixture({
        apply: async () => {
          throw detail;
        },
        authorizationRequired: () => detail,
      });
      await controller.load();
      await controller.review("Original", changes);
      await controller.apply();
      expect(controller.getSnapshot().mutation).toBe("unconfirmed");
      controller.dispose();
    }
  });
  test("reload during delayed Begin closes only its owned popup", async () => {
    const finishes: Array<
      (
        value: Awaited<
          ReturnType<SecurityManagementPort["beginAuthorization"]>
        >,
      ) => void
    > = [];
    const closed: number[] = [];
    let opened = 0;
    const controller = fixture({
      prepare: async (review) => ({
        $typeName: "graph.v1.PrepareSecurityChangesResponse",
        expectedVersion: review.expectedVersion,
        changeId: review.changeId,
        intentDigest: new Uint8Array(32).fill(4),
        requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
      }),
      beginAuthorization: () =>
        new Promise((resolve) => {
          finishes.push(resolve);
        }),
      openAuthorization: () => {
        const id = ++opened;
        return {
          navigate() {},
          close() {
            closed.push(id);
          },
        };
      },
    });
    const start = {
      $typeName: "graph.v1.BeginSecurityChangeAuthorizationResponse" as const,
      authorizationId: new Uint8Array(32).fill(8),
      startUrl:
        "https://admin.example/auth/management-authorization/" + "A".repeat(43),
      expiresAt: {
        $typeName: "google.protobuf.Timestamp" as const,
        seconds: 3000000000n,
        nanos: 0,
      },
    };
    await controller.load();
    await controller.review("First", changes);
    const old = controller.authorize();
    await controller.load();
    expect(closed).toContain(1);
    await controller.review("Second", changes);
    const current = controller.authorize();
    finishes[0](start);
    await old;
    expect(closed).not.toContain(2);
    finishes[1](start);
    await current;
    expect(controller.getSnapshot().review?.label).toBe("Second");
    expect(controller.getSnapshot().review?.approval).toBe("authenticating");
    controller.dispose();
  });
  test("operation approval uses the immutable reviewed ID and keeps proof out of response-loss recovery", async () => {
    const recovery = new SecurityChangeRecovery();
    let sent = 0,
      opened = 0,
      navigated = 0,
      closed = 0,
      pending = true;
    const controller = fixture(
      {
        prepare: async (review) => ({
          $typeName: "graph.v1.PrepareSecurityChangesResponse",
          expectedVersion: review.expectedVersion,
          changeId: review.changeId,
          intentDigest: new Uint8Array(32).fill(4),
          requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
        }),
        beginAuthorization: async (review) => {
          expect(review.changeId).toEqual(changeId);
          expect(review.expectedVersion).toEqual(version);
          expect(review.changes).toEqual(changes);
          return {
            $typeName: "graph.v1.BeginSecurityChangeAuthorizationResponse",
            authorizationId: new Uint8Array(32).fill(8),
            startUrl:
              "https://admin.example/auth/management-authorization/" +
              "A".repeat(43),
            expiresAt: {
              $typeName: "google.protobuf.Timestamp",
              seconds: 3000000000n,
              nanos: 0,
            },
          };
        },
        openAuthorization: () => {
          opened++;
          return {
            navigate() {
              navigated++;
            },
            close() {
              closed++;
            },
          };
        },
        authorization: async (id) => ({
          $typeName: "graph.v1.GetSecurityChangeAuthorizationResponse",
          authorizationId: id,
          state: pending
            ? SecurityAuthorizationState.PENDING
            : SecurityAuthorizationState.APPROVED,
          authorizationProof: pending
            ? new Uint8Array()
            : new Uint8Array(32).fill(9),
          expiresAt: {
            $typeName: "google.protobuf.Timestamp",
            seconds: 3000000000n,
            nanos: 0,
          },
        }),
        apply: async (request) => {
          sent++;
          expect(request.changeId).toEqual(changeId);
          expect(request.authorizationProof).toEqual(
            new Uint8Array(32).fill(9),
          );
          throw new Error("response dropped");
        },
      },
      new AbortController(),
      recovery,
    );
    await controller.load();
    await controller.review("High impact", changes);
    await controller.apply();
    expect(sent).toBe(0);
    await controller.authorize();
    expect(opened).toBe(1);
    expect(navigated).toBe(1);
    await controller.checkAuthorization();
    expect(controller.getSnapshot().review?.approval).toBe("authenticating");
    await controller.apply();
    expect(sent).toBe(0);
    pending = false;
    await controller.checkAuthorization();
    expect(controller.getSnapshot().review?.approval).toBe("approved");
    await controller.apply();
    expect(sent).toBe(1);
    expect(closed).toBeGreaterThan(0);
    const retained = recovery.read("browser-session")!;
    expect("authorizationProof" in retained).toBe(false);
    controller.dispose();
    const remounted = fixture({}, new AbortController(), recovery);
    expect(remounted.getSnapshot().review).toBeUndefined();
    expect(remounted.getSnapshot().mutation).toBe("unconfirmed");
    await remounted.checkStatus();
    expect(remounted.getSnapshot().result?.applied).toBeUndefined();
    expect(sent).toBe(1);
    remounted.dispose();
  });
  test("a refused or malformed preflight sends no Apply and creates no ambiguous command", async () => {
    for (const prepare of [
      async () => {
        throw new Error("denied");
      },
      async () => ({
        $typeName: "graph.v1.PrepareSecurityChangesResponse" as const,
        expectedVersion: version,
        changeId: new Uint8Array(16).fill(8),
        intentDigest: new Uint8Array(32).fill(4),
        requirement: SecurityAuthorizationRequirement.ORDINARY,
      }),
    ]) {
      let sent = 0;
      const recovery = new SecurityChangeRecovery();
      const controller = fixture(
        {
          prepare,
          apply: async () => {
            sent++;
            return result;
          },
        },
        new AbortController(),
        recovery,
      );
      await controller.load();
      await controller.review("Delete", changes);
      await controller.apply();
      expect(sent).toBe(0);
      expect(controller.getSnapshot().mutation).toBe("idle");
      expect(controller.getSnapshot().review?.approval).toBe("failed");
      expect(recovery.read("browser-session")).toBeUndefined();
      controller.dispose();
    }
  });
  test("cancelled approval cannot publish proof into a later review", async () => {
    let complete!: (
      value: Awaited<ReturnType<SecurityManagementPort["authorization"]>>,
    ) => void;
    const controller = fixture({
      prepare: async (review) => ({
        $typeName: "graph.v1.PrepareSecurityChangesResponse",
        expectedVersion: review.expectedVersion,
        changeId: review.changeId,
        intentDigest: new Uint8Array(32).fill(4),
        requirement: SecurityAuthorizationRequirement.REAUTHENTICATION,
      }),
      authorization: () =>
        new Promise((resolve) => {
          complete = resolve;
        }),
    });
    await controller.load();
    await controller.review("First", changes);
    await controller.authorize();
    const checking = controller.checkAuthorization();
    controller.cancelReview();
    await controller.review("Second", changes);
    complete({
      $typeName: "graph.v1.GetSecurityChangeAuthorizationResponse",
      authorizationId: new Uint8Array(32).fill(8),
      state: SecurityAuthorizationState.APPROVED,
      authorizationProof: new Uint8Array(32).fill(9),
      expiresAt: {
        $typeName: "google.protobuf.Timestamp",
        seconds: 3000000000n,
        nanos: 0,
      },
    });
    await checking;
    expect(controller.getSnapshot().review?.label).toBe("Second");
    expect(controller.getSnapshot().review?.approval).toBe("required");
    expect(controller.getSnapshot().review?.authorizationProof).toBeUndefined();
    controller.dispose();
  });
  test("an ordinary review commits without a recent-auth gate with the inspected revision and aligned acknowledgement", async () => {
    let sent = 0;
    const controller = fixture({
      apply: async (request) => {
        sent++;
        expect(request.expectedRevision).toBe(3n);
        expect(request.changeId).toEqual(new Uint8Array(16).fill(7));
        return result;
      },
    });
    await controller.load();
    await controller.review("Delete reader", changes);
    expect(controller.getSnapshot().review?.approval).toBe("ordinary");
    await controller.apply();
    expect(sent).toBe(1);
    expect(controller.getSnapshot().mutation).toBe("pending");
    controller.dispose();
  });
  test("response loss retains the same ID for status and prevents a fresh mutation", async () => {
    let sent = 0,
      status = 0;
    const controller = fixture({
      apply: async () => {
        sent++;
        throw new Error("response dropped");
      },
      status: async (id) => {
        status++;
        expect(id).toEqual(new Uint8Array(16).fill(7));
        return { ...proof, enforcement: SecurityEnforcementState.ENFORCED };
      },
    });
    await controller.load();
    await controller.review("Delete reader", changes);
    await controller.apply();
    await controller.review("Delete another", changes);
    await controller.apply();
    expect(sent).toBe(1);
    expect(controller.getSnapshot().mutation).toBe("unconfirmed");
    await controller.checkStatus();
    expect(status).toBe(1);
    expect(controller.getSnapshot().mutation).toBe("enforced");
    expect(controller.getSnapshot().result?.applied).toBeUndefined();
    expect(controller.getSnapshot().result?.replayed).toBeUndefined();
    controller.dispose();
  });
  test("CAS conflict cannot be applied until a new load and review", async () => {
    let sent = 0;
    const controller = fixture({
      apply: async () => {
        sent++;
        throw new Error("conflict");
      },
      failure: () => "conflict",
    });
    await controller.load();
    await controller.review("Delete reader", changes);
    await controller.apply();
    await controller.review("Delete again", changes);
    await controller.apply();
    expect(sent).toBe(1);
    expect(controller.getSnapshot().version).toBeUndefined();
    expect(controller.getSnapshot().review).toBeUndefined();
    await controller.load();
    expect(controller.getSnapshot().mutation).toBe("idle");
    controller.dispose();
  });
  test("malformed and different-generation acknowledgements stay unconfirmed", async () => {
    for (const invalid of [
      { ...result, applied: [] },
      { ...result, enforcement: SecurityEnforcementState.UNSPECIFIED },
      { ...result, version: { ...result.version, revision: 3n } },
      { ...result, version: { ...result.version, revision: 5n } },
      {
        ...result,
        version: { ...version, generation: new Uint8Array(16).fill(3) },
      },
    ]) {
      const controller = fixture({ apply: async () => invalid });
      await controller.load();
      await controller.review("Delete", changes);
      await controller.apply();
      expect(controller.getSnapshot().mutation).toBe("unconfirmed");
      controller.dispose();
    }
  });
  test("expired scope cannot receive stale granting state", async () => {
    const scope = new AbortController();
    let finish!: (value: typeof result) => void;
    const controller = fixture(
      {
        apply: () =>
          new Promise((resolve) => {
            finish = resolve;
          }),
      },
      scope,
    );
    await controller.load();
    await controller.review("Delete", changes);
    const applying = controller.apply();
    scope.abort();
    controller.dispose();
    finish(result);
    await applying;
    expect(controller.getSnapshot().mutation).toBe("sending");
  });
  test("proof-only status advances enforcement and preserves original aligned Apply outcomes across remount", async () => {
    const recovery = new SecurityChangeRecovery();
    let sent = 0,
      ids = 0;
    const acknowledgement = {
      ...result,
      applied: [true, false],
      replayed: true,
    };
    const before = fixture(
      {
        apply: async () => {
          sent++;
          return acknowledgement;
        },
        newChangeId: () => {
          ids++;
          return changeId;
        },
      },
      new AbortController(),
      recovery,
    );
    await before.load();
    await before.review("Two changes", [...changes, ...changes]);
    await before.apply();
    before.dispose();
    const after = fixture(
      {
        status: async () => ({
          ...proof,
          enforcement: SecurityEnforcementState.ENFORCED,
        }),
        apply: async () => {
          sent++;
          return acknowledgement;
        },
        newChangeId: () => {
          ids++;
          return changeId;
        },
      },
      new AbortController(),
      recovery,
    );
    expect(after.getSnapshot().mutation).toBe("pending");
    await after.checkStatus();
    expect(after.getSnapshot().mutation).toBe("enforced");
    expect(after.getSnapshot().result?.applied).toEqual([true, false]);
    expect(after.getSnapshot().result?.replayed).toBe(true);
    expect(sent).toBe(1);
    expect(ids).toBe(1);
    after.dispose();
  });
  test("status rejects ID, generation, digest shape, revision and enforcement mismatches", async () => {
    for (const invalid of [
      { ...proof, changeId: new Uint8Array(16).fill(8) },
      { ...proof, changeId: new Uint8Array(15).fill(7) },
      { ...proof, version: undefined },
      {
        ...proof,
        version: { ...proof.version, generation: new Uint8Array(16).fill(3) },
      },
      {
        ...proof,
        version: { ...proof.version, generation: new Uint8Array(16) },
      },
      { ...proof, version: { ...proof.version, digest: new Uint8Array(31) } },
      { ...proof, version: { ...proof.version, revision: 3n } },
      { ...proof, version: { ...proof.version, revision: 5n } },
      { ...proof, enforcement: SecurityEnforcementState.UNSPECIFIED },
      { ...proof, enforcement: 99 as SecurityEnforcementState },
    ]) {
      let sent = 0,
        ids = 0;
      const controller = fixture({
        apply: async () => {
          sent++;
          throw new Error("response lost");
        },
        newChangeId: () => {
          ids++;
          return changeId;
        },
        status: async () => invalid,
      });
      await controller.load();
      await controller.review("Delete", changes);
      await controller.apply();
      await controller.checkStatus();
      expect(controller.getSnapshot().mutation).toBe("unconfirmed");
      expect(controller.getSnapshot().result).toBeUndefined();
      expect(controller.getSnapshot().message).toContain("unavailable");
      expect(controller.changeId()).toBe("07".repeat(16));
      expect(sent).toBe(1);
      expect(ids).toBe(1);
      controller.dispose();
    }
  });
  test("status must match the exact retained acknowledgement digest and preserves evidence on failure", async () => {
    const controller = fixture({
      status: async () => ({
        ...proof,
        version: { ...proof.version, digest: new Uint8Array(32).fill(9) },
        enforcement: SecurityEnforcementState.ENFORCED,
      }),
    });
    await controller.load();
    await controller.review("Delete", changes);
    await controller.apply();
    await controller.checkStatus();
    expect(controller.getSnapshot().mutation).toBe("pending");
    expect(controller.getSnapshot().result?.version?.digest).toEqual(
      result.version.digest,
    );
    expect(controller.getSnapshot().result?.applied).toEqual([true]);
    controller.dispose();
  });
  test("a superseded status operation cannot overwrite current enforcement", async () => {
    const finish: Array<(value: typeof proof) => void> = [];
    const controller = fixture({
      status: () => new Promise((resolve) => finish.push(resolve)),
    });
    await controller.load();
    await controller.review("Delete", changes);
    await controller.apply();
    const older = controller.checkStatus();
    const current = controller.checkStatus();
    finish[1]({ ...proof, enforcement: SecurityEnforcementState.ENFORCED });
    await current;
    finish[0](proof);
    await older;
    expect(controller.getSnapshot().mutation).toBe("enforced");
    expect(controller.getSnapshot().result?.applied).toEqual([true]);
    controller.dispose();
  });
  test("unknown or retired status is indeterminate and cannot trigger another send", async () => {
    for (const failure of ["not-found", "invalid"] as const) {
      let sent = 0,
        ids = 0;
      const controller = fixture({
        apply: async () => {
          sent++;
          throw new Error("response lost");
        },
        newChangeId: () => {
          ids++;
          return changeId;
        },
        status: async () => {
          throw new Error(failure);
        },
        failure: () => failure,
      });
      await controller.load();
      await controller.review("Delete", changes);
      await controller.apply();
      await controller.checkStatus();
      await controller.review("Resend", changes);
      await controller.apply();
      expect(controller.getSnapshot().mutation).toBe("unconfirmed");
      expect(controller.getSnapshot().result).toBeUndefined();
      expect(controller.changeId()).toBe("07".repeat(16));
      expect(sent).toBe(1);
      expect(ids).toBe(1);
      controller.dispose();
    }
  });
  test("policy remount retains only the immutable change ID for status recovery", async () => {
    const recovery = new SecurityChangeRecovery();
    let sent = 0;
    const before = fixture(
      {
        apply: async () => {
          sent++;
          throw new Error("response dropped");
        },
      },
      new AbortController(),
      recovery,
    );
    await before.load();
    await before.review("Delete two", [...changes, ...changes]);
    await before.apply();
    before.dispose();
    const after = fixture(
      {
        status: async (id) => {
          expect(id).toEqual(new Uint8Array(16).fill(7));
          return proof;
        },
      },
      new AbortController(),
      recovery,
    );
    expect(after.getSnapshot().mutation).toBe("unconfirmed");
    await after.checkStatus();
    expect(after.getSnapshot().mutation).toBe("pending");
    expect(after.getSnapshot().result?.applied).toBeUndefined();
    expect(sent).toBe(1);
    after.dispose();
    const confirmed = fixture({}, new AbortController(), recovery);
    expect(confirmed.getSnapshot().mutation).toBe("pending");
    expect(confirmed.getSnapshot().result?.applied).toBeUndefined();
    await confirmed.checkStatus();
    expect(confirmed.getSnapshot().mutation).toBe("pending");
    expect(confirmed.changeId()).toBe("07".repeat(16));
    confirmed.dispose();
    const other = fixture(
      {},
      new AbortController(),
      recovery,
      "other-browser-session",
    );
    expect(other.getSnapshot().mutation).toBe("idle");
    other.dispose();
  });
});

test("membership pages from another revision cannot populate the inspected Role", async () => {
  const controller = fixture({
    users: async () => ({
      $typeName: "graph.v1.ListUsersResponse",
      users: [],
      version: { ...version, revision: 4n },
      nextCursor: "foreign",
    }),
  });
  await controller.load();
  await controller.loadRoleMembers("reader");
  expect(controller.getSnapshot().memberCursor).toBe("");
  expect(controller.getSnapshot().memberRole).toBe("");
  expect(controller.getSnapshot().message).toContain("revision changed");
  controller.dispose();
});
