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

/**
 * How a multi-word query's terms combine when choosing which vertices match
 * (see {@link SearchOptions.matchMode}): "any" (OR-union, the default), "all"
 * (AND), or "min-should" (at least {@link SearchOptions.minShouldMatch} terms).
 */
export type MatchMode = "any" | "all" | "min-should";

export interface SearchOptions {
  /** Caps the number of ranked hits returned (0 = server default; the server also enforces a hard max). */
  limit?: number;
  /** Restrict hits to vertices whose key carries this prefix (empty/omitted = no namespace scope). */
  prefix?: string;
  /** How the query's words combine: "any" (OR, default), "all" (AND), or "min-should". */
  matchMode?: MatchMode;
  /** With matchMode "min-should", the minimum distinct query words a hit must carry (0 = server default). */
  minShouldMatch?: number;
  /** Require the query's words to occur adjacently, in order — the highest-precision mode. */
  phrase?: boolean;
  /** Maximum edit distance (0, 1, or 2) for fuzzy term matching, so a typo still finds the term. */
  fuzziness?: number;
  /** Also match dictionary terms that extend a query word, so "lan" finds "lantern". */
  prefixTerms?: boolean;
}

export interface DeleteByPrefixOptions {
  limit?: number;
  /** When true, the server reports the count that *would* be deleted. */
  dryRun?: boolean;
}

/**
 * Options for {@link LanternClient.deleteEdgesByPrefix}. At least one of
 * `tailPrefix` / `headPrefix` must be non-empty — the server rejects a
 * both-empty request with `InvalidArgumentError` to prevent a whole-graph
 * edge wipe. An empty prefix on one axis means "any" on that axis.
 */
export interface DeleteEdgesByPrefixOptions {
  /** Match edges whose tail (source) key carries this prefix (empty = any tail). */
  tailPrefix?: string;
  /** Match edges whose head (destination) key carries this prefix (empty = any head). */
  headPrefix?: string;
  /** Caps how many matching edges are deleted in one call (0 = server default). */
  limit?: number;
  /** When true, the server reports the count that *would* be deleted without mutating. */
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
  /**
   * Opt in to automatic idempotency keys for `addEdge` / `addEdges` (#895).
   * When true, the client mints a 24-byte contrib ID per contribution from a
   * per-client random nonce and a monotonic per-call sequence, so a transport
   * retry re-sends the same bytes and the additive contribution is applied
   * exactly once (while it is live). A caller-supplied `EdgeInput.contribId`
   * always takes precedence over the automatic id. Default false (the legacy
   * additive path, where a retry double-counts weight).
   */
  idempotentAdds?: boolean;
}

export const DEFAULT_BATCH_CHUNK_SIZE = 1000;
