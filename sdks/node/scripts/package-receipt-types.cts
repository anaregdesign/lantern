import { CONTRIB_ID_BYTES as NODE_CONTRIB_ID_BYTES } from "lantern-sdk";
import { CONTRIB_ID_BYTES as WEB_CONTRIB_ID_BYTES } from "lantern-sdk/web";
import type * as node from "lantern-sdk";
import type * as web from "lantern-sdk/web";

function _checkCjsReceiptTypes(
  nodeClient: node.Lantern,
  webClient: web.Lantern,
  nodeInput: node.EdgeAddReceiptInput,
  webInput: web.EdgeAddReceiptInput,
  nodeContext: node.ReceiptOperationContext,
  webContext: web.ReceiptOperationContext,
): [Promise<node.EdgeAddReceiptResult>, Promise<web.EdgeAddReceiptResult>] {
  const _nodeContribId: node.EdgeAddReceiptInput["contribId"] = new Uint8Array(
    NODE_CONTRIB_ID_BYTES,
  );
  const _webContribId: web.EdgeAddReceiptInput["contribId"] = new Uint8Array(WEB_CONTRIB_ID_BYTES);
  return [
    nodeClient.addEdgeWithReceipt(nodeInput, nodeContext),
    webClient.addEdgeWithReceipt(webInput, webContext),
  ];
}
