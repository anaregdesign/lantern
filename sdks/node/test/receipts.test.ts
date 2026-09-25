import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";

import {
  InvalidArgumentError,
  Lantern,
  LanternError,
  ReceiptMutationUncertainError,
  ReceiptReconciliationError,
  mintReceiptOperationContext,
  parseGroupID,
  parseOperationID,
  parseReceiptOperationContext,
  type EnabledReceiptCapability,
  type OperationID,
  type ReceiptOperationContext,
  type ReceiptReconciliationReason,
} from "../src/index.js";
import { authTokenInterceptor } from "../src/client.js";
import { LanternService, MutationReceiptState } from "../src/gen/graph/v1/graph_pb.js";

function filled(length: number, value: number): Uint8Array {
  return new Uint8Array(length).fill(value);
}

function hex(bytes: Uint8Array): string {
  return [...bytes].map((value) => value.toString(16).padStart(2, "0")).join("");
}

function deterministicRandom(first = 1): (target: Uint8Array) => void {
  let next = first;
  return (target) => {
    target.fill(next);
    next++;
  };
}

function counterRandom(): (target: Uint8Array) => void {
  let next = 1;
  return (target) => {
    target.fill(0);
    new DataView(target.buffer, target.byteOffset, target.byteLength).setUint32(
      target.byteLength - 4,
      next,
    );
    next++;
  };
}

interface FakeCapability {
  enabled: boolean;
  policy?: {
    deploymentEpoch: Uint8Array;
    fingerprint: Uint8Array;
    retentionMs: bigint;
    maxEntries: bigint;
    maxBytes: bigint;
  };
  endpoint?: {
    nodeId: Uint8Array;
    generation: Uint8Array;
  };
  serverNowUnixMs: bigint;
}

interface StoredDeleteReceipt {
  operationId: Uint8Array;
  logicalCallId: Uint8Array;
  itemIndex: number;
  itemCount: number;
  tail: string;
  head: string;
  existed: boolean;
}

class ReceiptTransportFake {
  capability: FakeCapability = {
    enabled: true,
    policy: {
      deploymentEpoch: filled(16, 0x21),
      fingerprint: filled(32, 0x22),
      retentionMs: 3_600_000n,
      maxEntries: 100n,
      maxBytes: 1_000_000n,
    },
    endpoint: {
      nodeId: filled(16, 0x23),
      generation: filled(16, 0x24),
    },
    serverNowUnixMs: 1_800_000_000_123n,
  };
  capabilityUnavailable = false;
  rotateGenerationBeforeDelete = false;
  dropNextDeleteResponse = false;
  malformedDeleteResponse = false;
  malformedStatusResponse = false;
  capabilityCalls = 0;
  deleteCalls = 0;
  mutationCount = 0;
  statusCalls = 0;
  readonly authorizationHeaders: Array<string | null> = [];
  readonly edges = new Set<string>();
  readonly receipts = new Map<string, StoredDeleteReceipt>();
  readonly statusOverrides = new Map<string, "notYetObserved" | "noLongerProvable">();

  private edgeKey(tail: string, head: string): string {
    return `${tail}\u0000${head}`;
  }

  addEdge(tail: string, head: string): void {
    this.edges.add(this.edgeKey(tail, head));
  }

  hasEdge(tail: string, head: string): boolean {
    return this.edges.has(this.edgeKey(tail, head));
  }

