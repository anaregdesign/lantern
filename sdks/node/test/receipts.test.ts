import { describe, expect, test } from "bun:test";
import { toJson } from "@bufbuild/protobuf";
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
  type EdgeAddReceiptInput,
  type OperationID,
  type ReceiptOperationContext,
  type ReceiptReconciliationReason,
} from "../src/index.js";
import { authTokenInterceptor } from "../src/client.js";
import {
  LanternService,
  EdgeSchema,
  MutationReceiptState,
  PutOutcome as PbPutOutcome,
  ReceiptMutationKind as PbReceiptMutationKind,
  VertexSchema,
} from "../src/gen/graph/v1/graph_pb.js";

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
  supportedMutations: PbReceiptMutationKind[];
}

type StoredOriginalResult =
  | {
      case: "putVertexOutcome";
      value: PbPutOutcome;
    }
  | {
      case: "deleteVertexExisted";
      value: boolean;
    }
  | {
      case: "deleteEdgeExisted";
      value: boolean;
    }
  | {
      case: "addEdgeEffectiveWeight";
      value: number;
    };

interface StoredReceipt {
  operationId: Uint8Array;
  logicalCallId: Uint8Array;
  itemIndex: number;
  itemCount: number;
  intent: string;
  originalResult: StoredOriginalResult;
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
    supportedMutations: [
      PbReceiptMutationKind.PUT_VERTEX,
      PbReceiptMutationKind.DELETE_VERTEX,
      PbReceiptMutationKind.DELETE_EDGE,
      PbReceiptMutationKind.ADD_EDGE,
    ],
  };
  capabilityUnavailable = false;
  rotateGenerationBeforeDelete = false;
  dropNextDeleteResponse = false;
  dropNextPutResponse = false;
  dropNextVertexDeleteResponse = false;
  dropNextAddResponse = false;
  malformedDeleteResponse = false;
  malformedPutResponse = false;
  malformedVertexDeleteResponse = false;
  malformedAddResponse = false;
  malformedStatusResponse = false;
  putOutcomesOverride?: PbPutOutcome[];
  addWrittenOverride?: number;
  capabilityCalls = 0;
  putCalls = 0;
  vertexDeleteCalls = 0;
  deleteCalls = 0;
  addCalls = 0;
  mutationCount = 0;
  statusCalls = 0;
  readonly authorizationHeaders: Array<string | null> = [];
  readonly vertices = new Map<string, string>();
  readonly edges = new Set<string>();
  readonly edgeWeights = new Map<string, number>();
  readonly receipts = new Map<string, StoredReceipt>();
  readonly statusOverrides = new Map<string, "notYetObserved" | "noLongerProvable">();
  lastPutVertexJson: string[] = [];
  lastAddEdgeJson: string[] = [];
  lastAddContribIds: Uint8Array[] = [];

  private edgeKey(tail: string, head: string): string {
    return `${tail}\u0000${head}`;
  }

  addEdge(tail: string, head: string, weight = 1): void {
    const key = this.edgeKey(tail, head);
    this.edges.add(key);
    this.edgeWeights.set(key, weight);
  }

  hasEdge(tail: string, head: string): boolean {
    return this.edges.has(this.edgeKey(tail, head));
  }

  edgeWeight(tail: string, head: string): number | undefined {
    return this.edgeWeights.get(this.edgeKey(tail, head));
  }

  addVertex(key: string, value = "seed"): void {
    this.vertices.set(key, value);
  }

  hasVertex(key: string): boolean {
    return this.vertices.has(key);
  }

  private validateReceiptIntents(
    receiptContext: {
      operationIds: Uint8Array[];
      logicalCallId: Uint8Array;
    },
    intents: readonly string[],
  ): void {
    if (receiptContext.operationIds.length !== intents.length) {
      throw new ConnectError("misaligned receipt context", Code.InvalidArgument);
    }
    const group = hex(receiptContext.logicalCallId);
    for (let index = 0; index < intents.length; index++) {
      const operationId = receiptContext.operationIds[index]!;
      const prior = this.receipts.get(hex(operationId));
      if (
        prior &&
        (hex(prior.logicalCallId) !== group ||
          prior.itemIndex !== index ||
          prior.itemCount !== intents.length ||
          prior.intent !== intents[index])
      ) {
        throw new ConnectError("receipt intent conflict", Code.InvalidArgument);
      }
    }
  }

  private storeReceipt(
    receiptContext: {
      operationIds: Uint8Array[];
      logicalCallId: Uint8Array;
    },
    index: number,
    itemCount: number,
    intent: string,
    originalResult: StoredOriginalResult,
  ): StoredReceipt {
    const operationId = receiptContext.operationIds[index]!;
    const receipt: StoredReceipt = {
      operationId: new Uint8Array(operationId),
      logicalCallId: new Uint8Array(receiptContext.logicalCallId),
      itemIndex: index,
      itemCount,
      intent,
      originalResult,
    };
    this.receipts.set(hex(operationId), receipt);
    this.mutationCount++;
    return receipt;
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
                    result: receipt.originalResult,
                  },
                },
              };
            });
            return {
              statuses: this.malformedStatusResponse ? statuses.slice(1) : statuses,
            };
          },
          putVertices: (request, context) => {
            this.putCalls++;
            this.authorizationHeaders.push(context.requestHeader.get("Authorization"));
            const receiptContext = request.receiptContext;
            if (!receiptContext) {
              throw new ConnectError("receipt context required by fake", Code.InvalidArgument);
            }
            const vertexJson = request.vertices.map((vertex) => {
              const json = JSON.stringify(toJson(VertexSchema, vertex));
              if (json === undefined) {
                throw new ConnectError("failed to serialize fake Vertex", Code.Internal);
              }
              return json;
            });
            this.lastPutVertexJson = vertexJson;
            const intents = vertexJson.map(
              (vertex, index) =>
                `putVertex:${request.ifAbsent}:${request.vertices[index]!.key}:${vertex}`,
            );
            this.validateReceiptIntents(receiptContext, intents);
            const outcomes = request.vertices.map((vertex, index) => {
              const operationId = receiptContext.operationIds[index]!;
              const prior = this.receipts.get(hex(operationId));
              if (prior) {
                if (prior.originalResult.case !== "putVertexOutcome") {
                  throw new ConnectError("receipt result family conflict", Code.InvalidArgument);
                }
                return prior.originalResult.value;
              }
              const json = JSON.parse(vertexJson[index]!) as Record<string, unknown>;
              const expiration =
                typeof json.expiration === "string" ? Date.parse(json.expiration) : undefined;
              const defaultOutcome =
                request.ifAbsent && this.vertices.has(vertex.key)
                  ? PbPutOutcome.CONDITION_NOT_MET
                  : expiration !== undefined &&
                      expiration <= Number(this.capability.serverNowUnixMs)
                    ? PbPutOutcome.EXPIRED
                    : PbPutOutcome.APPLIED_AND_LIVE;
              const outcome = this.putOutcomesOverride?.[index] ?? defaultOutcome;
              if (outcome === PbPutOutcome.APPLIED_AND_LIVE) {
                this.vertices.set(vertex.key, vertexJson[index]!);
              } else if (outcome === PbPutOutcome.EXPIRED) {
                this.vertices.delete(vertex.key);
              }
              this.storeReceipt(receiptContext, index, request.vertices.length, intents[index]!, {
                case: "putVertexOutcome",
                value: outcome,
              });
              return outcome;
            });
            if (this.dropNextPutResponse) {
              this.dropNextPutResponse = false;
              throw new ConnectError("injected committed response loss", Code.Unavailable);
            }
            return {
              outcomes: this.malformedPutResponse ? outcomes.slice(1) : outcomes,
            };
          },
          deleteVertices: (request, context) => {
            this.vertexDeleteCalls++;
            this.authorizationHeaders.push(context.requestHeader.get("Authorization"));
            const receiptContext = request.receiptContext;
            if (!receiptContext) {
              throw new ConnectError("receipt context required by fake", Code.InvalidArgument);
            }
            const intents = request.keys.map((key) => `deleteVertex:${key}`);
            this.validateReceiptIntents(receiptContext, intents);
            const existed = request.keys.map((key, index) => {
              const operationId = receiptContext.operationIds[index]!;
              const prior = this.receipts.get(hex(operationId));
              if (prior) {
                if (prior.originalResult.case !== "deleteVertexExisted") {
                  throw new ConnectError("receipt result family conflict", Code.InvalidArgument);
                }
                return prior.originalResult.value;
              }
              const original = this.vertices.delete(key);
              this.storeReceipt(receiptContext, index, request.keys.length, intents[index]!, {
                case: "deleteVertexExisted",
                value: original,
              });
              return original;
            });
            if (this.dropNextVertexDeleteResponse) {
              this.dropNextVertexDeleteResponse = false;
              throw new ConnectError("injected committed response loss", Code.Unavailable);
            }
            if (this.malformedVertexDeleteResponse) {
              return { deleted: 0, existed: existed.slice(1) };
            }
            return {
              deleted: existed.filter(Boolean).length,
              existed,
            };
          },
          addEdges: (request, context) => {
            this.addCalls++;
            this.authorizationHeaders.push(context.requestHeader.get("Authorization"));
            const receiptContext = request.receiptContext;
            if (!receiptContext) {
              throw new ConnectError("receipt context required by fake", Code.InvalidArgument);
            }
            const edgeJson = request.edges.map((edge) => {
              const json = JSON.stringify(toJson(EdgeSchema, edge));
              if (json === undefined) {
                throw new ConnectError("failed to serialize fake Edge", Code.Internal);
              }
              return json;
            });
            this.lastAddEdgeJson = edgeJson;
            this.lastAddContribIds = request.contribIds.map(
              (contribId) => new Uint8Array(contribId),
            );
            const intents = edgeJson.map(
              (edge, index) =>
                `addEdge:${hex(request.contribIds[index] ?? new Uint8Array())}:${edge}`,
            );
            this.validateReceiptIntents(receiptContext, intents);
            const effectiveWeights = request.edges.map((edge, index) => {
              const operationId = receiptContext.operationIds[index]!;
              const prior = this.receipts.get(hex(operationId));
              if (prior) {
                if (prior.originalResult.case !== "addEdgeEffectiveWeight") {
                  throw new ConnectError("receipt result family conflict", Code.InvalidArgument);
                }
                return prior.originalResult.value;
              }
              const json = JSON.parse(edgeJson[index]!) as Record<string, unknown>;
              const expiration =
                typeof json.expiration === "string" ? Date.parse(json.expiration) : undefined;
              const edgeKey = this.edgeKey(edge.tail, edge.head);
              const effective =
                expiration !== undefined && expiration <= Number(this.capability.serverNowUnixMs)
                  ? 0
                  : Math.fround((this.edgeWeights.get(edgeKey) ?? 0) + edge.weight);
              if (effective === 0) {
                this.edges.delete(edgeKey);
                this.edgeWeights.delete(edgeKey);
              } else {
                this.edges.add(edgeKey);
                this.edgeWeights.set(edgeKey, effective);
              }
              this.storeReceipt(receiptContext, index, request.edges.length, intents[index]!, {
                case: "addEdgeEffectiveWeight",
                value: effective,
              });
              return effective;
            });
            if (this.dropNextAddResponse) {
              this.dropNextAddResponse = false;
              throw new ConnectError("injected committed response loss", Code.Unavailable);
            }
            return {
              written: this.addWrittenOverride ?? request.edges.length,
              effectiveWeights: this.malformedAddResponse
                ? effectiveWeights.slice(1)
                : effectiveWeights,
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
            const intents = request.edges.map(
              (edge) => `deleteEdge:${edge.tail}\u0000${edge.head}`,
            );
            this.validateReceiptIntents(receiptContext, intents);
            const existed = request.edges.map((edge, index) => {
              const operationId = receiptContext.operationIds[index]!;
              const prior = this.receipts.get(hex(operationId));
              if (prior) {
                if (prior.originalResult.case !== "deleteEdgeExisted") {
                  throw new ConnectError("receipt result family conflict", Code.InvalidArgument);
                }
                return prior.originalResult.value;
              }
              const edgeKey = this.edgeKey(edge.tail, edge.head);
              const original = this.edges.delete(edgeKey);
              this.edgeWeights.delete(edgeKey);
              this.storeReceipt(receiptContext, index, request.edges.length, intents[index]!, {
                case: "deleteEdgeExisted",
                value: original,
              });
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

  test("rejects malformed Vertex receipt inputs and alignment before transport", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0x55);
    fake.capabilityCalls = 0;

    await expect(
      client.putVertexWithReceipt({ key: "", value: "invalid" }, context),
    ).rejects.toBeInstanceOf(InvalidArgumentError);
    await expect(client.deleteVerticesWithReceipt([], context)).rejects.toBeInstanceOf(
      InvalidArgumentError,
    );
    await expect(
      client.putVerticesWithReceipt(
        [
          { key: "one", value: 1 },
          { key: "two", value: 2 },
        ],
        context,
      ),
    ).rejects.toThrow(/operation IDs for 2 items/);
    expect(fake.capabilityCalls).toBe(0);
    expect(fake.putCalls).toBe(0);
    expect(fake.vertexDeleteCalls).toBe(0);
  });
});

