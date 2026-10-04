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
const result = {
  $typeName: "graph.v1.ApplySecurityChangesResponse" as const,
  version: { ...version, revision: 4n },
  applied: [true],
  replayed: false,
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
    status: async () => result,
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
        return { ...result, enforcement: SecurityEnforcementState.ENFORCED };
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
    before.review("Delete", changes);
    await before.apply(true);
    before.dispose();
    const after = fixture(
      {
        status: async (id) => {
          expect(id).toEqual(new Uint8Array(16).fill(7));
          return result;
        },
      },
      new AbortController(),
      recovery,
    );
    expect(after.getSnapshot().mutation).toBe("unconfirmed");
    await after.checkStatus();
    expect(after.getSnapshot().mutation).toBe("pending");
    expect(sent).toBe(1);
    after.dispose();
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
