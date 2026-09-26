import assert from "node:assert/strict";
import { randomUUID } from "node:crypto";
import test from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";

import {
  InvalidArgumentError,
  ReceiptMutationUncertainError,
  ReceiptReconciliationError,
  connect,
  mintReceiptOperationContext,
  parseReceiptOperationContext,
} from "lantern-sdk";

function requiredEnvironment(name) {
  const value = process.env[name];
  if (!value) throw new Error(`${name} is required`);
  return value;
}

const endpoint = requiredEnvironment("LANTERN_NODE_RECEIPT_ENDPOINT");
const otherEndpoint = requiredEnvironment("LANTERN_NODE_RECEIPT_OTHER_ENDPOINT");
const token = requiredEnvironment("LANTERN_NODE_RECEIPT_TOKEN");

function dropNextPutVerticesResponse() {
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

test("receipt Vertex Put/Delete and Edge Delete reconcile exact results over real Connect/h2c", async () => {
  const client = connect(endpoint, { token });
  const prefix = `node-receipt-${randomUUID()}`;
  try {
    const capability = await client.getReceiptCapability();
    assert.equal(capability.enabled, true, "receipt test endpoint is disabled");
    assert.deepEqual(capability.supportedMutations, ["putVertex", "deleteVertex", "deleteEdge"]);

    const putContext = mintReceiptOperationContext(capability, 2);
    const put = await client.putVerticesWithReceipt(
      [
        { key: `${prefix}:live`, value: "live" },
        {
          key: `${prefix}:expired`,
          value: "expired",
          expiration: new Date("2000-01-01T00:00:00.000Z"),
        },
      ],
      putContext,
    );
    assert.deepEqual(
      put.results.map((result) => result.outcome),
      ["appliedAndLive", "expired"],
    );
    const putStatuses = await client.getReceiptStatuses(putContext.operationIds);
    assert.deepEqual(
      putStatuses.map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
      [
        { kind: "putVertex", outcome: "appliedAndLive" },
        { kind: "putVertex", outcome: "expired" },
      ],
    );

    const conditionalContext = mintReceiptOperationContext(capability, 1);
    const conditional = await client.putVertexIfAbsentWithReceipt(
      { key: `${prefix}:live`, value: "replacement" },
      conditionalContext,
    );
    assert.equal(conditional.outcome, "conditionNotMet");

    const conflictContext = mintReceiptOperationContext(capability, 1);
    await client.putVertexWithReceipt(
      { key: `${prefix}:conflict`, value: "original" },
      conflictContext,
    );
    await assert.rejects(
      client.putVertexWithReceipt(
        { key: `${prefix}:conflict`, value: "different" },
        conflictContext,
      ),
      InvalidArgumentError,
    );

    const deleteContext = mintReceiptOperationContext(capability, 2);
    const deleted = await client.deleteVerticesWithReceipt(
      [`${prefix}:live`, `${prefix}:missing`],
      deleteContext,
    );
    assert.deepEqual(
      deleted.results.map((result) => result.existed),
      [true, false],
    );
    const deleteStatuses = await client.getReceiptStatuses(deleteContext.operationIds);
    assert.deepEqual(
      deleteStatuses.map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
      [
        { kind: "deleteVertex", existed: true },
        { kind: "deleteVertex", existed: false },
      ],
    );

    const presentEdge = {
      tail: `${prefix}:edge:tail`,
      head: `${prefix}:edge:present`,
    };
    const missingEdge = {
      tail: `${prefix}:edge:tail`,
      head: `${prefix}:edge:missing`,
    };
    await client.putEdge({ ...presentEdge, weight: 1 });
    const edgeDeleteContext = mintReceiptOperationContext(capability, 2);
    const edgeDeleted = await client.deleteEdgesWithReceipt(
      [presentEdge, missingEdge],
      edgeDeleteContext,
    );
    assert.deepEqual(edgeDeleted, {
      context: edgeDeleteContext,
      deleted: 1,
      results: [
        {
          ...presentEdge,
          operationId: edgeDeleteContext.operationIds[0],
          existed: true,
        },
        {
          ...missingEdge,
          operationId: edgeDeleteContext.operationIds[1],
          existed: false,
        },
      ],
    });
    const edgeDeleteStatuses = await client.getReceiptStatuses(edgeDeleteContext.operationIds);
    assert.deepEqual(
      edgeDeleteStatuses.map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
      [
        { kind: "deleteEdge", existed: true },
        { kind: "deleteEdge", existed: false },
      ],
    );

    const singularEdge = {
      tail: `${prefix}:edge:tail`,
      head: `${prefix}:edge:singular`,
    };
    await client.putEdge({ ...singularEdge, weight: 2 });
    const singularEdgeContext = mintReceiptOperationContext(capability, 1);
    const singularEdgeDeleted = await client.deleteEdgeWithReceipt(
      singularEdge.tail,
      singularEdge.head,
      singularEdgeContext,
    );
    assert.deepEqual(singularEdgeDeleted, {
      ...singularEdge,
      operationId: singularEdgeContext.operationIds[0],
      existed: true,
    });
    const singularEdgeStatus = await client.getReceiptStatus(singularEdgeContext.operationIds[0]);
    assert.deepEqual(
      singularEdgeStatus.state === "confirmed"
        ? singularEdgeStatus.receipt.originalResult
        : singularEdgeStatus.state,
      { kind: "deleteEdge", existed: true },
    );

    const protectedEdge = {
      tail: `${prefix}:edge:tail`,
      head: `${prefix}:edge:protected`,
    };
    await client.putEdge({ ...protectedEdge, weight: 3 });
    await assert.rejects(
      client.deleteEdgesWithReceipt([protectedEdge, missingEdge], edgeDeleteContext),
      InvalidArgumentError,
    );
    assert.equal((await client.getEdge(protectedEdge.tail, protectedEdge.head)).weight, 3);
  } finally {
    client.close();
  }
});

test("real h2c response loss replays the exact original conditional Put result", async () => {
  const lossy = connect(endpoint, {
    token,
    interceptors: [dropNextPutVerticesResponse()],
  });
  const key = `node-receipt-loss-${randomUUID()}`;
  const input = { key, value: "committed-once", ttlSeconds: 3600 };
  let persistedContext = "";
  let uncertain;
  try {
    const capability = await lossy.getReceiptCapability();
    assert.equal(capability.enabled, true, "receipt test endpoint is disabled");
    const context = mintReceiptOperationContext(capability, 1);
    persistedContext = JSON.stringify(context);
    try {
      await lossy.putVertexIfAbsentWithReceipt(input, context);
    } catch (error) {
      if (!(error instanceof ReceiptMutationUncertainError)) throw error;
      uncertain = error;
    }
  } finally {
    lossy.close();
  }
  assert.ok(uncertain instanceof ReceiptMutationUncertainError);
  assert.equal(uncertain.mutation.kind, "putVertex");
  assert.equal(uncertain.mutation.ifAbsent, true);

  const replay = connect(endpoint, { token });
  try {
    const restored = parseReceiptOperationContext(JSON.parse(persistedContext));
    const result = await replay.putVertexIfAbsentWithReceipt(input, restored);
    assert.equal(result.outcome, "appliedAndLive");
    const status = await replay.getReceiptStatus(restored.operationIds[0]);
    assert.deepEqual(status.state === "confirmed" ? status.receipt.originalResult : status.state, {
      kind: "putVertex",
      outcome: "appliedAndLive",
    });
    assert.equal((await replay.getVertex(key)).value, "committed-once");
  } finally {
    replay.close();
  }
});

test("receipt retry rejects a different real endpoint before mutation", async () => {
  const first = connect(endpoint, { token });
  const second = connect(otherEndpoint, { token });
  const key = `node-receipt-endpoint-${randomUUID()}`;
  try {
    await second.putVertex({ key, value: "must-survive" });
    const capability = await first.getReceiptCapability();
    assert.equal(capability.enabled, true, "receipt test endpoint is disabled");
    const context = mintReceiptOperationContext(capability, 1);

    let caught;
    try {
      await second.deleteVertexWithReceipt(key, context);
    } catch (error) {
      caught = error;
    }
    assert.ok(caught instanceof ReceiptReconciliationError);
    assert.equal(caught.reason, "nodeChanged");
    assert.equal((await second.getVertex(key)).value, "must-survive");
  } finally {
    first.close();
    second.close();
  }
});
