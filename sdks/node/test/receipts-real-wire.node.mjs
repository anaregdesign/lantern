import assert from "node:assert/strict";
import { randomBytes, randomUUID } from "node:crypto";
import process from "node:process";
import test from "node:test";

import { Code, ConnectError } from "@connectrpc/connect";

import {
  CONTRIB_ID_BYTES,
  InvalidArgumentError,
  LanternError,
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
const nanFixtureEndpoint = requiredEnvironment("LANTERN_NODE_NAN_FIXTURE_ENDPOINT");
const token = requiredEnvironment("LANTERN_NODE_RECEIPT_TOKEN");

function randomContribId() {
  const contribId = new Uint8Array(randomBytes(CONTRIB_ID_BYTES));
  if (!contribId.some((value) => value !== 0)) contribId[0] = 1;
  return contribId;
}

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
    const addContribIds = [randomContribId(), randomContribId()];
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

    const invalidContribContext = mintReceiptOperationContext(capability, 1);
    await assert.rejects(
      client.addEdgeWithReceipt(
        { ...addEdge, weight: 1, contribId: new Uint8Array(CONTRIB_ID_BYTES - 1) },
        invalidContribContext,
      ),
      InvalidArgumentError,
    );
    assert.equal((await client.getEdge(addEdge.tail, addEdge.head)).weight, 5);
    assert.equal(
      (await client.getReceiptStatus(invalidContribContext.operationIds[0])).state,
      "notYetObserved",
    );

    const zeroContext = mintReceiptOperationContext(capability, 1);
    const zero = await client.addEdgeWithReceipt(
      {
        tail: `${prefix}:add:expired`,
        head: "edge",
        weight: 11,
        expiration: new Date("2000-01-01T00:00:00.000Z"),
        contribId: randomContribId(),
      },
      zeroContext,
    );
    assert.equal(zero.effectiveWeight, 0);
    const zeroStatus = await client.getReceiptStatus(zeroContext.operationIds[0]);
    assert.deepEqual(
      zeroStatus.state === "confirmed" ? zeroStatus.receipt.originalResult : zeroStatus.state,
      { kind: "addEdge", effectiveWeight: 0 },
    );

    const maxFloat32 = 3.4028234663852886e38;
    const overflowEdge = {
      tail: `${prefix}:add:overflow`,
      head: "edge",
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
    assert.equal(overflow.effectiveWeight, Number.POSITIVE_INFINITY);
    const overflowStatus = await client.getReceiptStatus(overflowContext.operationIds[0]);
    assert.deepEqual(
      overflowStatus.state === "confirmed"
        ? overflowStatus.receipt.originalResult
        : overflowStatus.state,
      { kind: "addEdge", effectiveWeight: Number.POSITIVE_INFINITY },
    );

    const negativeOverflowEdge = {
      tail: `${prefix}:add:negative-overflow`,
      head: "edge",
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
    assert.equal(negativeOverflow.effectiveWeight, Number.NEGATIVE_INFINITY);
    const negativeOverflowStatus = await client.getReceiptStatus(
      negativeOverflowContext.operationIds[0],
    );
    assert.deepEqual(
      negativeOverflowStatus.state === "confirmed"
        ? negativeOverflowStatus.receipt.originalResult
        : negativeOverflowStatus.state,
      { kind: "addEdge", effectiveWeight: Number.NEGATIVE_INFINITY },
    );

    const rejectedContext = mintReceiptOperationContext(capability, 1);
    for (const weight of [Number.NaN, Number.POSITIVE_INFINITY, Number.NEGATIVE_INFINITY]) {
      await assert.rejects(
        client.addEdgeWithReceipt(
          { ...addEdge, weight, contribId: randomContribId() },
          rejectedContext,
        ),
        InvalidArgumentError,
      );
    }
    assert.equal(
      (await client.getReceiptStatus(rejectedContext.operationIds[0])).state,
      "notYetObserved",
    );
  } finally {
    client.close();
  }
});

test("test-only NaN receipt fixture decodes original results over authenticated Node h2c", async () => {
  const client = connect(nanFixtureEndpoint, { token });
  const unauthenticated = connect(nanFixtureEndpoint);
  const malformed = connect(nanFixtureEndpoint, {
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
    await assert.rejects(
      unauthenticated.getReceiptCapability(),
      (error) =>
        error instanceof LanternError &&
        error.cause instanceof ConnectError &&
        error.cause.code === Code.Unauthenticated,
    );
    const capability = await client.getReceiptCapability();
    assert.equal(capability.enabled, true);
    assert.deepEqual(capability.supportedMutations, ["addEdge"]);

    const context = mintReceiptOperationContext(capability, 1);
    const input = {
      tail: `node-receipt-fixture-${randomUUID()}`,
      head: "edge",
      weight: 1,
      contribId: randomContribId(),
    };
    const added = await client.addEdgeWithReceipt(input, context);
    assert.equal(Number.isNaN(added.effectiveWeight), true);
    const status = await client.getReceiptStatus(context.operationIds[0]);
    if (status.state !== "confirmed" || status.receipt.originalResult.kind !== "addEdge") {
      throw new Error("fixture did not return a confirmed Edge Add result");
    }
    assert.equal(Number.isNaN(status.receipt.originalResult.effectiveWeight), true);
    assert.equal(
      Number.isNaN((await client.addEdgeWithReceipt(input, context)).effectiveWeight),
      true,
    );

    const rejectedContext = mintReceiptOperationContext(capability, 1);
    await assert.rejects(
      client.addEdgeWithReceipt(
        { ...input, weight: Number.NaN, contribId: randomContribId() },
        rejectedContext,
      ),
      InvalidArgumentError,
    );
    const absent = await client.getReceiptStatus(rejectedContext.operationIds[0]);
    assert.equal(absent.state, "notYetObserved");
    assert.equal(Object.hasOwn(absent, "receipt"), false);
    await assert.rejects(malformed.getReceiptStatus(context.operationIds[0]), LanternError);
  } finally {
    client.close();
    unauthenticated.close();
    malformed.close();
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
    contribId: randomContribId(),
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
          contribId: randomContribId(),
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