describe("receipt capability and continuity", () => {
  test("decodes a stable supported-mutation list and rejects malformed capability lists", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    await expect(client.getReceiptCapability()).resolves.toMatchObject({
      enabled: true,
      supportedMutations: ["putVertex", "deleteVertex", "deleteEdge", "addEdge"],
    });

    for (const supportedMutations of [
      [PbReceiptMutationKind.UNSPECIFIED],
      [PbReceiptMutationKind.DELETE_VERTEX, PbReceiptMutationKind.PUT_VERTEX],
      [PbReceiptMutationKind.PUT_VERTEX, PbReceiptMutationKind.PUT_VERTEX],
      [PbReceiptMutationKind.ADD_EDGE, PbReceiptMutationKind.DELETE_EDGE],
      [99 as PbReceiptMutationKind],
    ]) {
      fake.capability.supportedMutations = supportedMutations;
      await expect(client.getReceiptCapability()).rejects.toBeInstanceOf(LanternError);
    }

    fake.capability = {
      enabled: false,
      serverNowUnixMs: 0n,
      supportedMutations: [PbReceiptMutationKind.DELETE_EDGE],
    };
    await expect(client.getReceiptCapability()).rejects.toBeInstanceOf(LanternError);
  });

  test("surfaces disabled and unavailable capability without sending a mutation", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0x61);

    fake.capability = { enabled: false, serverNowUnixMs: 0n, supportedMutations: [] };
    await expect(client.getReceiptCapability()).resolves.toEqual({
      enabled: false,
      supportedMutations: [],
    });
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

  test("rejects an unsupported mutation family before sending it", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0x65);
    fake.capability.supportedMutations = [PbReceiptMutationKind.DELETE_EDGE];
    fake.capabilityCalls = 0;

    let caught: unknown;
    try {
      await client.putVertexWithReceipt({ key: "unsupported", value: "value" }, context);
    } catch (error) {
      caught = error;
    }
    expect(caught).toBeInstanceOf(ReceiptReconciliationError);
    expect((caught as ReceiptReconciliationError).reason).toBe("mutationUnsupported");
    expect((caught as ReceiptReconciliationError).mutationKind).toBe("putVertex");
    expect(fake.capabilityCalls).toBe(1);
    expect(fake.putCalls).toBe(0);
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

  test("does not replay an uncertain mutation against a different endpoint", async () => {
    const first = new ReceiptTransportFake();
    const firstClient = Lantern.withTransport(first.transport());
    const context = await contextFor(firstClient, 1, 0x97);
    first.dropNextPutResponse = true;

    let uncertain: ReceiptMutationUncertainError | undefined;
    try {
      await firstClient.putVertexWithReceipt({ key: "endpoint-bound", value: "first" }, context);
    } catch (error) {
      if (error instanceof ReceiptMutationUncertainError) uncertain = error;
    }
    if (!uncertain || uncertain.mutation.kind !== "putVertex") {
      throw new Error("expected uncertain Vertex Put");
    }

    const second = new ReceiptTransportFake();
    second.capability.endpoint!.nodeId = filled(16, 0x98);
    const secondClient = Lantern.withTransport(second.transport());
    let rejected: unknown;
    try {
      await secondClient.putVerticesWithReceipt(uncertain.mutation.inputs, uncertain.context);
    } catch (error) {
      rejected = error;
    }
    expect(rejected).toBeInstanceOf(ReceiptReconciliationError);
    expect((rejected as ReceiptReconciliationError).reason).toBe("nodeChanged");
    expect(second.putCalls).toBe(0);
    expect(first.mutationCount).toBe(1);
  });
});

