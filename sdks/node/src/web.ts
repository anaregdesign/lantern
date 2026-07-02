/**
 * `lantern-sdk/web` — browser entrypoint built on `@connectrpc/connect-web`.
 *
 * Use this from any browser bundle (Vite/Webpack/Rollup) so the bundler
 * does NOT pull in `@connectrpc/connect-node`. The Lantern class itself
 * is identical to the Node entrypoint; only the underlying transport
 * differs.
 *
 *   ```ts
 *   import { connectWeb } from "lantern-sdk/web";
 *   const client = connectWeb("http://localhost:6380");
 *   try {
 *     await client.putVertex({ key: "hello", value: "world" });
 *     const v = await client.getVertex("hello");
 *   } finally {
 *     client.close();
 *   }
 *   ```
 *
 * @packageDocumentation
 */

import { Lantern, normaliseBaseUrl, withTokenInterceptors, type LanternArgs } from "./client.js";
import { makeWebTransport } from "./transport-web.js";

/**
 * Open a Connect-Web (browser fetch) Lantern client. The base URL
 * MUST include the scheme.
 *
 * Defaults:
 *   - HTTP/1.1 fetch transport (Connect protocol, JSON codec); set
 *     `args.transportOptions.useBinaryFormat = true` for binary.
 *   - Batch chunk size 1000.
 *   - No per-call timeout; pass `args.options.defaultTimeoutMs` to
 *     apply one.
 */
export function connectWeb(baseUrl: string, args: LanternArgs = {}): Lantern {
  const normalised = normaliseBaseUrl("connectWeb", baseUrl);
  return Lantern.withTransport(
    makeWebTransport(normalised, withTokenInterceptors(args), args.transportOptions),
    args.options,
  );
}

export { Lantern } from "./client.js";
export type { LanternArgs } from "./client.js";
export { ReplicationPeer_State } from "./gen/graph/v1/graph_pb.js";
export {
  BatchError,
  FailedPreconditionError,
  InvalidArgumentError,
  LanternError,
  NotFoundError,
  OverflowError,
  ResourceExhaustedError,
} from "./errors.js";
export type {
  ConnectOptions,
  DeleteByPrefixOptions,
  EdgeScanOptions,
  IlluminateOptions,
  ScanOptions,
  SearchOptions,
} from "./options.js";
export {
  Reduction,
  Duration,
  Float32,
  Int32,
  Objective,
  Uint32,
  Uint64,
  Weighting,
  fromEdgeJson,
  fromVertexJson,
  toEdgeJson,
  toVertexJson,
} from "./values.js";
export type {
  Edge,
  EdgeInput,
  Graph,
  SearchHit,
  Vertex,
  VertexInput,
  VertexKind,
  VertexValue,
} from "./values.js";
