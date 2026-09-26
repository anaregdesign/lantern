import { expect, test } from "bun:test";

import { mintReceiptOperationContext } from "../src/index.js";
import { connectWeb } from "../src/web.js";

const endpoint = process.env.LANTERN_NODE_RECEIPT_ENDPOINT;
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
