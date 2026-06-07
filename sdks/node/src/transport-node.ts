// transport-node.ts — Node-only thin wrapper around
// @connectrpc/connect-node's createConnectTransport. The Lantern class
// itself is transport-agnostic; this module is loaded only from the
// default `lantern-sdk` entry (Node) and is INTENTIONALLY excluded from
// the `lantern-sdk/web` bundle so the browser entrypoint cannot pull in
// node-only deps.

import { createConnectTransport, type ConnectTransportOptions } from "@connectrpc/connect-node";
import type { Transport, Interceptor } from "@connectrpc/connect";

/**
 * Build a Node-flavoured Connect transport (h2c / HTTP/2 by default).
 * Used by `Lantern.connect()`.
 */
export function makeNodeTransport(
  baseUrl: string,
  interceptors: Interceptor[] | undefined,
  overrides: Record<string, unknown> | undefined,
): Transport {
  return createConnectTransport({
    baseUrl,
    httpVersion: "2",
    interceptors,
    ...overrides,
  } as ConnectTransportOptions);
}
