import { describe, expect, test } from "bun:test";
import {
  parseReceiptOperationContext,
  type ReceiptStatus,
} from "lantern-sdk/web";
import type { AddRecoveryGateway } from "~/lib/client/infrastructure/api/add-recovery";
import { AddRecoveryStore, type AddRecoveryStorage } from "./add-recovery";

const context = parseReceiptOperationContext({
  operationIds: ["01" + "11".repeat(16) + "00".repeat(8) + "22".repeat(24)],
  groupId: "33".repeat(16),
  continuity: {
    deploymentEpoch: "11".repeat(16),
    policyFingerprint: "44".repeat(32),
    nodeId: "55".repeat(16),
    generation: "66".repeat(16),
  },
});
const edge = { tail: "orders:a", head: "orders:b", weight: 7 };
const confirmed = {
  state: "confirmed",
  operationId: context.operationIds[0],
  receipt: {
    operationId: context.operationIds[0],
    groupId: context.groupId,
    itemIndex: 0,
    itemCount: 1,
    intentSha256: "77".repeat(32),
    deadlineUnixMs: 1000n,
    originalResult: { kind: "addEdge", effectiveWeight: 7 },
  },
} as ReceiptStatus;
function fixture() {
  let raw: string | null = null,
    serial = 0,
    sends = 0,
    statuses = 0;
  const storage: AddRecoveryStorage = {
    read: () => raw,
    write: (value) => {
      raw = value;
    },
    newID: () => String(++serial),
  };
  const gateway: AddRecoveryGateway = {
    prepare: async (input) => ({ input, receipt: context }),
    send: async () => {
      sends++;
      expect(raw).toContain(context.operationIds[0]);
      throw new Error("lost response");
    },
    status: async () => {
      statuses++;
      return confirmed;
    },
    definiteRefusal: () => false,
  };
  return {
    storage,
    gateway,
    store: new AddRecoveryStore(storage),
    counts: () => ({ sends, statuses }),
    raw: () => raw,
  };
}
describe("Retained Add uncertainty", () => {
  test("saved before send; reload and original-ID status recover without another Add", async () => {
    const f = fixture();
    await expect(f.store.add("actor", edge, f.gateway)).rejects.toThrow(
      "outcome is unknown",
    );
    const restored = new AddRecoveryStore(f.storage);
    const item = restored.pending("actor", edge.tail, edge.head)!;
    expect(item.receipt).toEqual(context);
    await restored.check(item.id, "actor", f.gateway);
    expect(restored.getSnapshot()[0].phase).toBe("confirmed");
    expect(f.counts()).toEqual({ sends: 1, statuses: 1 });
  });
  test("unavailable status and a later attempted refusal cannot clear original ambiguity", async () => {
    const f = fixture();
    await expect(f.store.add("actor", edge, f.gateway)).rejects.toThrow();
    const item = f.store.getSnapshot()[0];
    await f.store.check(item.id, "actor", {
      ...f.gateway,
      status: async () => {
        throw new Error("unavailable");
      },
    });
    await expect(
      f.store.add("actor", edge, { ...f.gateway, definiteRefusal: () => true }),
    ).rejects.toThrow("outcome is unknown");
    expect(f.store.getSnapshot()[0].receipt).toEqual(context);
    expect(f.counts().sends).toBe(1);
  });
  for (const state of [
    "notYetObserved",
    "noLongerProvable",
    "effectUndisclosed",
  ] as const) {
    test(`${state} supplies no resend permission`, async () => {
      const f = fixture();
      await expect(f.store.add("actor", edge, f.gateway)).rejects.toThrow();
      const item = f.store.getSnapshot()[0];
      await f.store.check(item.id, "actor", {
        ...f.gateway,
        status: async () => ({ state, operationId: context.operationIds[0] }),
      });
      expect(f.store.pending("actor", edge.tail, edge.head)).toBeDefined();
      expect(f.counts().sends).toBe(1);
    });
  }
  test("legacy Add stays unknown without using current Edge as original proof", async () => {
    const f = fixture();
    const g = {
      ...f.gateway,
      prepare: async (input: typeof edge) => ({ input }),
      send: async () => {
        throw new Error("loss");
      },
    };
    await expect(f.store.add("actor", edge, g)).rejects.toThrow();
    await f.store.check(f.store.getSnapshot()[0].id, "actor", g);
    expect(f.store.getSnapshot()[0].message).toContain(
      "Reading the current edge cannot confirm",
    );
    expect(f.counts().statuses).toBe(0);
  });
  test("storage failure prevents preparation and first dispatch", async () => {
    const f = fixture();
    let prepares = 0;
    const store = new AddRecoveryStore({
      ...f.storage,
      write: () => {
        throw new Error("quota");
      },
    });
    await expect(
      store.add("actor", edge, {
        ...f.gateway,
        prepare: async (input) => {
          prepares++;
          return { input };
        },
      }),
    ).rejects.toThrow("quota");
    expect(prepares).toBe(0);
    expect(f.counts().sends).toBe(0);
  });
  test("reservation excludes concurrent duplicate Add while capability is pending", async () => {
    const f = fixture();
    let complete!: (value: {
      input: typeof edge;
      receipt: typeof context;
    }) => void;
    const first = f.store.add("actor", edge, {
      ...f.gateway,
      prepare: () =>
        new Promise((done) => {
          complete = done;
        }),
    });
    await expect(f.store.add("actor", edge, f.gateway)).rejects.toThrow(
      "outcome is unknown",
    );
    complete({ input: edge, receipt: context });
    await expect(first).rejects.toThrow();
    expect(f.counts().sends).toBe(1);
  });
  test("another identity cannot reconcile the retained attempt", async () => {
    const f = fixture();
    await expect(f.store.add("actor", edge, f.gateway)).rejects.toThrow();
    await f.store.check(f.store.getSnapshot()[0].id, "different", f.gateway);
    expect(f.counts().statuses).toBe(0);
    expect(f.store.getSnapshot()[0].phase).toBe("uncertain");
  });
  test("invalid storage fails closed; reloaded sending is never auto-dispatched", async () => {
    const f = fixture();
    f.storage.write("not JSON");
    await expect(
      new AddRecoveryStore(f.storage).add("actor", edge, f.gateway),
    ).rejects.toThrow("cannot be read");
    f.storage.write(
      JSON.stringify([
        {
          id: "old",
          actor: "actor",
          tail: edge.tail,
          head: edge.head,
          phase: "sending",
          receipt: context,
        },
      ]),
    );
    const restored = new AddRecoveryStore(f.storage);
    expect(restored.getSnapshot()[0].phase).toBe("uncertain");
    expect(f.counts().sends).toBe(0);
  });
  test("refusal of one first dispatch preserves a different unresolved attempt", async () => {
    const f = fixture();
    await expect(f.store.add("actor", edge, f.gateway)).rejects.toThrow();
    await expect(
      f.store.add(
        "actor",
        { ...edge, head: "orders:c" },
        { ...f.gateway, definiteRefusal: () => true },
      ),
    ).rejects.toThrow();
    expect(f.store.getSnapshot()).toHaveLength(1);
    expect(f.store.pending("actor", edge.tail, edge.head)).toBeDefined();
  });
});
