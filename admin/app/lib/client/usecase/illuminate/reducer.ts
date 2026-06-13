import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import {
  ACCUMULATOR_HARD_CAP,
  INITIAL_ILLUMINATE_STATE,
  edgeIdOf,
  type AccumEdge,
  type AccumVertex,
  type Expansion,
  type IlluminateControls,
  type IlluminateState,
} from "./state";

export type IlluminateAction =
  | { type: "INITIAL_SEED_CHANGED"; seed: string | null }
  | {
      type: "EXPANSION_REQUESTED";
      expansionId: number;
      origin: string;
      controls: IlluminateControls;
      startedAtMs: number;
    }
  | {
      type: "EXPANSION_RECEIVED";
      expansionId: number;
      origin: string;
      controls: IlluminateControls;
      startedAtMs: number;
      vertices: Vertex[];
      edges: Edge[];
      /** Wall-clock-ish ms used as the merge timestamp. */
      receivedAtMs: number;
    }
  | { type: "EXPANSION_FAILED"; expansionId: number; error: string }
  | { type: "CONTROLS_CHANGED"; controls: IlluminateControls }
  | { type: "CLEARED" }
  | { type: "RESET" };

/**
 * Pure reducer for the Illuminate view. Async I/O lives in `handlers.ts`;
 * this module is the single source of truth for state transitions and is
 * therefore unit-testable without touching the network.
 *
 * Per #466 the model is additive: each `EXPANSION_RECEIVED` merges the
 * server response into the accumulator (latest-response-wins per D3/D4)
 * and appends an `Expansion` to the audit trail. `CLEARED` empties
 * everything but preserves the `initialSeed` so the next URL render
 * can re-run the seed expansion; `RESET` also forgets the seed.
 */