  transport(token?: string, readMaxBytes?: number) {
    return createRouterTransport(
      (router) => {
        router.service(LanternService, {
          getReceiptCapability: (_request, context) => {
            this.capabilityCalls++;
            this.authorizationHeaders.push(context.requestHeader.get("Authorization"));
            if (this.capabilityUnavailable) {
              throw new ConnectError("receipt capability unavailable", Code.Unavailable);
            }
            return this.capability;
          },
          getReceiptStatuses: (request, context) => {
            this.statusCalls++;
            this.authorizationHeaders.push(context.requestHeader.get("Authorization"));
            const statuses = request.operationIds.map((operationId) => {
              const key = hex(operationId);
              const override = this.statusOverrides.get(key);
              if (override === "notYetObserved") {
                return {
                  operationId,
                  state: MutationReceiptState.NOT_YET_OBSERVED,
                };
              }
              if (override === "noLongerProvable") {
                return {
                  operationId,
                  state: MutationReceiptState.NO_LONGER_PROVABLE,
                };
              }
              const receipt = this.receipts.get(key);
              if (!receipt) {
                return {
                  operationId,
                  state: MutationReceiptState.NOT_YET_OBSERVED,
                };
              }
              return {
                operationId,
                state: MutationReceiptState.CONFIRMED,
                receipt: {
                  operationId,
                  logicalCallId: receipt.logicalCallId,
                  itemIndex: receipt.itemIndex,
                  itemCount: receipt.itemCount,
                  intentSha256: filled(32, receipt.itemIndex + 1),
                  deadlineUnixMs: 1_800_003_600_123n,
                  originalResult: {
                    result: {
                      case: "deleteEdgeExisted" as const,
                      value: receipt.existed,
                    },
                  },
                },
              };
            });
            return {
              statuses: this.malformedStatusResponse ? statuses.slice(1) : statuses,
            };
          },
          deleteEdges: (request, context) => {
            this.deleteCalls++;
            this.authorizationHeaders.push(context.requestHeader.get("Authorization"));
            if (this.rotateGenerationBeforeDelete) {
              this.rotateGenerationBeforeDelete = false;
              this.capability.endpoint!.generation = filled(16, 0x99);
              throw new ConnectError(
                "receipt endpoint does not match the active certified generation",
                Code.FailedPrecondition,
              );
            }
            const receiptContext = request.receiptContext;
            if (!receiptContext) {
              throw new ConnectError("receipt context required by fake", Code.InvalidArgument);
            }
            if (receiptContext.operationIds.length !== request.edges.length) {
              throw new ConnectError("misaligned receipt context", Code.InvalidArgument);
            }
            const group = hex(receiptContext.logicalCallId);
            for (let index = 0; index < request.edges.length; index++) {
              const edge = request.edges[index]!;
              const operationId = receiptContext.operationIds[index]!;
              const prior = this.receipts.get(hex(operationId));
              if (
                prior &&
                (hex(prior.logicalCallId) !== group ||
                  prior.tail !== edge.tail ||
                  prior.head !== edge.head)
              ) {
                throw new ConnectError("receipt intent conflict", Code.InvalidArgument);
              }
            }
            const existed = request.edges.map((edge, index) => {
              const operationId = receiptContext.operationIds[index]!;
              const key = hex(operationId);
              const prior = this.receipts.get(key);
              if (prior) return prior.existed;
              const edgeKey = this.edgeKey(edge.tail, edge.head);
              const original = this.edges.delete(edgeKey);
              this.receipts.set(key, {
                operationId: new Uint8Array(operationId),
                logicalCallId: new Uint8Array(receiptContext.logicalCallId),
                itemIndex: index,
                itemCount: request.edges.length,
                tail: edge.tail,
                head: edge.head,
                existed: original,
              });
              this.mutationCount++;
              return original;
            });
            if (this.dropNextDeleteResponse) {
              this.dropNextDeleteResponse = false;
              throw new ConnectError("injected committed response loss", Code.Unavailable);
            }
            if (this.malformedDeleteResponse) {
              return { deleted: 0, existed: existed.slice(1) };
            }
            return {
              deleted: existed.filter(Boolean).length,
              existed,
            };
          },
        });
      },
      token
        ? {
            transport: {
              interceptors: [authTokenInterceptor(token)],
              readMaxBytes,
            },
          }
        : readMaxBytes
          ? { transport: { readMaxBytes } }
          : undefined,
    );
  }
}

async function enabledCapability(client: Lantern): Promise<EnabledReceiptCapability> {
  const capability = await client.getReceiptCapability();
  if (!capability.enabled) {
    throw new Error("test requires enabled receipt capability");
  }
  return capability;
}

async function contextFor(
  client: Lantern,
  itemCount: number,
  firstRandomByte = 1,
): Promise<ReceiptOperationContext> {
  return mintReceiptOperationContext(
    await enabledCapability(client),
    itemCount,
    deterministicRandom(firstRandomByte),
  );
}

