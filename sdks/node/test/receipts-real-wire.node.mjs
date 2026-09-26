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

function dropNextAddEdgesResponse() {
  let armed = true;
  return (next) => async (request) => {
    const response = await next(request);
    if (armed && request.method.name === "AddEdges") {
      armed = false;
      throw new ConnectError("injected post-commit response loss", Code.Unavailable);
    }
    return response;
  };
}

test("all receipt mutation families reconcile exact results over real Connect/h2c", async () => {
  const client = connect(endpoint, { token });
  const prefix = `node-receipt-${randomUUID()}`;
  try {
    const capability = await client.getReceiptCapability();
    assert.equal(capability.enabled, true, "receipt test endpoint is disabled");
    assert.deepEqual(capability.supportedMutations, [
      "putVertex",
      "deleteVertex",
      "deleteEdge",
      "addEdge",
    ]);

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

    const addEdge = {
      tail: `${prefix}:add:tail`,
      head: `${prefix}:add:head`,
    };
    const addContribIds = [new Uint8Array(24).fill(0x31), new Uint8Array(24).fill(0x32)];
    const addContext = mintReceiptOperationContext(capability, 2);
    const added = await client.addEdgesWithReceipt(
      [
        { ...addEdge, weight: 2, contribId: addContribIds[0] },
        { ...addEdge, weight: 3, contribId: addContribIds[1] },
      ],
      addContext,
    );
    assert.equal(added.written, 2);
    assert.deepEqual(
      added.results.map((result) => result.effectiveWeight),
      [2, 5],
    );
    assert.deepEqual(
      added.results.map((result) => result.contribId),
      addContribIds,
    );
    const addStatuses = await client.getReceiptStatuses(addContext.operationIds);
    assert.deepEqual(
      addStatuses.map((status) =>
        status.state === "confirmed" ? status.receipt.originalResult : status.state,
      ),
      [
        { kind: "addEdge", effectiveWeight: 2 },
        { kind: "addEdge", effectiveWeight: 5 },
      ],
    );
    await assert.rejects(
      client.addEdgesWithReceipt(
        [
          { ...addEdge, weight: 2, contribId: addContribIds[0] },
          { ...addEdge, weight: 4, contribId: addContribIds[1] },
        ],
        addContext,
      ),
      InvalidArgumentError,
    );
    assert.equal((await client.getEdge(addEdge.tail, addEdge.head)).weight, 5);

    const zeroContext = mintReceiptOperationContext(capability, 1);
    const zero = await client.addEdgeWithReceipt(
      {
        tail: `${prefix}:add:expired`,
        head: "edge",
        weight: 11,
        expiration: new Date("2000-01-01T00:00:00.000Z"),
        contribId: new Uint8Array(24).fill(0x33),
      },
      zeroContext,
    );
    assert.equal(zero.effectiveWeight, 0);
    const zeroStatus = await client.getReceiptStatus(zeroContext.operationIds[0]);
    assert.deepEqual(
      zeroStatus.state === "confirmed" ? zeroStatus.receipt.originalResult : zeroStatus.state,
      { kind: "addEdge", effectiveWeight: 0 },
    );
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

test("real h2c response loss replays Add proof without reapplying after Delete", async () => {
  const lossy = connect(endpoint, {
    token,
    interceptors: [dropNextAddEdgesResponse()],
  });
  const edge = {
    tail: `node-receipt-add-loss-${randomUUID()}`,
    head: "edge",
  };
  const input = {
    ...edge,
    weight: 4,
    ttlSeconds: 3600,
    contribId: new Uint8Array(24).fill(0x41),
  };
  let persistedContext = "";
  let uncertain;
  try {
    const capability = await lossy.getReceiptCapability();
    assert.equal(capability.enabled, true, "receipt test endpoint is disabled");
    const context = mintReceiptOperationContext(capability, 1);
    persistedContext = JSON.stringify(context);
    try {
      await lossy.addEdgeWithReceipt(input, context);
    } catch (error) {
      if (!(error instanceof ReceiptMutationUncertainError)) throw error;
      uncertain = error;
    }
  } finally {
    lossy.close();
  }
  assert.ok(uncertain instanceof ReceiptMutationUncertainError);
  assert.equal(uncertain.mutation.kind, "addEdge");
  assert.deepEqual(uncertain.mutation.inputs[0].contribId, input.contribId);

  const replay = connect(endpoint, { token });
  try {
    assert.equal((await replay.getEdge(edge.tail, edge.head)).weight, 4);
    assert.equal(await replay.deleteEdge(edge.tail, edge.head), true);

    const restored = parseReceiptOperationContext(JSON.parse(persistedContext));
    const status = await replay.getReceiptStatus(restored.operationIds[0]);
    assert.deepEqual(status.state === "confirmed" ? status.receipt.originalResult : status.state, {
      kind: "addEdge",
      effectiveWeight: 4,
    });
    const result = await replay.addEdgeWithReceipt(input, restored);
    assert.equal(result.effectiveWeight, 4);
    await assert.rejects(replay.getEdge(edge.tail, edge.head), { name: "NotFoundError" });
  } finally {
    replay.close();
  }
});

test("receipt retry rejects a different real endpoint before mutation", async () => {
  const first = connect(endpoint, { token });
  const second = connect(otherEndpoint, { token });
  const edge = {
    tail: `node-receipt-endpoint-${randomUUID()}`,
    head: "edge",
  };
  try {
    const capability = await first.getReceiptCapability();
    assert.equal(capability.enabled, true, "receipt test endpoint is disabled");
    const context = mintReceiptOperationContext(capability, 1);

    let caught;
    try {
      await second.addEdgeWithReceipt(
        {
          ...edge,
          weight: 1,
          contribId: new Uint8Array(24).fill(0x51),
        },
        context,
      );
    } catch (error) {
      caught = error;
    }
    assert.ok(caught instanceof ReceiptReconciliationError);
    assert.equal(caught.reason, "nodeChanged");
    await assert.rejects(second.getEdge(edge.tail, edge.head), { name: "NotFoundError" });
  } finally {
    first.close();
    second.close();
  }
});
