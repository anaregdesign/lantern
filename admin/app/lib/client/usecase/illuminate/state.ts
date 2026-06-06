import type {
  Algorithm,
  Edge,
  Objective,
  Vertex,
  Weighting,
} from "~/lib/client/infrastructure/api/illuminate";

/**
 * Knobs that the user can tweak from the toolbar. Persisted in state so
 * the reducer can decide when a control change should kick off a re-fetch.
 * Per #410 the three Illuminate axes (algorithm, objective, weighting)
 * are first-class controls; the legacy single "optimization" knob and
 * the standalone tfidf switch are gone.
 */
export interface IlluminateControls {
  step: number;
  k: number;
  algorithm: Algorithm;
  objective: Objective;
  weighting: Weighting;
}

/**
 * Defaults that match the server's behaviour when knobs are omitted, and
 * give a sensible first frame for the user. UNSPECIFIED on every axis
 * lets the server pick (raw subgraph, minimise direction, raw weighting).
 */
export const DEFAULT_ILLUMINATE_CONTROLS: IlluminateControls = {
  step: 2,
  k: 8,
  algorithm: "ALGORITHM_UNSPECIFIED",
  objective: "OBJECTIVE_UNSPECIFIED",
  weighting: "WEIGHTING_UNSPECIFIED",
};

export type IlluminateStatus = "idle" | "loading" | "ready" | "error";

/**
 * Subgraph returned by a single Illuminate call, normalised into the shape
 * the canvas/table need. We keep the seed alongside the graph so back-nav
 * through the seed history stack can restore a frame without re-fetching.
 */
export interface IlluminateFrame {
  seed: string;
  controls: IlluminateControls;
  vertices: Vertex[];
  edges: Edge[];
}

export interface IlluminateState {
  /** Active seed (already URL-decoded). Empty string ⇒ no seed prompt yet. */
  seed: string;
  /** Stack of seeds visited so far. The last entry is always === `seed`. */
  history: string[];
  /** Currently-pending knob values. */
  controls: IlluminateControls;
  /** Most-recent successful frame for the active seed, or null. */
  frame: IlluminateFrame | null;
  status: IlluminateStatus;
  error: string | null;
  /**
   * Bumped on every seed-or-controls change. Async handlers carry the
   * value they saw at dispatch time and discard their result if it has
   * moved on (matches the prefixEpoch pattern used by browse-vertices).
   */
  fetchEpoch: number;
}

export const INITIAL_ILLUMINATE_STATE: IlluminateState = {
  seed: "",
  history: [],
  controls: DEFAULT_ILLUMINATE_CONTROLS,
  frame: null,
  status: "idle",
  error: null,
  fetchEpoch: 0,
};
