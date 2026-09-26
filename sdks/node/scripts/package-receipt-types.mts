import * as node from "lantern-sdk";
import * as web from "lantern-sdk/web";

async function _checkNodeReceiptTypes(
  client: node.Lantern,
  capability: node.EnabledReceiptCapability,
  context: node.ReceiptOperationContext,
  vertex: node.VertexInput,
  add: node.EdgeAddReceiptInput,
  ref: node.ReceiptEdgeRef,
): Promise<void> {
  const _minted: node.ReceiptOperationContext = node.mintReceiptOperationContext(capability, 1);
  const _parsed: node.ReceiptOperationContext = node.parseReceiptOperationContext(context);
  const _group: node.GroupID = node.parseGroupID(context.groupId);
  const operation: node.OperationID = node.parseOperationID(context.operationIds[0]!);
  const _capability: node.ReceiptCapability = await client.getReceiptCapability();
  const _statuses: readonly node.ReceiptStatus[] = await client.getReceiptStatuses([operation]);
  const status: node.ReceiptStatus = await client.getReceiptStatus(operation);
  if (status.state === "confirmed") {
    const _original: node.ReceiptOriginalResult = status.receipt.originalResult;
  }
  const _put: node.VertexPutReceiptBatchResult = await client.putVerticesWithReceipt(
    [vertex],
    context,
  );
  const _putOne: node.VertexPutReceiptResult = await client.putVertexWithReceipt(vertex, context);
  const _conditional: node.VertexPutReceiptBatchResult =
    await client.putVerticesIfAbsentWithReceipt([vertex], context);
  const _conditionalOne: node.VertexPutReceiptResult = await client.putVertexIfAbsentWithReceipt(
    vertex,
    context,
  );
  const _deleted: node.VertexDeleteReceiptBatchResult = await client.deleteVerticesWithReceipt(
    [vertex.key],
    context,
  );
  const _deletedOne: node.VertexDeleteReceiptResult = await client.deleteVertexWithReceipt(
    vertex.key,
    context,
  );
  const _edgeDeleted: node.EdgeDeleteReceiptBatchResult = await client.deleteEdgesWithReceipt(
    [ref],
    context,
  );
  const _edgeDeletedOne: node.EdgeDeleteReceiptResult = await client.deleteEdgeWithReceipt(
    ref.tail,
    ref.head,
    context,
  );
  const _added: node.EdgeAddReceiptBatchResult = await client.addEdgesWithReceipt([add], context);
  const _addedOne: node.EdgeAddReceiptResult = await client.addEdgeWithReceipt(add, context);
  const _uncertain: typeof node.ReceiptMutationUncertainError = node.ReceiptMutationUncertainError;
  const _reconciliation: typeof node.ReceiptReconciliationError = node.ReceiptReconciliationError;
  const _contribId: node.EdgeAddReceiptInput["contribId"] = new Uint8Array(node.CONTRIB_ID_BYTES);
}

async function _checkWebReceiptTypes(
  client: web.Lantern,
  capability: web.EnabledReceiptCapability,
  context: web.ReceiptOperationContext,
  vertex: web.VertexInput,
  add: web.EdgeAddReceiptInput,
  ref: web.ReceiptEdgeRef,
): Promise<void> {
  const _minted: web.ReceiptOperationContext = web.mintReceiptOperationContext(capability, 1);
  const _parsed: web.ReceiptOperationContext = web.parseReceiptOperationContext(context);
  const _group: web.GroupID = web.parseGroupID(context.groupId);
  const operation: web.OperationID = web.parseOperationID(context.operationIds[0]!);
  const _capability: web.ReceiptCapability = await client.getReceiptCapability();
  const _statuses: readonly web.ReceiptStatus[] = await client.getReceiptStatuses([operation]);
  const status: web.ReceiptStatus = await client.getReceiptStatus(operation);
  if (status.state === "confirmed") {
    const _original: web.ReceiptOriginalResult = status.receipt.originalResult;
  }
  const _put: web.VertexPutReceiptBatchResult = await client.putVerticesWithReceipt(
    [vertex],
    context,
  );
  const _putOne: web.VertexPutReceiptResult = await client.putVertexWithReceipt(vertex, context);
  const _conditional: web.VertexPutReceiptBatchResult = await client.putVerticesIfAbsentWithReceipt(
    [vertex],
    context,
  );
  const _conditionalOne: web.VertexPutReceiptResult = await client.putVertexIfAbsentWithReceipt(
    vertex,
    context,
  );
  const _deleted: web.VertexDeleteReceiptBatchResult = await client.deleteVerticesWithReceipt(
    [vertex.key],
    context,
  );
  const _deletedOne: web.VertexDeleteReceiptResult = await client.deleteVertexWithReceipt(
    vertex.key,
    context,
  );
  const _edgeDeleted: web.EdgeDeleteReceiptBatchResult = await client.deleteEdgesWithReceipt(
    [ref],
    context,
  );
  const _edgeDeletedOne: web.EdgeDeleteReceiptResult = await client.deleteEdgeWithReceipt(
    ref.tail,
    ref.head,
    context,
  );
  const _added: web.EdgeAddReceiptBatchResult = await client.addEdgesWithReceipt([add], context);
  const _addedOne: web.EdgeAddReceiptResult = await client.addEdgeWithReceipt(add, context);
  const _uncertain: typeof web.ReceiptMutationUncertainError = web.ReceiptMutationUncertainError;
  const _reconciliation: typeof web.ReceiptReconciliationError = web.ReceiptReconciliationError;
  const _contribId: web.EdgeAddReceiptInput["contribId"] = new Uint8Array(web.CONTRIB_ID_BYTES);
}
