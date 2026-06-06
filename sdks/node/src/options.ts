/**
 * Per-call and per-client options.
 */

import { Optimization } from "./values.js";

export interface IlluminateOptions {
  /** BFS depth limit (0 = server default; treated as "no expansion"). */
  step?: number;
  /** Per-hop fan-out, top-k neighbours kept at each frontier (0 = unlimited). */
  k?: number;
  /** Whether the server should TF-IDF re-weight edges before optimization. */
  tfidf?: boolean;
  /** Post-processing strategy. Defaults to UNSPECIFIED. */
  optimization?: Optimization;
}

export interface ScanOptions {
  /** Page size (0 = server default). */
  limit?: number;
  /** Opaque cursor returned by the previous scan; empty starts a fresh scan. */
  cursor?: Uint8Array;
}

export interface EdgeScanOptions extends ScanOptions {
  tailPrefix?: string;
  headPrefix?: string;
}

export interface DeleteByPrefixOptions {
  limit?: number;
  /** When true, the server reports the count that *would* be deleted. */
  dryRun?: boolean;
}

export interface ConnectOptions {
  /** Per-call deadline (ms) applied when the caller did not set one. */
  defaultTimeoutMs?: number;
  /** Auto-chunk size for putVertices / addEdges / putEdges / delete* (default 1000). */
  batchChunkSize?: number;
  /** Override the built-in retry + round_robin Connect service config. */
  serviceConfigJson?: string;
  /** Optional Connect user-agent string appended to the default. */
  userAgent?: string;
}

export const DEFAULT_BATCH_CHUNK_SIZE = 1000;
