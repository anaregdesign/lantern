import { expect, test } from "bun:test";

import {
  InvalidArgumentError,
  LanternError,
  connectWeb,
  mintReceiptOperationContext,
} from "../src/web.js";

const endpoint = process.env.LANTERN_NODE_RECEIPT_ENDPOINT;
const nanFixtureEndpoint = process.env.LANTERN_NODE_NAN_FIXTURE_ENDPOINT;
const token = process.env.LANTERN_NODE_RECEIPT_TOKEN;

function randomContribId(): Uint8Array {
  const contribId = crypto.getRandomValues(new Uint8Array(24));
  if (!contribId.some((value) => value !== 0)) contribId[0] = 1;
  return contribId;
}

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

if (endpoint && token) {
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

        const addContext = mintReceiptOperationContext(capability, 1);
        const added = await client.addEdgeWithReceipt(
          {
            tail: key,
            head: `${key}:head`,
            weight: 2,
            contribId: randomContribId(),
          },
          addContext,
        );
        expect(added.effectiveWeight).toBe(2);
        const addStatus = await client.getReceiptStatus(addContext.operationIds[0]!);
        expect(
          addStatus.state === "confirmed" ? addStatus.receipt.originalResult : addStatus.state,
        ).toEqual({ kind: "addEdge", effectiveWeight: 2 });

        const maxFloat32 = 3.4028234663852886e38;
        const overflowEdge = {
          tail: key,
          head: `${key}:overflow`,
        };
        await client.putEdge({ ...overflowEdge, weight: maxFloat32 });
        const overflowContext = mintReceiptOperationContext(capability, 1);
        const overflow = await client.addEdgeWithReceipt(
          {
            ...overflowEdge,
            weight: maxFloat32,
            contribId: randomContribId(),
          },
          overflowContext,
        );
        expect(overflow.effectiveWeight).toBe(Number.POSITIVE_INFINITY);
        const overflowStatus = await client.getReceiptStatus(overflowContext.operationIds[0]!);
        expect(
          overflowStatus.state === "confirmed"
            ? overflowStatus.receipt.originalResult
            : overflowStatus.state,
        ).toEqual({ kind: "addEdge", effectiveWeight: Number.POSITIVE_INFINITY });

        const negativeOverflowEdge = {
          tail: key,
          head: `${key}:negative-overflow`,
        };
        await client.putEdge({ ...negativeOverflowEdge, weight: -maxFloat32 });
        const negativeOverflowContext = mintReceiptOperationContext(capability, 1);
        const negativeOverflow = await client.addEdgeWithReceipt(
          {
            ...negativeOverflowEdge,
            weight: -maxFloat32,
            contribId: randomContribId(),
          },
          negativeOverflowContext,
        );
        expect(negativeOverflow.effectiveWeight).toBe(Number.NEGATIVE_INFINITY);
        const negativeOverflowStatus = await client.getReceiptStatus(
          negativeOverflowContext.operationIds[0]!,
        );
        expect(
          negativeOverflowStatus.state === "confirmed"
            ? negativeOverflowStatus.receipt.originalResult
            : negativeOverflowStatus.state,
        ).toEqual({ kind: "addEdge", effectiveWeight: Number.NEGATIVE_INFINITY });

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

  test("test-only NaN receipt fixture decodes original results over browser ProtoJSON", async () => {
    if (!nanFixtureEndpoint) {
      throw new Error("LANTERN_NODE_NAN_FIXTURE_ENDPOINT is required");
    }
    const observedResponses: string[] = [];
    const client = connectWeb(nanFixtureEndpoint, {
      token,
      transportOptions: {
        fetch: async (input: RequestInfo | URL, init?: RequestInit) => {
          const response = await fetch(input, init);
          const path = new URL(input instanceof Request ? input.url : String(input)).pathname;
          if (response.ok && (path.endsWith("/AddEdges") || path.endsWith("/GetReceiptStatuses"))) {
            observedResponses.push(await response.clone().text());
          }
          return response;
        },
      },
    });
    const unauthenticated = connectWeb(nanFixtureEndpoint);
    const malformed = connectWeb(nanFixtureEndpoint, {
      token,
      interceptors: [
        (next) => (request) => {
          if (request.method.name === "GetReceiptStatuses") {
            request.header.set("X-Fixture-Omit-Result", "true");
          }
          return next(request);
        },
      ],
    });
    try {
      await expect(unauthenticated.getReceiptCapability()).rejects.toBeInstanceOf(LanternError);
      const capability = await client.getReceiptCapability();
      if (!capability.enabled) throw new Error("fixture did not advertise receipt Add support");
      expect(capability.supportedMutations).toEqual(["addEdge"]);

      const context = mintReceiptOperationContext(capability, 1);
      const input = {
        tail: `node-receipt-json-fixture-${crypto.randomUUID()}`,
        head: "edge",
        weight: 1,
        contribId: randomContribId(),
      };
      const added = await client.addEdgeWithReceipt(input, context);
      expect(Number.isNaN(added.effectiveWeight)).toBe(true);
      const status = await client.getReceiptStatus(context.operationIds[0]!);
      if (status.state !== "confirmed" || status.receipt.originalResult.kind !== "addEdge") {
        throw new Error("fixture did not return a confirmed Edge Add result");
      }
      expect(Number.isNaN(status.receipt.originalResult.effectiveWeight)).toBe(true);
      const replay = await client.addEdgeWithReceipt(input, context);
      expect(Number.isNaN(replay.effectiveWeight)).toBe(true);
      expect(observedResponses.some((body) => body.includes('"effectiveWeights":["NaN"]'))).toBe(
        true,
      );
      expect(
        observedResponses.some((body) => body.includes('"addEdgeEffectiveWeight":"NaN"')),
      ).toBe(true);

      const rejectedContext = mintReceiptOperationContext(capability, 1);
      await expect(
        client.addEdgeWithReceipt(
          { ...input, weight: Number.NaN, contribId: randomContribId() },
          rejectedContext,
        ),
      ).rejects.toBeInstanceOf(InvalidArgumentError);
      const absent = await client.getReceiptStatus(rejectedContext.operationIds[0]!);
      expect(absent.state).toBe("notYetObserved");
      expect(absent).not.toHaveProperty("receipt");
      await expect(malformed.getReceiptStatus(context.operationIds[0]!)).rejects.toBeInstanceOf(
        LanternError,
      );
    } finally {
      client.close();
      unauthenticated.close();
      malformed.close();
    }
  });
}
