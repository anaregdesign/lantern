import { expect, test } from "bun:test";
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { Code, ConnectError, createRouterTransport } from "@connectrpc/connect";
import {
  Lantern,
  MutationAcceptance,
  MutationProtocolError,
  BatchError,
  mutationReply,
  mintReceiptOperationContext,
} from "../src/index.js";
import { checkMutationAcceptance } from "../src/mutation-acceptance.js";
import {
  AddEdgesResponseSchema,
  DeleteEdgesResponseSchema,
  LanternService,
  MutationAcceptanceKind,
  MutationAcceptanceSchema,
  PutOutcome,
  ReceiptMutationKind,
  MutationReceiptState,
} from "../src/gen/graph/v1/graph_pb.js";

const acceptance = { kind: MutationAcceptanceKind.HANDLED_EFFECT_UNDISCLOSED };

test("receipt mutations acknowledge without uncertainty wrapping or repeat dispatch", async () => {
  let calls = 0;
  const client = Lantern.withTransport(
    createRouterTransport((router) =>
      router.service(LanternService, {
        getReceiptCapability() {
          return {
            enabled: true,
            policy: {
              deploymentEpoch: new Uint8Array(16).fill(1),
              fingerprint: new Uint8Array(32).fill(2),
              retentionMs: 86400000n,
              maxEntries: 1000n,
              maxBytes: 1000000n,
            },
            endpoint: {
              nodeId: new Uint8Array(16).fill(3),
              generation: new Uint8Array(16).fill(4),
            },
            serverNowUnixMs: 1700000000000n,
            supportedMutations: [
              ReceiptMutationKind.PUT_VERTEX,
              ReceiptMutationKind.DELETE_VERTEX,
              ReceiptMutationKind.DELETE_EDGE,
              ReceiptMutationKind.ADD_EDGE,
              ReceiptMutationKind.DELETE_EDGE_CONTRIBUTION,
              ReceiptMutationKind.CREATE_EDGE,
            ].sort((a, b) => a - b),
          };
        },
        addEdges() {
          calls++;
          return { acceptance };
        },
        createEdges() {
          calls++;
          return { acceptance };
        },
        deleteEdges() {
          calls++;
          return { acceptance };
        },
        deleteEdgeContributions() {
          calls++;
          return { acceptance };
        },
        getReceiptStatuses(request) {
          return {
            statuses: request.operationIds.map((operationId) => ({
              operationId,
              state: MutationReceiptState.EFFECT_UNDISCLOSED,
            })),
          };
        },
      }),
    ),
  );
  const capability = await client.getReceiptCapability();
  if (!capability.enabled) throw new Error("enabled capability required");
  let seed = 5;
  const context = mintReceiptOperationContext(capability, 1, (bytes) => bytes.fill(seed++));
  const input = { tail: "a", head: "b", weight: 1, contribId: new Uint8Array(24).fill(1) };
  expect(await mutationReply(() => client.addEdgesWithReceipt([input], context))).toEqual({
    kind: "acceptedUndisclosed",
  });
  expect(
    await mutationReply(() =>
      client.createEdgesWithReceipt([{ tail: "a", head: "b", weight: 1 }], context),
    ),
  ).toEqual({ kind: "acceptedUndisclosed" });
  expect(await mutationReply(() => client.deleteEdgesWithReceipt([input], context))).toEqual({
    kind: "acceptedUndisclosed",
  });
  expect(
    await mutationReply(() => client.deleteEdgeContributionsWithReceipt([input], context)),
  ).toEqual({ kind: "acceptedUndisclosed" });
  expect(calls).toBe(4);
  const status = await client.getReceiptStatus(context.operationIds[0]!);
  expect(status).toEqual({ state: "effectUndisclosed", operationId: context.operationIds[0] });
  expect(status).not.toHaveProperty("receipt");
});

