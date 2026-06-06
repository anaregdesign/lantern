/**
 * `lantern-sdk` — Bun-managed TypeScript SDK for the Lantern graph KVS.
 *
 * Built on Connect-Node v2. The Lantern server's primary listener
 * speaks Connect / gRPC / gRPC-Web on the same h2c socket, so this
 * client points at the server's `host:6380` URL prefixed with an
 * `http://` (or `https://`) scheme:
 *
 *   ```ts
 *   import { Lantern } from "lantern-sdk";
 *   const client = Lantern.connect("http://localhost:6380");
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

export { Lantern } from "./client.js";
export type { LanternArgs } from "./client.js";
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
  Algorithm,
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
  Vertex,
  VertexInput,
  VertexKind,
  VertexValue,
} from "./values.js";