describe("receipt identity", () => {
  test("mints deterministic canonical IDs and restores the JSON context", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const capability = await enabledCapability(client);
    const context = mintReceiptOperationContext(capability, 2, deterministicRandom(0x31));
    const issuedHex = capability.serverNowUnixMs.toString(16).padStart(16, "0");

    expect(context.groupId).toBe("31".repeat(16));
    expect(context.operationIds).toEqual([
      `01${"21".repeat(16)}${issuedHex}${"32".repeat(24)}`,
      `01${"21".repeat(16)}${issuedHex}${"33".repeat(24)}`,
    ]);
    expect(parseReceiptOperationContext(JSON.parse(JSON.stringify(context)))).toEqual(context);
    expect(parseOperationID(context.operationIds[0]!.toUpperCase())).toBe(context.operationIds[0]);
    expect(parseGroupID(context.groupId.toUpperCase())).toBe(context.groupId);
  });

  test("rejects malformed IDs, groups, endpoints, and mixed epochs before transport", async () => {
    expect(() => parseOperationID("01")).toThrow(InvalidArgumentError);
    expect(() =>
      parseOperationID(`01${"21".repeat(16)}${"00".repeat(8)}${"00".repeat(24)}`),
    ).toThrow(/randomness/);
    expect(() => parseGroupID("00".repeat(16))).toThrow(/nonzero/);

    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 2, 0x41);
    fake.capabilityCalls = 0;

    const malformedEndpoint = {
      ...context,
      continuity: {
        ...context.continuity,
        generation: "00".repeat(16),
      },
    } as ReceiptOperationContext;
    await expect(
      client.deleteEdgesWithReceipt(
        [
          { tail: "a", head: "b" },
          { tail: "c", head: "d" },
        ],
        malformedEndpoint,
      ),
    ).rejects.toBeInstanceOf(InvalidArgumentError);
    expect(fake.capabilityCalls).toBe(0);
    expect(fake.deleteCalls).toBe(0);

    const other = new ReceiptTransportFake();
    other.capability.policy!.deploymentEpoch = filled(16, 0x42);
    const otherContext = await contextFor(Lantern.withTransport(other.transport()), 1, 0x51);
    const mixed = {
      ...context,
      operationIds: [context.operationIds[0]!, otherContext.operationIds[0]!],
    } as ReceiptOperationContext;
    await expect(
      client.deleteEdgesWithReceipt(
        [
          { tail: "a", head: "b" },
          { tail: "c", head: "d" },
        ],
        mixed,
      ),
    ).rejects.toThrow(/different deployment epoch/);
    expect(fake.capabilityCalls).toBe(0);
    expect(fake.deleteCalls).toBe(0);
  });

  test("rejects zero identity fields in an enabled capability", async () => {
    const fake = new ReceiptTransportFake();
    fake.capability.endpoint!.nodeId = filled(16, 0);
    const client = Lantern.withTransport(fake.transport());
    await expect(client.getReceiptCapability()).rejects.toBeInstanceOf(LanternError);
  });
});

