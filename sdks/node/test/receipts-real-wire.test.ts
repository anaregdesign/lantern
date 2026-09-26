import { expect, test } from "bun:test";
import { Code, ConnectError, type Interceptor } from "@connectrpc/connect";

import {
  InvalidArgumentError,
  ReceiptMutationUncertainError,
  ReceiptReconciliationError,
  connect,
  mintReceiptOperationContext,
  parseReceiptOperationContext,
} from "../src/index.js";
import { connectWeb } from "../src/web.js";

const endpoint = process.env.LANTERN_NODE_RECEIPT_ENDPOINT;
const otherEndpoint = process.env.LANTERN_NODE_RECEIPT_OTHER_ENDPOINT;
const token = process.env.LANTERN_NODE_RECEIPT_TOKEN;

async function nextWithin<T>(
  iterator: AsyncIterator<T>,
  timeoutMs = 10_000,
): Promise<IteratorResult<T>> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      iterator.next(),
      new Promise<never>((_resolve, reject) => {
        timer = setTimeout(
          () => reject(new Error("timed out waiting for stream frame")),
          timeoutMs,
        );
      }),
    ]);
  } finally {
    if (timer) clearTimeout(timer);
  }
}

function dropNextPutVerticesResponse(): Interceptor {
  let armed = true;
  return (next) => async (request) => {
    const response = await next(request);
    if (armed && request.method.name === "PutVertices") {
      armed = false;
      throw new ConnectError("injected post-commit response loss", Code.Unavailable);
    }
    return response;
  };
}