export function illuminateReducer(
  state: IlluminateState,
  action: IlluminateAction,
): IlluminateState {
  switch (action.type) {
    case "INITIAL_SEED_CHANGED": {
      if (action.seed === state.initialSeed) {
        return state;
      }
      // A URL-level seed change wipes everything we know — the new seed
      // implies a fresh exploration. The hook is responsible for aborting
      // any in-flight controllers BEFORE dispatching this so we don't end
      // up merging a stale fetch into the cleared accumulator.
      return {
        ...INITIAL_ILLUMINATE_STATE,
        controls: state.controls,
        initialSeed: action.seed,
      };
    }
    case "EXPANSION_REQUESTED": {
      // Optimistic counter — the badge flips to "loading" immediately so
      // the user sees feedback even though the merge hasn't landed yet.
      return {
        ...state,
        pendingCount: state.pendingCount + 1,
        status: "loading",
        error: null,
      };
    }
    case "EXPANSION_RECEIVED": {
      const merged = mergeExpansion(state, action);
      if (merged === null) {
        // Hard cap exceeded — surface as an error and discount the
        // pending counter so the badge clears.
        const pendingCount = Math.max(0, state.pendingCount - 1);
        return {
          ...state,
          pendingCount,
          status: pendingCount > 0 ? "loading" : "error",
          error: `Accumulator hard cap of ${ACCUMULATOR_HARD_CAP} vertices reached — click Clear to start over.`,
        };
      }
      const pendingCount = Math.max(0, state.pendingCount - 1);
      return {
        ...merged,
        pendingCount,
        status: pendingCount > 0 ? "loading" : "ready",
        error: null,
      };
    }
    case "EXPANSION_FAILED": {
      const pendingCount = Math.max(0, state.pendingCount - 1);
      return {
        ...state,
        pendingCount,
        status: pendingCount > 0 ? "loading" : "error",
        error: action.error,
      };
    }
    case "CONTROLS_CHANGED": {
      if (controlsEqual(action.controls, state.controls)) {
        return state;
      }
      // Per #466 D8 a control change does NOT touch the accumulator and
      // does NOT trigger a refetch — only the next click sees the new
      // values. Just clear any stale error so the toolbar isn't sticky.
      return {
        ...state,
        controls: action.controls,
        error: null,
      };
    }
    case "CLEARED": {
      // Empty the canvas but keep the initialSeed; the hook re-runs the
      // seed expansion afterwards so the user lands on a fresh seed frame
      // rather than the empty SeedPrompt.
      return {
        ...INITIAL_ILLUMINATE_STATE,
        controls: state.controls,
        initialSeed: state.initialSeed,
      };
    }
    case "RESET": {
      return { ...INITIAL_ILLUMINATE_STATE, controls: state.controls };
    }
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
}

/**
 * Merge the response from one expansion into the accumulator. Returns
 * `null` when the merge would push the vertex count past the hard cap
 * (#466 D13); the caller surfaces that as an error.
 */
function mergeExpansion(
  state: IlluminateState,
  action: Extract<IlluminateAction, { type: "EXPANSION_RECEIVED" }>,
): IlluminateState | null {
  const vertices = new Map(state.accumulator.vertices);
  const edges = new Map(state.accumulator.edges);

  // Vertices first — collect normalised keys for dedup + audit + the
  // hard-cap check. We treat empty/null keys as no-ops so a server
  // response can't poison the accumulator with anonymous nodes.
  const newVertexKeys: string[] = [];
  const seenVertexKeys = new Set<string>();
  for (const v of action.vertices) {
    const key = typeof v.key === "string" ? v.key : "";
    if (key === "" || seenVertexKeys.has(key)) continue;
    seenVertexKeys.add(key);
    newVertexKeys.push(key);
  }

  // Hard cap — reject merges that would exceed it. We compute the
  // post-merge size by counting only vertices NOT already in the
  // accumulator (the merge is idempotent on keys).
  let projectedSize = vertices.size;
  for (const key of newVertexKeys) {
    if (!vertices.has(key)) projectedSize += 1;
  }
  if (projectedSize > ACCUMULATOR_HARD_CAP) {
    return null;
  }

  // Append the expansion record FIRST so vertex/edge audit indexes can
  // reference it. Append-only means the index stays stable.
  const expansionIndex = state.expansions.length;
  for (const key of newVertexKeys) {
    const prev = vertices.get(key);
    const indexes = prev
      ? withIndex(prev.expansionIndexes, expansionIndex)
      : [expansionIndex];
    const vertex = findVertex(action.vertices, key) ?? prev?.vertex;
    if (!vertex) continue;
    const merged: AccumVertex = {
      vertex,
      receivedAtMs: action.receivedAtMs,
      expansionIndexes: indexes,
    };
    vertices.set(key, merged);
  }

  const newEdgeIds: string[] = [];
  const seenEdgeIds = new Set<string>();
  for (const e of action.edges) {
    const tail = typeof e.tail === "string" ? e.tail : "";
    const head = typeof e.head === "string" ? e.head : "";
    if (tail === "" || head === "") continue;
    const id = edgeIdOf(tail, head);
    if (seenEdgeIds.has(id)) continue;
    seenEdgeIds.add(id);
    newEdgeIds.push(id);
    const prev = edges.get(id);
    const indexes = prev
      ? withIndex(prev.expansionIndexes, expansionIndex)
      : [expansionIndex];
    const merged: AccumEdge = {
      edge: e,
      receivedAtMs: action.receivedAtMs,
      expansionIndexes: indexes,
    };
    edges.set(id, merged);
  }

  const expansion: Expansion = {
    id: action.expansionId,
    origin: action.origin,
    controls: action.controls,
    startedAtMs: action.startedAtMs,
    vertexKeys: newVertexKeys,
    edgeIds: newEdgeIds,
  };

  return {
    ...state,
    accumulator: { vertices, edges },
    expansions: [...state.expansions, expansion],
  };
}

function findVertex(list: Vertex[], key: string): Vertex | null {
  for (const v of list) {
    if (v.key === key) return v;
  }
  return null;
}

function withIndex(list: number[], index: number): number[] {
  if (list.length > 0 && list[list.length - 1] === index) {
    return list;
  }
  return [...list, index];
}

function controlsEqual(a: IlluminateControls, b: IlluminateControls): boolean {
  return (
    a.step === b.step &&
    a.k === b.k &&
    a.algorithm === b.algorithm &&
    a.objective === b.objective &&
    a.weighting === b.weighting &&
    a.vertexPrefix === b.vertexPrefix
  );
}
