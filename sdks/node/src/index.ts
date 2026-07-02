/**
 * `lantern-sdk` — Bun-managed TypeScript SDK for the Lantern graph KVS.
 *
 * Built on Connect-Node v2. The Lantern server's primary listener
 * speaks Connect / gRPC / gRPC-Web on the same h2c socket, so this
 * client points at the server's `host:6380` URL prefixed with an
 * `http://` (or `https://`) scheme:
 *
 *   ```ts
 *   import { connect } from "lantern-sdk";
 *   const client = connect("http://localhost:6380");
 *   try {
 *     await client.putVertex({ key: "hello", value: "world" });
 *     const v = await client.getVertex("hello");
 *   } finally {
 *     client.close();
 *   }
 *   ```
 *
 * For browser consumers, import from `lantern-sdk/web` instead — that
 * subpath ships `connectWeb()` and a bundle that excludes
 * `@connectrpc/connect-node`.
 *
 * @packageDocumentation
 */

import { Lantern, normaliseBaseUrl, withTokenInterceptors, type LanternArgs } from "./client.js";
import { makeNodeTransport } from "./transport-node.js";

/**
 * Open a Connect-Node-backed Lantern client. The base URL MUST
 * include the scheme — `http://` for h2c (the server default), or
 * `https://` for TLS.
 *
 * Defaults:
 *   - HTTP/2 transport (Connect protocol, JSON codec); set
 *     `args.transportOptions.useBinaryFormat = true` to flip to
 *     protobuf.
 *   - Batch chunk size 1000.
 *   - No per-call timeout; pass `args.options.defaultTimeoutMs` to
 *     apply one.
 */
export function connect(baseUrl: string, args: LanternArgs = {}): Lantern {
  const normalised = normaliseBaseUrl("connect", baseUrl);
  return Lantern.withTransport(
    makeNodeTransport(normalised, withTokenInterceptors(args), args.transportOptions),
    args.options,
  );
}

export { Lantern } from "./client.js";
export type { LanternArgs } from "./client.js";
export {
  createIncrementalSearch,
  DEFAULT_DEBOUNCE_MS,
  DEFAULT_MIN_QUERY_LENGTH,
} from "./incremental-search.js";
export type {
  IncrementalSearch,
  IncrementalSearchOptions,
  SearchFn,
  SearchUpdate,
} from "./incremental-search.js";
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
  BfsOptions,
  IlluminateOptions,
  LocalCommunityOptions,
  PprOptions,
  ScanOptions,
  SearchOptions,
} from "./options.js";
export {
  Duration,
  Float32,
  Int32,
  Objective,
  Reduction,
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
