/**
 * Pure reconcile-time decisions for the Illuminate canvas (#495 batch 3).
 *
 * The canvas's reconcile `useEffect`
 * (`components/illuminate/IlluminateCanvas/IlluminateCanvas.tsx`) must, on
 * every render-set change, (1) measure the structural delta between the
 * frame it last drew and the frame it is about to draw, and (2) pick which
 * d3-force layout regime that delta warrants (#483). Both are pure
 * set/scalar arithmetic with no React, sigma, or graphology in sight, so
 * they live here where they can be unit-tested in isolation — the
 * component keeps only the imperative "apply the chosen regime to the live
 * simulation" glue.
 *
 * Behaviour is identical to the inline logic this replaces; the move is a
 * relocation, not a change (#495). The regime enum is deliberately
 * alpha-free: deciding the regime is a pure semantic choice, while mapping
 * the chosen regime to a d3-force heat constant (`FORCE_ALPHA_COLD` /
 * `FORCE_ALPHA`) and driving the live simulation is imperative glue the
 * rendering shell owns.
 */

/**
 * Structural delta between the previously-rendered set and the next one.
 * `addedCount` / `droppedCount` count node-id membership changes;
 * `edgeSetChanged` is a boolean because the layout regime only cares
 * *whether* the edge set changed, not by how much — an edge-only
 * expansion still reheats the whole render set (#500).
 */
export interface RenderSetDiff {
  /** Count of ids in `nextNodeIds` that were absent from `previousNodeIds`. */
  addedCount: number;
  /** Count of ids in `previousNodeIds` that are absent from `nextNodeIds`. */
  droppedCount: number;
  /** True iff the edge-id set differs (size or membership) between frames. */
  edgeSetChanged: boolean;
}

/**
 * Measures the node add/drop counts and whether the edge set changed
 * between the previously-rendered frame and the next one. Pure; mirrors
 * the diff the reconcile effect computed inline BEFORE mutating
 * graphology, so the counts reflect the structural delta rather than the
 * post-merge state.
 *
 * The edge comparison short-circuits on a size mismatch and otherwise
 * scans `nextEdgeIds` for a previously-absent id: for two equal-size sets,
 * membership is symmetric, so a one-directional scan is sufficient to
 * decide whether they differ.
 */
export function diffRenderSets(
  previousNodeIds: ReadonlySet<string>,
  nextNodeIds: ReadonlySet<string>,
  previousEdgeIds: ReadonlySet<string>,
  nextEdgeIds: ReadonlySet<string>,
): RenderSetDiff {
  let addedCount = 0;
  let droppedCount = 0;
  for (const id of nextNodeIds) {
    if (!previousNodeIds.has(id)) addedCount += 1;
  }
  for (const id of previousNodeIds) {
    if (!nextNodeIds.has(id)) droppedCount += 1;
  }
  let edgeSetChanged = previousEdgeIds.size !== nextEdgeIds.size;
  if (!edgeSetChanged) {
    for (const id of nextEdgeIds) {
      if (!previousEdgeIds.has(id)) {
        edgeSetChanged = true;
        break;
      }
    }
  }
  return { addedCount, droppedCount, edgeSetChanged };
}

/**
 * Which d3-force layout regime a reconcile should run (#483):
 *
 * - `"cold"`: empty → populated, or a full reseed with NO survivors. The
 *   component starts at full heat (`FORCE_ALPHA_COLD`) and settles in bounded
 *   animation-frame batches so large graphs do not create one long task.
 * - `"incremental"`: at least one survivor carried over AND something
 *   structural changed (a node was added/dropped, or the edge set changed
 *   — #500). The component eases the new frame in on rAF (`FORCE_ALPHA`),
 *   painting survivors at their exact prior position first so the click
 *   never snaps.
 * - `"static"`: nothing structural changed (a pure attribute/weight
 *   refresh), or there is nothing to lay out. The component just refreshes
 *   sigma and leaves any in-flight animation undisturbed (dropping a
 *   now-empty simulation on Clear).
 */
export type LayoutRegime = "cold" | "incremental" | "static";

/**
 * Picks the {@link LayoutRegime} for a reconcile from its structural delta.
 *
 * `nextNodeCount` is the size of the resolved render set (`nextNodeIds`),
 * while `forceNodeCount` is how many nodes actually made it into
 * graphology this frame. They can differ: a latest-result key whose vertex
 * was filtered out (e.g. expired, #459) counts toward the render set but
 * is never added to the graph, leaving the simulation empty. Keeping them
 * separate preserves the original guard (`isColdStart && forceNodes.length
 * > 0`) exactly — an "all-new but nothing actually rendered" reconcile
 * falls through to `"static"`, which the component handles by stopping any
 * empty simulation.
 */
export function decideLayoutRegime({
  nextNodeCount,
  forceNodeCount,
  addedCount,
  droppedCount,
  edgeSetChanged,
}: {
  nextNodeCount: number;
  forceNodeCount: number;
  addedCount: number;
  droppedCount: number;
  edgeSetChanged: boolean;
}): LayoutRegime {
  const survivorCount = nextNodeCount - addedCount;
  const isColdStart = nextNodeCount > 0 && survivorCount === 0;
  if (isColdStart && forceNodeCount > 0) return "cold";
  const isIncremental =
    !isColdStart &&
    forceNodeCount > 0 &&
    (addedCount > 0 || droppedCount > 0 || edgeSetChanged);
  if (isIncremental) return "incremental";
  return "static";
}