describe("receipt Vertex Put", () => {
  test("maps exact plural outcomes and keeps singular conditional Put as a plural facade", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    fake.putOutcomesOverride = [
      PbPutOutcome.APPLIED_AND_LIVE,
      PbPutOutcome.EXPIRED,
      PbPutOutcome.SUPERSEDED,
    ];
    const context = await contextFor(client, 3, 0xa0);

    const result = await client.putVerticesWithReceipt(
      [
        { key: "put:live", value: "value" },
        { key: "put:expired", value: "value" },
        { key: "put:superseded", value: "value" },
      ],
      context,
    );
    expect(result.results).toEqual([
      {
        key: "put:live",
        operationId: context.operationIds[0],
        outcome: "appliedAndLive",
      },
      {
        key: "put:expired",
        operationId: context.operationIds[1],
        outcome: "expired",
      },
      {
        key: "put:superseded",
        operationId: context.operationIds[2],
        outcome: "superseded",
      },
    ]);
    expect(
      (await client.getReceiptStatuses(context.operationIds)).map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
    ).toEqual([
      { kind: "putVertex", outcome: "appliedAndLive" },
      { kind: "putVertex", outcome: "expired" },
      { kind: "putVertex", outcome: "superseded" },
    ]);

    fake.putOutcomesOverride = undefined;
    fake.addVertex("put:conditional");
    const singularContext = await contextFor(client, 1, 0xa4);
    const singular = await client.putVertexIfAbsentWithReceipt(
      { key: "put:conditional", value: "replacement" },
      singularContext,
    );
    expect(singular).toEqual({
      key: "put:conditional",
      operationId: singularContext.operationIds[0],
      outcome: "conditionNotMet",
    });
    const conditionalStatus = await client.getReceiptStatus(singularContext.operationIds[0]!);
    expect(
      conditionalStatus.state === "confirmed"
        ? conditionalStatus.receipt.originalResult
        : conditionalStatus.state,
    ).toEqual({ kind: "putVertex", outcome: "conditionNotMet" });
    expect(fake.putCalls).toBe(2);
  });

  test("freezes relative TTL and mutable values for exact response-loss replay", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0xb0);
    const value = new Uint8Array([1, 2, 3]);
    const input = { key: "put:lost", value, ttlSeconds: 60 };
    fake.dropNextPutResponse = true;

    let uncertain: ReceiptMutationUncertainError | undefined;
    try {
      await client.putVertexWithReceipt(input, context);
    } catch (error) {
      if (error instanceof ReceiptMutationUncertainError) uncertain = error;
    }
    if (!uncertain || uncertain.mutation.kind !== "putVertex") {
      throw new Error("expected uncertain Vertex Put");
    }
    const firstWire = fake.lastPutVertexJson[0]!;
    expect(JSON.parse(firstWire)).toMatchObject({
      key: "put:lost",
      bytes: "AQID",
      expiration: new Date(Number(fake.capability.serverNowUnixMs) + 60_000).toISOString(),
    });
    expect(uncertain.mutation).toMatchObject({
      kind: "putVertex",
      ifAbsent: false,
      inputs: [{ key: "put:lost", ttlSeconds: 60 }],
    });
    expect([...(uncertain.mutation.inputs[0]!.value as Uint8Array)]).toEqual([1, 2, 3]);

    value[0] = 9;
    input.ttlSeconds = 90;
    const replay = await client.putVerticesWithReceipt(
      uncertain.mutation.inputs,
      uncertain.context,
    );
    expect(replay.results[0]!.outcome).toBe("appliedAndLive");
    expect(fake.lastPutVertexJson[0]).toBe(firstWire);
    expect(fake.mutationCount).toBe(1);
  });

  test("keeps intent conflicts definite and malformed responses uncertain", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0xc0);

    await client.putVertexWithReceipt({ key: "put:conflict", value: "first" }, context);
    await expect(
      client.putVertexWithReceipt({ key: "put:conflict", value: "second" }, context),
    ).rejects.toBeInstanceOf(InvalidArgumentError);
    expect(fake.mutationCount).toBe(1);

    const malformedContext = await contextFor(client, 1, 0xc4);
    fake.malformedPutResponse = true;
    await expect(
      client.putVertexWithReceipt({ key: "put:malformed", value: "value" }, malformedContext),
    ).rejects.toBeInstanceOf(ReceiptMutationUncertainError);
  });
});

