import { describe, expect, test } from "bun:test";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import { toBinary } from "@bufbuild/protobuf";
import {
  BatchError,
  InvalidArgumentError,
  Lantern,
  ReceiptMutationUncertainError,
  mintReceiptOperationContext,
} from "../src/index.js";
import { createEdgeOutcomeFromWire } from "../src/create-outcome.js";
import {
  CreateEdgesRequestSchema,
  CreateEdgeOutcome as Outcome,
  LanternService,
  ReceiptMutationKind,
} from "../src/gen/graph/v1/graph_pb.js";
import type { EdgeInput } from "../src/values.js";

const input: EdgeInput = { tail: "users:1", head: "targets:1", weight: 1 };
function capability() {
  return {
    enabled: true,
    policy: {
      deploymentEpoch: new Uint8Array(16).fill(1),
      fingerprint: new Uint8Array(32).fill(2),
      retentionMs: 86400000n,
      maxEntries: 1000n,
      maxBytes: 1000000n,
    },
    endpoint: { nodeId: new Uint8Array(16).fill(3), generation: new Uint8Array(16).fill(4) },
    serverNowUnixMs: 1700000000000n,
    supportedMutations: [ReceiptMutationKind.CREATE_EDGE],
  };
}

describe("conditional Edge Create", () => {
  test("strict original outcomes carry no existing Edge value", () => {
    expect([1, 2, 3, 4].map((v) => createEdgeOutcomeFromWire(v))).toEqual([
      "createdAndLive",
      "edgeExists",
      "endpointNotLive",
      "expired",
    ]);
    for (const bad of [0, 99, -1]) expect(() => createEdgeOutcomeFromWire(bad)).toThrow();
  });
  test("prevalidates all sources before sending any chunk", async () => {
    let calls = 0;
    const client = Lantern.withTransport(
      createRouterTransport((router) =>
        router.service(LanternService, {
          createEdges() {
            calls++;
            return { outcomes: [Outcome.CREATED_AND_LIVE] };
          },
        }),
      ),
      { batchChunkSize: 1 },
    );
    for (const weight of [0, NaN, Infinity, 1e100, 1e-100])
      await expect(client.createEdges([input, { ...input, weight }])).rejects.toBeInstanceOf(
        InvalidArgumentError,
      );
    await expect(
      client.createEdge({ ...input, contribId: new Uint8Array(24).fill(1) }),
    ).rejects.toBeInstanceOf(InvalidArgumentError);
    expect(calls).toBe(0);
  });
  test("singular forwards to plural; partial prefix is preserved without retry", async () => {
    let calls = 0;
    const client = Lantern.withTransport(
      createRouterTransport((router) =>
        router.service(LanternService, {
          createEdges(request) {
            calls++;
            expect(request.receiptContext).toBeUndefined();
            if (calls === 3) throw new ConnectError("committed response lost", Code.Unavailable);
            return { outcomes: request.edges.map(() => Outcome.EDGE_EXISTS) };
          },
        }),
      ),
      { batchChunkSize: 1 },
    );
    expect(await client.createEdge(input)).toBe("edgeExists");
    try {
      await client.createEdges([input, input]);
      throw new Error("expected failure");
    } catch (error) {
      expect(error).toBeInstanceOf(BatchError);
      expect((error as BatchError).written).toBe(1);
    }
    expect(calls).toBe(3);
  });
  test("misaligned and unspecified outcomes fail closed", async () => {
    for (const outcomes of [
      [],
      [Outcome.UNSPECIFIED],
      [99],
      [Outcome.CREATED_AND_LIVE, Outcome.EXPIRED],
    ]) {
      let calls = 0;
      const client = Lantern.withTransport(
        createRouterTransport((router) =>
          router.service(LanternService, {
            createEdges() {
              calls++;
              return { outcomes };
            },
          }),
        ),
      );
      await expect(client.createEdge(input)).rejects.toBeInstanceOf(BatchError);
      expect(calls).toBe(1);
    }
  });
  test("receipt intent and TTL remain frozen across caller edits", async () => {
    const requests: Uint8Array[] = [];
    const inputs: EdgeInput[] = [{ ...input, ttlSeconds: 60 }];
    let preflights = 0;
    const client = Lantern.withTransport(
      createRouterTransport((router) =>
        router.service(LanternService, {
          getReceiptCapability() {
            preflights++;
            if (preflights > 1) inputs.length = 0;
            return capability();
          },
          createEdges(request) {
            requests.push(toBinary(CreateEdgesRequestSchema, request));
            expect(request.edges[0]!.expiration!.seconds).toBe(1700000060n);
            return { outcomes: [Outcome.CREATED_AND_LIVE] };
          },
        }),
      ),
    );
    const cap = await client.getReceiptCapability();
    if (!cap.enabled) throw new Error("enabled capability required");
    let entropy = 5;
    const context = mintReceiptOperationContext(cap, 1, (bytes) => bytes.fill(entropy++));
    expect(await client.createEdgesWithReceipt(inputs, context)).toEqual(["createdAndLive"]);
    inputs.push({ ...input, ttlSeconds: 60 });
    expect(await client.createEdgeWithReceipt(inputs[0]!, context)).toBe("createdAndLive");
    expect(requests[0]).toEqual(requests[1]);
  });
  test("malformed receipt response requires reconciliation", async () => {
    let calls = 0;
    const client = Lantern.withTransport(
      createRouterTransport((router) =>
        router.service(LanternService, {
          getReceiptCapability() {
            return capability();
          },
          createEdges() {
            calls++;
            return { outcomes: [Outcome.UNSPECIFIED] };
          },
        }),
      ),
    );
    const cap = await client.getReceiptCapability();
    if (!cap.enabled) throw new Error("enabled capability required");
    let entropy = 5;
    const context = mintReceiptOperationContext(cap, 1, (bytes) => bytes.fill(entropy++));
    await expect(client.createEdgeWithReceipt(input, context)).rejects.toBeInstanceOf(
      ReceiptMutationUncertainError,
    );
    expect(calls).toBe(1);
  });
});