if (endpoint && token) {
  test("receipt Vertex Put/Delete reconcile exact results over real Connect/h2c", async () => {
    const client = connect(endpoint, { token });
    const prefix = `node-receipt-${crypto.randomUUID()}`;
    try {
      const capability = await client.getReceiptCapability();
      if (!capability.enabled) throw new Error("receipt test endpoint is disabled");
      expect(capability.supportedMutations).toEqual(["putVertex", "deleteVertex", "deleteEdge"]);

      const putContext = mintReceiptOperationContext(capability, 2);
      const put = await client.putVerticesWithReceipt(
        [
          { key: `${prefix}:live`, value: "live" },
          { key: `${prefix}:expired`, value: "expired", expiration: new Date(1) },
        ],
        putContext,
      );
      expect(put.results.map((result) => result.outcome)).toEqual(["appliedAndLive", "expired"]);
      const putStatuses = await client.getReceiptStatuses(putContext.operationIds);
      expect(
        putStatuses.map((status) =>
          status.state === "confirmed" ? status.receipt.originalResult : status.state,
        ),
      ).toEqual([
        { kind: "putVertex", outcome: "appliedAndLive" },
        { kind: "putVertex", outcome: "expired" },
      ]);

      const conditionalContext = mintReceiptOperationContext(capability, 1);
      const conditional = await client.putVertexIfAbsentWithReceipt(
        { key: `${prefix}:live`, value: "replacement" },
        conditionalContext,
      );
      expect(conditional.outcome).toBe("conditionNotMet");

      const conflictContext = mintReceiptOperationContext(capability, 1);
      await client.putVertexWithReceipt(
        { key: `${prefix}:conflict`, value: "original" },
        conflictContext,
      );
      await expect(
        client.putVertexWithReceipt(
          { key: `${prefix}:conflict`, value: "different" },
          conflictContext,
        ),
      ).rejects.toBeInstanceOf(InvalidArgumentError);

      const deleteContext = mintReceiptOperationContext(capability, 2);
      const deleted = await client.deleteVerticesWithReceipt(
        [`${prefix}:live`, `${prefix}:missing`],
        deleteContext,
      );
      expect(deleted.results.map((result) => result.existed)).toEqual([true, false]);
      const deleteStatuses = await client.getReceiptStatuses(deleteContext.operationIds);
      expect(
        deleteStatuses.map((status) =>
          status.state === "confirmed" ? status.receipt.originalResult : status.state,
        ),
      ).toEqual([
        { kind: "deleteVertex", existed: true },
        { kind: "deleteVertex", existed: false },
      ]);
    } finally {
      client.close();
    }
  });

  test("real h2c response loss replays the exact original conditional Put result", async () => {
    const lossy = connect(endpoint, {
      token,
      interceptors: [dropNextPutVerticesResponse()],
    });
    const key = `node-receipt-loss-${crypto.randomUUID()}`;
    const input = { key, value: "committed-once", ttlSeconds: 3600 };
    let persistedContext = "";
    let uncertain: ReceiptMutationUncertainError | undefined;
    try {
      const capability = await lossy.getReceiptCapability();
      if (!capability.enabled) throw new Error("receipt test endpoint is disabled");
      const context = mintReceiptOperationContext(capability, 1);
      persistedContext = JSON.stringify(context);
      try {
        await lossy.putVertexIfAbsentWithReceipt(input, context);
      } catch (error) {
        if (error instanceof ReceiptMutationUncertainError) uncertain = error;
      }
    } finally {
      lossy.close();
    }
    if (!uncertain || uncertain.mutation.kind !== "putVertex") {
      throw new Error("expected uncertain Vertex Put");
    }
    expect(uncertain.mutation.ifAbsent).toBe(true);

    const replay = connect(endpoint, { token });
    try {
      const restored = parseReceiptOperationContext(JSON.parse(persistedContext));
      const result = await replay.putVertexIfAbsentWithReceipt(input, restored);
      expect(result.outcome).toBe("appliedAndLive");
      const status = await replay.getReceiptStatus(restored.operationIds[0]!);
      expect(status.state === "confirmed" ? status.receipt.originalResult : status.state).toEqual({
        kind: "putVertex",
        outcome: "appliedAndLive",
      });
      expect((await replay.getVertex(key)).value).toBe("committed-once");
    } finally {
      replay.close();
    }
  });

  test("browser JSON supports receipt unary calls and identity-only CDC", async () => {
    const client = connectWeb(endpoint, { token });
    const key = `node-receipt-web-${crypto.randomUUID()}`;
    try {
      const capability = await client.getReceiptCapability();
      if (!capability.enabled) throw new Error("receipt test endpoint is disabled");
      const stream = client.subscribeIdentity({ bootstrap: true })[Symbol.asyncIterator]();
      try {
        const first = await nextWithin(stream);
        expect(first.done).toBe(false);
        expect(first.value?.kind).toBe("checkpoint");

        const context = mintReceiptOperationContext(capability, 1);
        const put = await client.putVertexWithReceipt(
          { key, value: new Uint8Array([1, 2, 3]) },
          context,
        );
        expect(put.outcome).toBe("appliedAndLive");
        const status = await client.getReceiptStatus(context.operationIds[0]!);
        expect(status.state).toBe("confirmed");

        const conditionalContext = mintReceiptOperationContext(capability, 1);
        const conditional = await client.putVertexIfAbsentWithReceipt(
          { key, value: "must-not-apply" },
          conditionalContext,
        );
        expect(conditional.outcome).toBe("conditionNotMet");

        let receiptOnly = false;
        for (let count = 0; count < 10_000; count++) {
          const next = await nextWithin(stream);
          if (next.done) throw new Error("identity stream ended before receipt-only frame");
          if (next.value?.kind === "chunk" && next.value.operation === "receiptOnly") {
            expect(next.value.vertexKeys).toEqual([]);
            expect(next.value.edgeKeys).toEqual([]);
            expect(next.value.isLast).toBe(true);
            receiptOnly = true;
            break;
          }
        }
        expect(receiptOnly).toBe(true);
      } finally {
        await stream.return?.();
      }
    } finally {
      client.close();
    }
  });
}

if (endpoint && otherEndpoint && token) {
  test("receipt retry rejects a different real endpoint before mutation", async () => {
    const first = connect(endpoint, { token });
    const second = connect(otherEndpoint, { token });
    const key = `node-receipt-endpoint-${crypto.randomUUID()}`;
    try {
      await second.putVertex({ key, value: "must-survive" });
      const capability = await first.getReceiptCapability();
      if (!capability.enabled) throw new Error("receipt test endpoint is disabled");
      const context = mintReceiptOperationContext(capability, 1);

      let caught: unknown;
      try {
        await second.deleteVertexWithReceipt(key, context);
      } catch (error) {
        caught = error;
      }
      expect(caught).toBeInstanceOf(ReceiptReconciliationError);
      expect((caught as ReceiptReconciliationError).reason).toBe("nodeChanged");
      expect((await second.getVertex(key)).value).toBe("must-survive");
    } finally {
      first.close();
      second.close();
    }
  });
}