describe("receipt Vertex Delete", () => {
  test("preserves exact plural true/false results and singular response-loss replay", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    fake.addVertex("delete:present");
    const context = await contextFor(client, 2, 0xd0);

    const result = await client.deleteVerticesWithReceipt(
      ["delete:present", "delete:absent"],
      context,
    );
    expect(result.deleted).toBe(1);
    expect(result.results).toEqual([
      {
        key: "delete:present",
        operationId: context.operationIds[0],
        existed: true,
      },
      {
        key: "delete:absent",
        operationId: context.operationIds[1],
        existed: false,
      },
    ]);

    fake.addVertex("delete:lost");
    const lostContext = await contextFor(client, 1, 0xd4);
    fake.dropNextVertexDeleteResponse = true;
    let uncertain: ReceiptMutationUncertainError | undefined;
    try {
      await client.deleteVertexWithReceipt("delete:lost", lostContext);
    } catch (error) {
      if (error instanceof ReceiptMutationUncertainError) uncertain = error;
    }
    expect(uncertain?.mutation).toEqual({
      kind: "deleteVertex",
      keys: ["delete:lost"],
    });
    if (!uncertain || uncertain.mutation.kind !== "deleteVertex") {
      throw new Error("expected uncertain Vertex Delete");
    }
    const replay = await client.deleteVerticesWithReceipt(
      uncertain.mutation.keys,
      uncertain.context,
    );
    expect(replay.results[0]!.existed).toBe(true);
    expect(fake.hasVertex("delete:lost")).toBe(false);
    expect(fake.mutationCount).toBe(3);

    const malformedContext = await contextFor(client, 1, 0xd8);
    fake.malformedVertexDeleteResponse = true;
    await expect(
      client.deleteVertexWithReceipt("delete:malformed", malformedContext),
    ).rejects.toBeInstanceOf(ReceiptMutationUncertainError);
  });
});

