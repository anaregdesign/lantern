/**
 * Per-call and per-client options.
 */

import { Algorithm, Objective, Weighting } from "./values.js";

export interface IlluminateOptions {
  /** BFS depth limit (0 = server default; treated as "no expansion"). */
  step?: number;
  /** Per-hop fan-out, top-k neighbours kept at each frontier (0 = unlimited). */
  k?: number;
  /**
   * Post-traversal subgraph reduction. Defaults to UNSPECIFIED (raw
   * discovered subgraph). See #410 for the orthogonal-axes design.
   */
  algorithm?: Algorithm;
  /**
   * Reduction direction. Ignored when `algorithm === Algorithm.UNSPECIFIED`.
   * Server resolves UNSPECIFIED to MINIMIZE.
   */
  objective?: Objective;
  /**
   * Edge-weight transform applied BEFORE the BFS walk. Server resolves
   * UNSPECIFIED to RAW.
   */
  weighting?: Weighting;
  /**
   * Restrict the traversal frontier to vertices whose key has this prefix.
   * The seed is always retained as the anchor even if it does not match.
   * Empty/omitted = no filter. Applied server-side BEFORE per-hop top-k and
   * before any MST/SPT reduction (induced-subgraph semantics).
   */
  vertexPrefix?: string;
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
