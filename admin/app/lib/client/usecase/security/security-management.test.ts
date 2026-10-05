import { describe, expect, test } from "bun:test";
import {
  SecurityEnforcementState,
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
  test("a recent-auth review commits with the inspected revision and aligned acknowledgement", async () => {
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
    controller.review("Delete reader", changes);
    await controller.apply(false);
    expect(sent).toBe(0);
    await controller.apply(true);
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
    controller.review("Delete reader", changes);
    await controller.apply(true);
    controller.review("Delete another", changes);
    await controller.apply(true);
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
    controller.review("Delete reader", changes);
    await controller.apply(true);
    controller.review("Delete again", changes);
    await controller.apply(true);
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
      controller.review("Delete", changes);
      await controller.apply(true);
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
    controller.review("Delete", changes);
    const applying = controller.apply(true);
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
    before.review("Two changes", [...changes, ...changes]);
    await before.apply(true);
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
      controller.review("Delete", changes);
      await controller.apply(true);
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
    controller.review("Delete", changes);
    await controller.apply(true);
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
    controller.review("Delete", changes);
    await controller.apply(true);
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
      controller.review("Delete", changes);
      await controller.apply(true);
      await controller.checkStatus();
      controller.review("Resend", changes);
      await controller.apply(true);
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
    before.review("Delete two", [...changes, ...changes]);
    await before.apply(true);
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