test("acceptance is exclusive, typed, and has no RPC failure status", async () => {
  const response = create(AddEdgesResponseSchema, { acceptance });
  try {
    checkMutationAcceptance(response);
    throw new Error("missing acceptance");
  } catch (error) {
    expect(error).toBeInstanceOf(MutationAcceptance);
    expect(error).not.toBeInstanceOf(ConnectError);
    expect(error).not.toHaveProperty("code");
  }
  for (const malformed of [
    create(AddEdgesResponseSchema, { acceptance: { kind: 0 } }),
    create(AddEdgesResponseSchema, { acceptance: { kind: 99 } }),
    create(AddEdgesResponseSchema, { acceptance, written: 1 }),
    create(AddEdgesResponseSchema, { acceptance, effectiveWeights: [0] }),
    create(DeleteEdgesResponseSchema, { acceptance, existed: [false] }),
    fromBinary(
      AddEdgesResponseSchema,
      new Uint8Array([...toBinary(AddEdgesResponseSchema, response), 0xa0, 0x06, 1]),
    ),
    create(AddEdgesResponseSchema, {
      acceptance: fromBinary(MutationAcceptanceSchema, new Uint8Array([8, 1, 0xa0, 0x06, 1])),
    }),
  ])
    expect(() => checkMutationAcceptance(malformed)).toThrow(MutationProtocolError);
  expect(
    await mutationReply(async () => {
      throw new MutationAcceptance();
    }),
  ).toEqual({ kind: "acceptedUndisclosed" });
  expect(await mutationReply(async () => false)).toEqual({ kind: "knownEffect", effect: false });
  await expect(
    mutationReply(async () => {
      throw new BatchError(1, new MutationAcceptance());
    }),
  ).rejects.toBeInstanceOf(BatchError);
});

for (const operation of ["add", "put", "delete", "create", "contribution"] as const) {
  for (const failLast of [false, true]) {
    test(`${operation}: blind chunk handles each new input once; later failure=${failLast}`, async () => {
      const seen: string[] = [];
      const next = (head: string) => {
        seen.push(head);
        if (seen.length === 3 && failLast)
          throw new ConnectError("response lost", Code.Unavailable);
        return seen.length === 2;
      };
      const client = Lantern.withTransport(
        createRouterTransport((router) =>
          router.service(LanternService, {
            addEdges(request) {
              return next(request.edges[0]!.head)
                ? { acceptance }
                : { written: 1, effectiveWeights: [7] };
            },
            putEdges(request) {
              return next(request.edges[0]!.head)
                ? { acceptance }
                : { outcomes: [PutOutcome.APPLIED_AND_LIVE] };
            },
            deleteEdges(request) {
              return next(request.edges[0]!.head)
                ? { acceptance }
                : { deleted: 1, existed: [true] };
            },
            createEdges(request) {
              return next(request.edges[0]!.head) ? { acceptance } : { outcomes: [1] };
            },
            deleteEdgeContributions(request) {
              return next(request.contributions[0]!.head)
                ? { acceptance }
                : { deleted: 1, existed: [true] };
            },
          }),
        ),
        { batchChunkSize: 1 },
      );
      const inputs = ["b", "c", "d"].map((head) => ({
        tail: "a",
        head,
        weight: 1,
        contribId: new Uint8Array(24).fill(1),
      }));
      const run = () =>
        operation === "add"
          ? client.addEdges(inputs)
          : operation === "put"
            ? client.putEdges(inputs)
            : operation === "delete"
              ? client.deleteEdges(inputs)
              : operation === "create"
                ? client.createEdges(
                    inputs.map(({ tail, head, weight }) => ({ tail, head, weight })),
                  )
                : client.deleteEdgeContributions(inputs);
      if (failLast) {
        try {
          await mutationReply(run);
          throw new Error("expected partial failure");
        } catch (error) {
          expect(error).toBeInstanceOf(BatchError);
          expect((error as BatchError).written).toBe(2);
        }
      } else expect(await mutationReply(run)).toEqual({ kind: "acceptedUndisclosed" });
      expect(seen).toEqual(["b", "c", "d"]);
    });
  }
}

test("restore does not claim full acceptance until its source is exhausted", async () => {
  for (const failLast of [false, true]) {
    let calls = 0;
    const client = Lantern.withTransport(
      createRouterTransport((router) =>
        router.service(LanternService, {
          putEdges() {
            calls++;
            if (calls === 3 && failLast) throw new ConnectError("response lost", Code.Unavailable);
            return calls === 2 ? { acceptance } : { outcomes: [PutOutcome.APPLIED_AND_LIVE] };
          },
        }),
      ),
    );
    const source = ["b", "c", "d"].map((head) => ({
      kind: "edge" as const,
      edge: { tail: "a", head, weight: 1, expiration: null },
    }));
    if (failLast)
      await expect(
        mutationReply(() => client.restore(source, { chunkSize: 1 })),
      ).rejects.not.toBeInstanceOf(MutationAcceptance);
    else
      expect(await mutationReply(() => client.restore(source, { chunkSize: 1 }))).toEqual({
        kind: "acceptedUndisclosed",
      });
    expect(calls).toBe(3);
  }
});
