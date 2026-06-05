/**
 * `lantern-sdk` — Bun-managed TypeScript SDK for the Lantern graph KVS.
 *
 * @packageDocumentation
 */

export { Lantern, defaultServiceConfig, hasEndpoints, staticTarget } from "./client.js";
export type { LanternConnectArgs } from "./client.js";
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
