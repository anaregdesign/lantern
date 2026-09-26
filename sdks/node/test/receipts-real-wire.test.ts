import { expect, test } from "bun:test";

import { mintReceiptOperationContext } from "../src/index.js";
import { connectWeb } from "../src/web.js";

const endpoint = process.env.LANTERN_NODE_RECEIPT_ENDPOINT;
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
