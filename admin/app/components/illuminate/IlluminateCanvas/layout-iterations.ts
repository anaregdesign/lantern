/**
 * Pure helper that decides how many ForceAtlas2 iterations to run after
 * a graph reconcile.
 *
 * Background (#454): the canvas previously ran 80 iterations on EVERY
 * prop change. The iterative nature of FA2 means an 80-iteration pass
 * from a perturbed equilibrium (one new centre-of-mass node + extra
 * edges) redistributes every node. Surviving vertices jumped to new
 * positions on every click, making the additive-expansion model (#466)
 * impossible to follow visually.
 *
 * Rule of thumb:
 *
 *   - cold start (graph was empty before): full 80-iteration warm-up.
 *   - pure attribute update (same node set, no add/drop): skip FA2.
 *   - additive (no drops, ≥1 add): light 5-iteration relax. New nodes
 *     were already seeded near their neighbours' centroid (#466 D7);
 *     5 iterations is enough to nudge them out of overlap without
 *     yanking the surrounding layout.
 *   - drops (vertex expired, soft-cap evict, etc.): 30-iteration mid
 *     settle. Fewer than cold start because most positions are still
 *     usable; more than additive because edge counts changed too.
 */
export const FA2_ITERATIONS_COLD = 80;
export const FA2_ITERATIONS_ADDITIVE = 5;
export const FA2_ITERATIONS_DROP = 30;

export interface Fa2IterationsInput {
  /** Number of nodes that survived from the previous reconcile to this one. */
  previousNodeCount: number;
  /** Number of nodes that appeared this reconcile (in next, not in prev). */
  addedCount: number;
  /** Number of nodes that disappeared this reconcile (in prev, not in next). */
  droppedCount: number;
  /** Number of nodes the graph holds AFTER reconcile. */
  nextNodeCount: number;
}

export function decideFa2Iterations(input: Fa2IterationsInput): number {
  // Nothing to lay out.
  if (input.nextNodeCount === 0) return 0;
  // Cold start: empty → populated (initial mount, or reseed after Clear).
  if (input.previousNodeCount === 0) return FA2_ITERATIONS_COLD;
  // Pure attribute refresh: same node set, no structural change.
  if (input.addedCount === 0 && input.droppedCount === 0) return 0;
  // Additive — keep surviving nodes within tolerance of their prior
  // position; only relax new arrivals out of overlap.
  if (input.droppedCount === 0) return FA2_ITERATIONS_ADDITIVE;
  // Drops happened — partial settle.
  return FA2_ITERATIONS_DROP;
}