describe("receipt capability and continuity", () => {
  test("surfaces disabled and unavailable capability without sending a mutation", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0x61);

    fake.capability = { enabled: false, serverNowUnixMs: 0n };
    await expect(client.getReceiptCapability()).resolves.toEqual({ enabled: false });
    let disabled: unknown;
    try {
      await client.deleteEdgeWithReceipt("a", "b", context);
    } catch (error) {
      disabled = error;
    }
    expect(disabled).toBeInstanceOf(ReceiptReconciliationError);
    expect((disabled as ReceiptReconciliationError).reason).toBe("capabilityDisabled");

    fake.capabilityUnavailable = true;
    let unavailable: unknown;
    try {
      await client.deleteEdgeWithReceipt("a", "b", context);
    } catch (error) {
      unavailable = error;
    }
    expect(unavailable).toBeInstanceOf(ReceiptReconciliationError);
    expect((unavailable as ReceiptReconciliationError).reason).toBe("capabilityUnavailable");
    expect(fake.deleteCalls).toBe(0);
  });

  test.each([
    ["deploymentEpochChanged", "epoch"],
    ["policyChanged", "policy"],
    ["nodeChanged", "node"],
    ["generationChanged", "generation"],
  ] as const)("rejects %s continuity before the destructive transport", async (reason, changed) => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0x71);
    fake.capabilityCalls = 0;

    switch (changed) {
      case "epoch":
        fake.capability.policy!.deploymentEpoch = filled(16, 0x81);
        break;
      case "policy":
        fake.capability.policy!.fingerprint = filled(32, 0x82);
        break;
      case "node":
        fake.capability.endpoint!.nodeId = filled(16, 0x83);
        break;
      case "generation":
        fake.capability.endpoint!.generation = filled(16, 0x84);
        break;
    }

    let caught: unknown;
    try {
      await client.deleteEdgeWithReceipt("a", "b", context);
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(ReceiptReconciliationError);
    expect((caught as ReceiptReconciliationError).reason).toBe(
      reason satisfies ReceiptReconciliationReason,
    );
    expect(fake.capabilityCalls).toBe(1);
    expect(fake.deleteCalls).toBe(0);
  });

  test("bearer token rotation does not change receipt identity", async () => {
    const fake = new ReceiptTransportFake();
    const oldClient = Lantern.withTransport(fake.transport("old-token"));
    const context = await contextFor(oldClient, 1, 0x91);

    fake.addEdge("token", "rotation");
    const newClient = Lantern.withTransport(fake.transport("new-token"));
    const result = await newClient.deleteEdgeWithReceipt("token", "rotation", context);

    expect(result.existed).toBe(true);
    expect(fake.authorizationHeaders).toContain("Bearer old-token");
    expect(fake.authorizationHeaders).toContain("Bearer new-token");
  });

  test("maps continuity rotation between preflight and mutation to a typed error", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0x95);
    fake.capabilityCalls = 0;
    fake.rotateGenerationBeforeDelete = true;

    let caught: unknown;
    try {
      await client.deleteEdgeWithReceipt("racing", "generation", context);
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(ReceiptReconciliationError);
    expect((caught as ReceiptReconciliationError).reason).toBe("generationChanged");
    expect(fake.capabilityCalls).toBe(2);
    expect(fake.mutationCount).toBe(0);
  });
});

