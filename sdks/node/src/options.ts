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
   * Illuminate algorithm. Defaults to UNSPECIFIED (raw discovered subgraph).
   * MST/SPT are post-traversal reductions; PERSONALIZED_PAGERANK (#801) is a
   * distinct traversal returning a relevance star tuned by `restartProb` /
   * `epsilon` and sized by `k`. See #410 for the orthogonal-axes design.
   */
  algorithm?: Algorithm;
  /**
   * Reduction direction. Ignored when `algorithm === Algorithm.UNSPECIFIED`
   * or `Algorithm.PERSONALIZED_PAGERANK`. Server resolves UNSPECIFIED to
   * MINIMIZE.
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
  /**
   * Personalized PageRank restart (teleport-to-seed) probability α. Higher α
   * keeps relevance tighter around the seed; lower α wanders farther. Only
   * meaningful with `algorithm === Algorithm.PERSONALIZED_PAGERANK`. Must lie
   * in (0,1) — 0/omitted or out-of-range falls back to the server default
   * (0.15).
   */
  restartProb?: number;
  /**
   * Personalized PageRank forward-push residual threshold ε. Smaller ε pushes
   * mass to more vertices (higher recall, more work); larger ε stops sooner.
   * Only meaningful with `algorithm === Algorithm.PERSONALIZED_PAGERANK`. Must
   * be positive — 0/omitted falls back to the server default (1e-4).
   */
  epsilon?: number;
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

export interface SearchOptions {
  /** Caps the number of ranked hits returned (0 = server default; the server also enforces a hard max). */
  limit?: number;
  /** Restrict hits to vertices whose key carries this prefix (empty/omitted = no namespace scope). */
  prefix?: string;
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
