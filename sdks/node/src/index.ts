/**
 * `lantern-sdk` — Bun-managed TypeScript SDK for the Lantern graph KVS.
 *
 * Built on Connect-Node v2. The server's primary listener accepts
 * Connect, gRPC, and gRPC-Web on the same h2c socket, so consumers
 * point this client at the same `host:6380` URL the legacy gRPC
 * clients used — just with an `http://` (or `https://`) scheme:
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
  Duration,
  Float32,
  Int32,
  Optimization,
  Uint32,
  Uint64,
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
