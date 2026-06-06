/**
 * `lantern-sdk` — Bun-managed TypeScript SDK for the Lantern graph KVS.
 *
 * Two transports ship side-by-side during the Connect-only migration
 * (#335 / #340):
 *
 *   - `Lantern` (legacy, grpc-js) — production today.
 *   - `LanternConnect` (additive, Connect-Node) — targets the
 *     server's additive Connect listener (`LANTERN_CONNECT_PORT`).
 *
 * Both return the same `Vertex` / `Edge` / `Graph` value-object
 * shapes, so consumer code that only holds typed values is
 * transport-agnostic — only the constructor call changes.
 *
 * @packageDocumentation
 */

export { Lantern, defaultServiceConfig, hasEndpoints, staticTarget } from "./client.js";
export type { LanternConnectArgs } from "./client.js";
export { LanternConnect } from "./connect-client.js";
export type { LanternConnectArgs as LanternConnectClientArgs } from "./connect-client.js";
export {
  BatchError,
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
} from "./options.js";
export {
  Duration,
  Float32,
  Int32,
  Optimization,
  Uint32,
  Uint64,
  fromPbEdge,
  fromPbVertex,
  toPbVertex,
} from "./values.js";
export type {
  Edge,
  EdgeInput,
  Graph,
  Vertex,
  VertexInput,
  VertexKind,
  VertexValue,
} from "./values.js";
