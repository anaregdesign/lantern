// transport-web.ts — Browser-only thin wrapper around
// @connectrpc/connect-web's createConnectTransport. Loaded from the
// `lantern-sdk/web` subpath export so admin / SPA consumers never pull
// in @connectrpc/connect-node. The Lantern class itself is
// transport-agnostic (see client.ts → Lantern.withTransport).
//
// connect-web speaks HTTP/1.1 over fetch by default — browsers cannot
// open h2c, and HTTPS-over-h2 is handled by fetch transparently. The
// `useBinaryFormat: false` default keeps the wire format JSON so
// DevTools shows readable payloads.

import { createConnectTransport, type ConnectTransportOptions } from "@connectrpc/connect-web";
import type { Transport, Interceptor } from "@connectrpc/connect";

/**
 * Build a Browser-flavoured Connect transport (HTTP/1.1 fetch by
 * default; binary format opt-in). Used by `Lantern.connectWeb()` from
 * the `lantern-sdk/web` subpath export.
 */
export function makeWebTransport(
  baseUrl: string,
  interceptors: Interceptor[] | undefined,
  overrides: Record<string, unknown> | undefined,
): Transport {
  return createConnectTransport({
    baseUrl,
    useBinaryFormat: false,
    interceptors,
    ...overrides,
  } as ConnectTransportOptions);
}