describe("receipt Edge Delete", () => {
  test("maps plural results by request index and keeps singular as a plural facade", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    fake.addEdge("present", "edge");
    const pluralContext = await contextFor(client, 2, 0xa1);

    const plural = await client.deleteEdgesWithReceipt(
      [
        { tail: "present", head: "edge" },
        { tail: "absent", head: "edge" },
      ],
      pluralContext,
    );
    expect(plural.deleted).toBe(1);
    expect(plural.results).toEqual([
      {
        tail: "present",
        head: "edge",
        operationId: pluralContext.operationIds[0],
        existed: true,
      },
      {
        tail: "absent",
        head: "edge",
        operationId: pluralContext.operationIds[1],
        existed: false,
      },
    ]);

    fake.addEdge("single", "edge");
    const singularContext = await contextFor(client, 1, 0xb1);
    const singular = await client.deleteEdgeWithReceipt("single", "edge", singularContext);
    expect(singular).toEqual({
      tail: "single",
      head: "edge",
      operationId: singularContext.operationIds[0],
      existed: true,
    });
    expect(fake.deleteCalls).toBe(2);
  });

  test("reuses the exact context after committed response loss", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    fake.addEdge("lost", "response");
    const context = await contextFor(client, 1, 0xc1);
    fake.dropNextDeleteResponse = true;

    let caught: unknown;
    try {
      await client.deleteEdgeWithReceipt("lost", "response", context);
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(ReceiptMutationUncertainError);
    expect((caught as ReceiptMutationUncertainError).context).toEqual(context);
    expect((caught as ReceiptMutationUncertainError).edges).toEqual([
      { tail: "lost", head: "response" },
    ]);
    expect(fake.hasEdge("lost", "response")).toBe(false);

    const replay = await client.deleteEdgeWithReceipt("lost", "response", context);
    expect(replay.existed).toBe(true);
    expect(fake.mutationCount).toBe(1);
  });

  test("keeps a semantic intent conflict definite and non-mutating", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    fake.addEdge("first", "edge");
    fake.addEdge("protected", "edge");
    const context = await contextFor(client, 1, 0xd1);

    await client.deleteEdgeWithReceipt("first", "edge", context);
    await expect(client.deleteEdgeWithReceipt("protected", "edge", context)).rejects.toBeInstanceOf(
      InvalidArgumentError,
    );
    expect(fake.hasEdge("protected", "edge")).toBe(true);
    expect(fake.mutationCount).toBe(1);
  });

  test("marks a malformed post-mutation response as uncertain", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0xe1);
    fake.malformedDeleteResponse = true;

    await expect(
      client.deleteEdgeWithReceipt("malformed", "response", context),
    ).rejects.toBeInstanceOf(ReceiptMutationUncertainError);
  });

  test("marks a post-commit response-size rejection as uncertain", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport(undefined, 256));
    const itemCount = 1_000;
    const capability = await enabledCapability(client);
    const context = mintReceiptOperationContext(capability, itemCount, counterRandom());
    const edges = Array.from({ length: itemCount }, (_, index) => ({
      tail: `tail:${index}`,
      head: `head:${index}`,
    }));
    fake.addEdge(edges[0]!.tail, edges[0]!.head);

    await expect(client.deleteEdgesWithReceipt(edges, context)).rejects.toBeInstanceOf(
      ReceiptMutationUncertainError,
    );
    expect(fake.hasEdge(edges[0]!.tail, edges[0]!.head)).toBe(false);
    expect(fake.mutationCount).toBe(itemCount);
  });
});

describe("receipt status", () => {
  test("preserves all three states, original false, duplicates, and singular mapping", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 3, 0xf1);
    await client.deleteEdgesWithReceipt(
      [
        { tail: "missing", head: "confirmed" },
        { tail: "pending", head: "edge" },
        { tail: "expired", head: "edge" },
      ],
      context,
    );
    fake.statusOverrides.set(context.operationIds[1]!, "notYetObserved");
    fake.statusOverrides.set(context.operationIds[2]!, "noLongerProvable");

    const statuses = await client.getReceiptStatuses(context.operationIds);
    expect(statuses.map((status) => status.state)).toEqual([
      "confirmed",
      "notYetObserved",
      "noLongerProvable",
    ]);
    expect(statuses[0]).toMatchObject({
      state: "confirmed",
      operationId: context.operationIds[0],
      receipt: {
        operationId: context.operationIds[0],
        groupId: context.groupId,
        itemIndex: 0,
        itemCount: 3,
        originalResult: { kind: "deleteEdge", existed: false },
      },
    });
    expect(statuses[1]).not.toHaveProperty("receipt");
    expect(statuses[2]).not.toHaveProperty("receipt");

    const duplicates = await client.getReceiptStatuses([
      context.operationIds[1]!,
      context.operationIds[0]!,
      context.operationIds[1]!,
    ]);
    expect(duplicates.map((status) => status.operationId)).toEqual([
      context.operationIds[1],
      context.operationIds[0],
      context.operationIds[1],
    ]);

    const singular = await client.getReceiptStatus(context.operationIds[0]!);
    expect(singular.state).toBe("confirmed");
    expect(fake.statusCalls).toBe(3);
  });

  test("rejects malformed inputs before transport and misaligned wire output", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());

    await expect(client.getReceiptStatuses([])).rejects.toBeInstanceOf(InvalidArgumentError);
    await expect(client.getReceiptStatuses(["01" as OperationID])).rejects.toBeInstanceOf(
      InvalidArgumentError,
    );
    expect(fake.statusCalls).toBe(0);

    const context = await contextFor(client, 1, 0x11);
    fake.malformedStatusResponse = true;
    await expect(client.getReceiptStatuses(context.operationIds)).rejects.toBeInstanceOf(
      LanternError,
    );
  });
});