describe("receipt Edge Add", () => {
  test("maps exact plural results and keeps singular zero as a plural facade", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    fake.addEdge("add", "shared", 1);
    const context = await contextFor(client, 2, 0xda);
    const firstContrib = filled(24, 0x31);
    const secondContrib = filled(24, 0x32);

    const plural = await client.addEdgesWithReceipt(
      [
        { tail: "add", head: "shared", weight: 2, contribId: firstContrib },
        { tail: "add", head: "shared", weight: 3, contribId: secondContrib },
      ],
      context,
    );
    expect(plural.written).toBe(2);
    expect(plural.results).toEqual([
      {
        tail: "add",
        head: "shared",
        operationId: context.operationIds[0],
        contribId: firstContrib,
        effectiveWeight: 3,
      },
      {
        tail: "add",
        head: "shared",
        operationId: context.operationIds[1],
        contribId: secondContrib,
        effectiveWeight: 6,
      },
    ]);

    const zeroContext = await contextFor(client, 1, 0xdc);
    const zeroContrib = filled(24, 0x33);
    const zero = await client.addEdgeWithReceipt(
      {
        tail: "add",
        head: "expired",
        weight: 7,
        expiration: new Date("2000-01-01T00:00:00.000Z"),
        contribId: zeroContrib,
      },
      zeroContext,
    );
    expect(zero).toEqual({
      tail: "add",
      head: "expired",
      operationId: zeroContext.operationIds[0],
      contribId: zeroContrib,
      effectiveWeight: 0,
    });
    const status = await client.getReceiptStatus(zeroContext.operationIds[0]!);
    expect(status.state === "confirmed" ? status.receipt.originalResult : status.state).toEqual({
      kind: "addEdge",
      effectiveWeight: 0,
    });
    expect(fake.addCalls).toBe(2);
  });

  test("preserves signed infinity when finite float32 accumulation overflows", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const maxFloat32 = 3.4028234663852886e38;
    fake.addEdge("overflow", "positive", maxFloat32);
    fake.addEdge("overflow", "negative", -maxFloat32);
    const context = await contextFor(client, 2, 0xdd);

    const added = await client.addEdgesWithReceipt(
      [
        {
          tail: "overflow",
          head: "positive",
          weight: maxFloat32,
          contribId: filled(24, 0x34),
        },
        {
          tail: "overflow",
          head: "negative",
          weight: -maxFloat32,
          contribId: filled(24, 0x35),
        },
      ],
      context,
    );
    expect(added.results.map((result) => result.effectiveWeight)).toEqual([
      Number.POSITIVE_INFINITY,
      Number.NEGATIVE_INFINITY,
    ]);

    const statuses = await client.getReceiptStatuses(context.operationIds);
    expect(
      statuses.map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
    ).toEqual([
      { kind: "addEdge", effectiveWeight: Number.POSITIVE_INFINITY },
      { kind: "addEdge", effectiveWeight: Number.NEGATIVE_INFINITY },
    ]);
  });

  test("rejects missing, mixed, zero, and wrong-sized contrib IDs before transport", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const singularContext = await contextFor(client, 1, 0xde);
    const pluralContext = await contextFor(client, 2, 0xe0);
    fake.capabilityCalls = 0;

    // @ts-expect-error Receipt Add requires an explicit contribId at compile time too.
    const missing: EdgeAddReceiptInput = { tail: "missing", head: "id", weight: 1 };
    await expect(client.addEdgeWithReceipt(missing, singularContext)).rejects.toBeInstanceOf(
      InvalidArgumentError,
    );

    const mixed: EdgeAddReceiptInput[] = [
      { tail: "mixed", head: "keyed", weight: 1, contribId: filled(24, 0x41) },
      // @ts-expect-error The second item intentionally exercises malformed runtime input.
      { tail: "mixed", head: "unkeyed", weight: 1 },
    ];
    await expect(client.addEdgesWithReceipt(mixed, pluralContext)).rejects.toBeInstanceOf(
      InvalidArgumentError,
    );
    await expect(
      client.addEdgeWithReceipt(
        { tail: "zero", head: "id", weight: 1, contribId: filled(24, 0) },
        singularContext,
      ),
    ).rejects.toThrow(/nonzero/);
    await expect(
      client.addEdgeWithReceipt(
        { tail: "wrong", head: "size", weight: 1, contribId: filled(23, 0x42) },
        singularContext,
      ),
    ).rejects.toThrow(/exactly 24 bytes/);

    expect(fake.capabilityCalls).toBe(0);
    expect(fake.addCalls).toBe(0);
    expect(fake.mutationCount).toBe(0);
  });

  test("reuses cloned inputs and contribution identity after committed response loss", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0xe2);
    const contribId = filled(24, 0x51);
    const input: EdgeAddReceiptInput = {
      tail: "add",
      head: "lost",
      weight: 2,
      ttlSeconds: 60,
      contribId,
    };
    fake.dropNextAddResponse = true;

    let uncertain: ReceiptMutationUncertainError | undefined;
    try {
      await client.addEdgeWithReceipt(input, context);
    } catch (error) {
      if (error instanceof ReceiptMutationUncertainError) uncertain = error;
    }
    if (!uncertain || uncertain.mutation.kind !== "addEdge") {
      throw new Error("expected uncertain Edge Add");
    }
    const firstWire = fake.lastAddEdgeJson[0]!;
    expect(JSON.parse(firstWire)).toMatchObject({
      tail: "add",
      head: "lost",
      weight: 2,
      expiration: new Date(Number(fake.capability.serverNowUnixMs) + 60_000).toISOString(),
    });
    expect(fake.lastAddContribIds[0]).toEqual(filled(24, 0x51));
    expect(uncertain.mutation.inputs[0]!.contribId).not.toBe(contribId);

    contribId[0] = 0x99;
    input.ttlSeconds = 90;
    fake.edges.delete("add\u0000lost");
    fake.edgeWeights.delete("add\u0000lost");
    const replay = await client.addEdgesWithReceipt(uncertain.mutation.inputs, uncertain.context);
    expect(replay.results[0]!.effectiveWeight).toBe(2);
    expect(fake.lastAddEdgeJson[0]).toBe(firstWire);
    expect(fake.lastAddContribIds[0]).toEqual(filled(24, 0x51));
    expect(fake.hasEdge("add", "lost")).toBe(false);
    expect(fake.mutationCount).toBe(1);
  });

  test("keeps intent conflicts definite and malformed responses uncertain", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());
    const context = await contextFor(client, 1, 0xe6);
    const input: EdgeAddReceiptInput = {
      tail: "add",
      head: "conflict",
      weight: 4,
      contribId: filled(24, 0x61),
    };

    await client.addEdgeWithReceipt(input, context);
    await expect(
      client.addEdgeWithReceipt({ ...input, weight: 9 }, context),
    ).rejects.toBeInstanceOf(InvalidArgumentError);
    expect(fake.edgeWeight("add", "conflict")).toBe(4);
    expect(fake.mutationCount).toBe(1);

    const malformedContext = await contextFor(client, 1, 0xe8);
    fake.malformedAddResponse = true;
    await expect(
      client.addEdgeWithReceipt(
        {
          tail: "add",
          head: "malformed",
          weight: 1,
          contribId: filled(24, 0x62),
        },
        malformedContext,
      ),
    ).rejects.toBeInstanceOf(ReceiptMutationUncertainError);

    fake.malformedAddResponse = false;
    fake.addWrittenOverride = 0;
    const malformedCountContext = await contextFor(client, 1, 0xe9);
    await expect(
      client.addEdgeWithReceipt(
        {
          tail: "add",
          head: "malformed-count",
          weight: 1,
          contribId: filled(24, 0x63),
        },
        malformedCountContext,
      ),
    ).rejects.toBeInstanceOf(ReceiptMutationUncertainError);
  });

  test("honors token rotation and rejects unsupported Add before transport", async () => {
    const fake = new ReceiptTransportFake();
    const oldClient = Lantern.withTransport(fake.transport("old-token"));
    const context = await contextFor(oldClient, 1, 0xea);
    const newClient = Lantern.withTransport(fake.transport("new-token"));

    await expect(
      newClient.addEdgeWithReceipt(
        {
          tail: "add",
          head: "token-rotation",
          weight: 1,
          contribId: filled(24, 0x71),
        },
        context,
      ),
    ).resolves.toMatchObject({ effectiveWeight: 1 });

    const unsupportedContext = await contextFor(newClient, 1, 0xec);
    fake.capability.supportedMutations = [
      PbReceiptMutationKind.PUT_VERTEX,
      PbReceiptMutationKind.DELETE_VERTEX,
      PbReceiptMutationKind.DELETE_EDGE,
    ];
    const callsBefore = fake.addCalls;
    let unsupported: unknown;
    try {
      await newClient.addEdgeWithReceipt(
        {
          tail: "add",
          head: "unsupported",
          weight: 1,
          contribId: filled(24, 0x72),
        },
        unsupportedContext,
      );
    } catch (error) {
      unsupported = error;
    }
    expect(unsupported).toBeInstanceOf(ReceiptReconciliationError);
    expect((unsupported as ReceiptReconciliationError).reason).toBe("mutationUnsupported");
    expect((unsupported as ReceiptReconciliationError).mutationKind).toBe("addEdge");
    expect(fake.addCalls).toBe(callsBefore);
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
    expect((caught as ReceiptMutationUncertainError).mutation).toEqual({
      kind: "deleteEdge",
      edges: [{ tail: "lost", head: "response" }],
    });
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
  test("decodes the exact original result for every supported mutation family", async () => {
    const fake = new ReceiptTransportFake();
    const client = Lantern.withTransport(fake.transport());

    const putContext = await contextFor(client, 1, 0xe4);
    await client.putVertexWithReceipt({ key: "status:put", value: "value" }, putContext);
    fake.addVertex("status:delete");
    const vertexDeleteContext = await contextFor(client, 1, 0xe8);
    await client.deleteVertexWithReceipt("status:delete", vertexDeleteContext);
    fake.addEdge("status", "edge");
    const edgeDeleteContext = await contextFor(client, 1, 0xec);
    await client.deleteEdgeWithReceipt("status", "edge", edgeDeleteContext);
    const edgeAddContext = await contextFor(client, 1, 0xee);
    await client.addEdgeWithReceipt(
      {
        tail: "status",
        head: "add",
        weight: 3,
        contribId: filled(24, 0x74),
      },
      edgeAddContext,
    );

    const statuses = await client.getReceiptStatuses([
      putContext.operationIds[0]!,
      vertexDeleteContext.operationIds[0]!,
      edgeDeleteContext.operationIds[0]!,
      edgeAddContext.operationIds[0]!,
    ]);
    expect(
      statuses.map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
    ).toEqual([
      { kind: "putVertex", outcome: "appliedAndLive" },
      { kind: "deleteVertex", existed: true },
      { kind: "deleteEdge", existed: true },
      { kind: "addEdge", effectiveWeight: 3 },
    ]);
  });

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

    const mutationsBeforeLookup = fake.mutationCount;
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
    expect(fake.mutationCount).toBe(mutationsBeforeLookup);
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

    fake.malformedStatusResponse = false;
    fake.putOutcomesOverride = [PbPutOutcome.UNSPECIFIED];
    const unspecifiedContext = await contextFor(client, 1, 0x15);
    await expect(
      client.putVertexWithReceipt(
        { key: "status:unspecified", value: "value" },
        unspecifiedContext,
      ),
    ).rejects.toBeInstanceOf(ReceiptMutationUncertainError);
    await expect(
      client.getReceiptStatus(unspecifiedContext.operationIds[0]!),
    ).rejects.toBeInstanceOf(LanternError);
  });
});
