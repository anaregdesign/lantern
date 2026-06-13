import type {
  Algorithm,
  Edge,
  Objective,
  Vertex,
  Weighting,
} from "~/lib/client/infrastructure/api/illuminate";

/**
 * Knobs that the user can tweak from the toolbar. Persisted in state so
 * each click can snapshot the controls in effect at dispatch time.
 * Per #410 the three Illuminate axes (algorithm, objective, weighting)
 * are first-class controls; the legacy single "optimization" knob and
 * the standalone tfidf switch are gone.
 *
 * Per #466 D8 a `controls` change does NOT retroactively re-apply to
 * the existing accumulator; it only affects the next click. Each
 * `Expansion` snapshots the controls in force at its dispatch time so
 * the audit trail records "this sub-neighbourhood was the result of
 * k=8 SPT at 10:14".
 */
export interface IlluminateControls {
  step: number;
  k: number;
  algorithm: Algorithm;
  objective: Objective;
  weighting: Weighting;
  /**
   * Free-text vertex-key prefix filter (#606). Empty = no filter. The value
   * is matched against vertex keys verbatim (case-sensitive); the seed is
   * always retained as the traversal anchor.
   */
  vertexPrefix: string;
}

/**
 * Defaults that match the server's behaviour when knobs are omitted, and
 * give a sensible first frame for the user. UNSPECIFIED on every axis
 * lets the server pick (raw subgraph, maximise direction, raw weighting).
 * Per #560 the server resolves an unspecified objective to MAXIMIZE and
 * the objective steers the per-hop top-k pruning as well as the
 * post-traversal reduction.
 */
export const DEFAULT_ILLUMINATE_CONTROLS: IlluminateControls = {
  step: 2,
  k: 8,
  algorithm: "ALGORITHM_UNSPECIFIED",
  objective: "OBJECTIVE_UNSPECIFIED",
  weighting: "WEIGHTING_UNSPECIFIED",
  vertexPrefix: "",
};

export type IlluminateStatus = "idle" | "loading" | "ready" | "error";

/**
 * Vertex stored in the accumulator. The full payload is kept around so
 * the canvas tooltip, the IlluminateTable, and (post-#460/#461) the
 * hop tooltip and info Drawer can render typed values without re-fetching.
 *
 * Per #466 D3 a collision is resolved by latest-response-wins (we
 * overwrite `vertex` + `receivedAtMs`) and the originating expansion
 * is appended to `expansionIndexes` so per-vertex audit ("first seen
 * in expansion #N") stays cheap.
 */
export interface AccumVertex {
  vertex: Vertex;
  /** `performance.now()` snapshot when this vertex was last received. */
  receivedAtMs: number;
  /** Indexes into `IlluminateState.expansions[]` that produced or refreshed this vertex. */
  expansionIndexes: number[];
}

/**
 * Edge stored in the accumulator. Per #466 D4 the merge rule is
 * latest-response-wins on collision (the server already handles
 * additive edge semantics; client-side re-summing would double-count).
 */
export interface AccumEdge {
  edge: Edge;
  receivedAtMs: number;
  expansionIndexes: number[];
}

/**
 * Snapshot of one Illuminate call's worth of provenance. `expansions[0]`
 * is structurally privileged per #466 D5: it is the initial seed (mirrors
 * `IlluminateState.initialSeed`) and cannot be dropped via "Clear last"
 * style actions (D6 v2 — out of scope for v1).
 */
export interface Expansion {
  /** Stable, monotonically increasing id for React keys + the pending map. */
  id: number;
  /** Vertex key the user clicked (or initial seed key). */
  origin: string;
  /** Controls in effect when this expansion was dispatched. */
  controls: IlluminateControls;
  /** `performance.now()` snapshot at dispatch. */
  startedAtMs: number;
  /** Vertex keys returned by THIS expansion's Illuminate call. */
  vertexKeys: string[];
  /** Edge IDs (`${tail}→${head}`) returned by THIS expansion's call. */
  edgeIds: string[];
}

/**
 * Soft / hard caps for the accumulator (#466 D13). The reducer enforces
 * the hard cap by rejecting merges that would exceed it; the soft cap
 * is purely advisory and surfaces a `MessageBar` in the UI.
 */
export const ACCUMULATOR_SOFT_CAP = 500;
export const ACCUMULATOR_HARD_CAP = 2000;

export interface IlluminateState {
  /**
   * First click; mirrors the URL `?seed=` (#466 D10). `null` when the
   * canvas is empty and the SeedPrompt is showing. Changing this clears
   * the accumulator and re-runs the seed expansion.
   */
  initialSeed: string | null;
  /** All expansions in chronological order. `expansions[0]` is the seed expansion. */
  expansions: Expansion[];
  /** Merged vertex/edge view of every expansion received so far. */
  accumulator: {
    vertices: Map<string, AccumVertex>;
    edges: Map<string, AccumEdge>;
  };
  /** Knob values used for the NEXT click (per #466 D8). */
  controls: IlluminateControls;
  /** Number of in-flight expansion requests (drives the loading badge). */
  pendingCount: number;
  status: IlluminateStatus;
  error: string | null;
}

export const INITIAL_ILLUMINATE_STATE: IlluminateState = {
  initialSeed: null,
  expansions: [],
  accumulator: { vertices: new Map(), edges: new Map() },
  controls: DEFAULT_ILLUMINATE_CONTROLS,
  pendingCount: 0,
  status: "idle",
  error: null,
};

/** Stable encoding for an edge's accumulator key. Mirrors `selectGraphView`'s edge id. */
export function edgeIdOf(tail: string, head: string): string {
  return `${tail}→${head}`;
}
