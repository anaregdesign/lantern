/**
 * Per-call and per-client options.
 */

import { Objective, Reduction, Weighting } from "./values.js";

/**
 * BFS-family knobs (#846): the greedy per-hop top-k walk and its optional
 * post-traversal tree reduction.
 */
export interface BfsOptions {
  /** BFS depth limit (0 = server default; treated as "no expansion"). */
  step?: number;
  /**
   * Per-hop fan-out: top-k neighbours kept at each frontier (0 = unlimited).
   * Formerly the overloaded `k`.
   */
  fanOut?: number;
  /**
   * Direction for both the per-hop pruning and the reduction (#560).
   * Server resolves UNSPECIFIED to MAXIMIZE.
   */
  objective?: Objective;
  /**
   * Optional tree view of the discovered neighbourhood (MST / SPT rooted at
   * the seed). UNSPECIFIED = raw subgraph.
   */
  reduction?: Reduction;
}

/**
 * PPR-family knobs (#801/#846): seed-anchored Personalized PageRank via
 * forward-push, returning a relevance star. PPR is intrinsically a relevance
 * maximiser with no per-hop step semantics — which is why neither knob
 * exists here.
 */
export interface PprOptions {
  /**
   * Cap the star to the top-N vertices by mass (0 = every positive-mass
   * vertex). Formerly the overloaded `k`.
   */
  topN?: number;
  /**
   * Restart (teleport-to-seed) probability α. Higher α keeps relevance
   * tighter around the seed. Must lie in (0,1) — 0/omitted or out-of-range
   * falls back to the server default (0.15).
   */
  restartProb?: number;
  /**
   * Forward-push residual threshold ε. Smaller ε pushes mass to more
   * vertices (higher recall, more work). Must be positive — 0/omitted falls
   * back to the server default (1e-4).
   */
  epsilon?: number;
}

/**
 * Local-community-family knobs (#845): PageRank-Nibble — the PPR push
 * followed by a conductance sweep cut. Unlike the PPR relevance star the
 * response preserves structure: the induced subgraph on the selected
 * members with actual stored edge weights and expirations.
 */
export interface LocalCommunityOptions {
  /**
   * UPPER BOUND on community size — not an exact count; the sweep stops at
   * the conductance minimum, which may come earlier. 0 = unbounded.
   */
  maxSize?: number;
  /** Locality knob α, same semantics/defaults as PprOptions.restartProb. */
  restartProb?: number;
  /** Push accuracy ε, same semantics/defaults as PprOptions.epsilon. */
  epsilon?: number;
  /**
   * Optional tree VIEW of the community rooted at the seed. Members
   * unreachable from the seed within the community are returned as
   * isolated vertices (membership preserved). UNSPECIFIED = the full
   * induced subgraph.
   */
  reduction?: Reduction;
  /** Direction/cost mapping for `reduction` only; ignored without one. */
  objective?: Objective;
}

export interface IlluminateOptions {
  /**
   * Select the BFS traversal family with its knobs (#846). The family
   * options are mutually exclusive — supplying more than one is an
   * InvalidArgumentError. Omitting all runs BFS with server defaults (the
   * bare illuminate).
   */
  bfs?: BfsOptions;
  /** Select the Personalized PageRank family. Mutually exclusive. */
  ppr?: PprOptions;
  /** Select the local community extraction family (#845). Mutually exclusive. */
  community?: LocalCommunityOptions;
  /**
   * Edge-weight transform applied BEFORE the walk (any family). Server
   * resolves UNSPECIFIED to RAW.
   */
  weighting?: Weighting;
  /**
   * Restrict the traversal frontier to vertices whose key has this prefix.
   * The seed is always retained as the anchor even if it does not match.
   * Empty/omitted = no filter. Applied server-side BEFORE per-hop top-k and
   * before any reduction (induced-subgraph semantics).
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
