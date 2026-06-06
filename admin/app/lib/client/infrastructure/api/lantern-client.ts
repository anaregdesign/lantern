import { createPromiseClient, type PromiseClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";

import { LanternService } from "~/lib/api/gen/graph/v1/graph_connect";

export interface LanternClientOptions {
  baseUrl: string;
}

/**
 * Lantern Connect-Web client. Re-exports the generated PromiseClient so
 * adapter modules under `lib/client/infrastructure/api/` can stay narrow
 * (one function per RPC) while sharing the same transport.
 *
 * The protocol set here is Connect-flavoured JSON because the Lantern
 * server's primary listener mounts Connect + gRPC + gRPC-Web on the
 * same h2c socket. JSON is the lowest-friction choice for a browser
 * SPA: it round-trips cleanly through the browser fetch implementation,
 * is human-readable in DevTools, and matches the shape the legacy REST
 * adapters returned (so usecase value-objects do not need to change).
 */
export type LanternClient = PromiseClient<typeof LanternService>;

/**
 * Build a Connect-Web client bound to the supplied gateway base URL.
 * The base URL is normalised by trimming any trailing slash so paths
 * concatenated by the Connect transport produce
 * `${baseUrl}/graph.v1.LanternService/...` without doubled separators.
 *
 * Browsers default to fetch/Streams under the hood — no explicit
 * transport configuration is required for unary RPCs against the
 * Connect listener on the same origin (or any origin allowed by
 * `LANTERN_CORS_ALLOWED_ORIGINS` on the server).
 */
export function createLanternClient(opts: LanternClientOptions): LanternClient {
  const baseUrl = opts.baseUrl.replace(/\/$/, "");
  const transport = createConnectTransport({ baseUrl, useBinaryFormat: false });
  return createPromiseClient(LanternService, transport);
}
