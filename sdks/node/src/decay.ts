/**
 * Geometric ("exponential") decay for additive edge weights — the Node
 * port of the Go SDK's `decay.go` (#953). A single decaying add expands,
 * entirely client-side, into a staircase of additive edge contributions
 * with staggered absolute expirations, so the edge's live (summed) weight
 * starts at `initialWeight` and is multiplied by `ratio` every
 * `intervalSeconds`, reaching exactly zero after `steps` intervals. No
 * server-side support is required: the contributions ride the ordinary
 * `AddEdges` batch path (chunking, contrib-id dedup, effective-weight
 * reporting).
 *
 * `decayContributions` is the pure, deterministic core (mirrors Go's
 * `DecayContributions`); `Lantern.addDecayingEdge` applies it with
 * `Date.now()` as the t=0 reference.
 */

import { InvalidArgumentError } from "./errors.js";
import type { EdgeInput } from "./values.js";

/**
 * Default per-call fan-out ceiling for {@link decayContributions} /
 * `Lantern.addDecayingEdge`: one decaying add expands into at most this
 * many additive contributions. It is a safety rail, not the fundamental
 * limit — the binding constraints are the server's
 * `LANTERN_TOMBSTONE_TTL` horizon (which caps `steps * intervalSeconds`)
 * and float32 underflow. Geometric decay with `ratio <= 0.5` is already
 * negligible (below 2^-16 of the initial weight) by 16 steps; callers who
 * want a smoother or longer curve raise `intervalSeconds` / the half-life
 * rather than the step count. Matches Go's `MaxDecaySteps`.
 */
export const MAX_DECAY_STEPS = 16;

/**
 * Specifies a geometric decay staircase for the live weight contributed by
 * one `addDecayingEdge` call. The idiomatic Node counterpart of Go's
 * `DecayOpts` — the only shape difference is `intervalSeconds` (a number of
 * seconds, matching the SDK's `ttlSeconds` convention) in place of Go's
 * `time.Duration`.
 */
export interface DecayOptions {
  /**
   * S(0): the live-sum weight this call contributes to the edge
   * immediately after it is applied. May be negative (a decaying negative
   * reinforcement) but must be non-zero and finite.
   */
  initialWeight: number;
  /**
   * The per-step multiplier r, which must lie in the open interval (0, 1):
   * the contributed live weight on step k is `initialWeight * r^k`.
   */
  ratio: number;
  /**
   * The number of decay steps N; must satisfy `1 <= steps <=
   * MAX_DECAY_STEPS`. `steps === 1` degenerates to a single
   * `addEdge(initialWeight)` expiring after `intervalSeconds`.
   */
  steps: number;
  /**
   * The wall-clock duration of one decay step, in seconds; must be > 0.
   * Fractional (sub-second) values are allowed.
   */
  intervalSeconds: number;
}

/**
 * Reports the first way `opts` is ill-formed by throwing
 * {@link InvalidArgumentError}, or returns cleanly when it is a well-formed
 * decay spec. Mirrors Go's `DecayOpts.validate`, plus the finiteness guards
 * JS needs (Go's float32 arithmetic cannot silently produce NaN/Infinity
 * the way JS numbers can).
 */
function validateDecayOptions(opts: DecayOptions): void {
  if (!Number.isFinite(opts.initialWeight) || opts.initialWeight === 0) {
    throw new InvalidArgumentError(
      `decay: initialWeight must be a non-zero finite number; got ${opts.initialWeight}`,
    );
  }
  if (!Number.isFinite(opts.ratio) || !(opts.ratio > 0 && opts.ratio < 1)) {
    throw new InvalidArgumentError(
      `decay: ratio must be in the open interval (0, 1); got ${opts.ratio}`,
    );
  }
  if (!Number.isInteger(opts.steps) || opts.steps < 1) {
    throw new InvalidArgumentError(`decay: steps must be an integer >= 1; got ${opts.steps}`);
  }
  if (opts.steps > MAX_DECAY_STEPS) {
    throw new InvalidArgumentError(
      `decay: steps must be <= MAX_DECAY_STEPS (${MAX_DECAY_STEPS}); got ${opts.steps}`,
    );
  }
  if (!Number.isFinite(opts.intervalSeconds) || opts.intervalSeconds <= 0) {
    throw new InvalidArgumentError(
      `decay: intervalSeconds must be > 0; got ${opts.intervalSeconds}`,
    );
  }
}

/**
 * Expands a geometric decay spec into the additive edge contributions that
 * realize it, relative to `baseMs` (epoch milliseconds) as t=0. It is the
 * pure, deterministic core of `addDecayingEdge`: contribution j (1-indexed)
 * targets the same `(tail, head)`, expires at `baseMs + j*intervalSeconds`,
 * and carries the step drop
 *
 * ```text
 *   c_j = S(j-1) - S(j)   for j = 1..N-1
 *   c_N = S(N-1)          (the residual, folded into the last step)
 * ```
 *
 * where `S(k) = initialWeight * ratio^k` is the target live-sum on step k
 * and N = `steps`. Because a read sums every live contribution, these
 * telescope to `S(0) = initialWeight`: the edge's live weight is
 * `initialWeight` right after the add and then follows the staircase
 * `initialWeight`, `initialWeight*ratio`, …, and exactly 0 once the last
 * contribution expires at `baseMs + N*intervalSeconds`.
 *
 * Note the contribution weights are the step DROPS, not the live-sum
 * values: `initialWeight=16, ratio=0.5, steps=5` yields weights
 * `8,4,2,1,1` (not `16,8,4,2,1`, whose live sum would start at 31).
 * Specifying the target curve rather than the raw weights is the whole
 * point of the helper.
 *
 * Contributions that underflow to exactly zero in float32 (deep in a fast
 * decay) are omitted — they would add nothing while still auto-materializing
 * endpoints and consuming server capacity — so the returned array may be
 * shorter than `steps`. Each contribution is rounded once to float32 via
 * {@link Math.fround} (the wire weight type), so a caller comparing the
 * read-back live weight against `initialWeight` should allow a small
 * float32 tolerance.
 *
 * @throws {InvalidArgumentError} when `opts` is ill-formed, or when
 * `initialWeight` is so small the entire curve underflows float32 to zero.
 */
export function decayContributions(
  tail: string,
  head: string,
  opts: DecayOptions,
  baseMs: number,
): EdgeInput[] {
  validateDecayOptions(opts);
  const w0 = opts.initialWeight;
  const r = opts.ratio;
  const out: EdgeInput[] = [];
  for (let j = 1; j <= opts.steps; j++) {
    // c_j is the drop S(j-1)-S(j) = w0*r^(j-1)*(1-r) for every step but the
    // last, which carries the whole residual S(N-1) = w0*r^(N-1) so the live
    // weight reaches exactly zero at baseMs + N*intervalSeconds.
    const exp = j - 1;
    const c = j < opts.steps ? w0 * Math.pow(r, exp) * (1 - r) : w0 * Math.pow(r, exp);
    // Round to float32 (the wire weight type) and drop underflows. `=== 0`
    // catches both +0 and -0 (a tiny negative contribution rounds to -0).
    const w = Math.fround(c);
    if (w === 0) continue;
    out.push({
      tail,
      head,
      weight: w,
      expiration: new Date(baseMs + j * opts.intervalSeconds * 1000),
    });
  }
  if (out.length === 0) {
    throw new InvalidArgumentError(
      `decay: curve underflows float32 to zero (initialWeight ${opts.initialWeight} is too small)`,
    );
  }
  return out;
}

/**
 * Builds a {@link DecayOptions} from a half-life instead of an explicit
 * ratio: the contributed weight halves every `halfLifeSeconds`, sampled
 * every `intervalSeconds` over `horizonSeconds`. It sets
 *
 * ```text
 *   ratio = 2^(-intervalSeconds/halfLifeSeconds)
 *   steps = ceil(horizonSeconds/intervalSeconds), clamped to [1, MAX_DECAY_STEPS]
 * ```
 *
 * The result feeds `addDecayingEdge` / {@link decayContributions}, which
 * validate it; `halfLifeDecay` itself does not throw, so a non-positive
 * `halfLifeSeconds`/`intervalSeconds`/`horizonSeconds` yields a
 * `DecayOptions` those functions reject. Mirrors Go's `HalfLifeDecay`.
 */
export function halfLifeDecay(
  initialWeight: number,
  halfLifeSeconds: number,
  intervalSeconds: number,
  horizonSeconds: number,
): DecayOptions {
  const opts: DecayOptions = { initialWeight, ratio: 0, steps: 0, intervalSeconds };
  if (halfLifeSeconds > 0 && intervalSeconds > 0) {
    opts.ratio = Math.fround(Math.pow(0.5, intervalSeconds / halfLifeSeconds));
  }
  if (intervalSeconds > 0) {
    let steps = Math.ceil(horizonSeconds / intervalSeconds);
    if (steps < 1) steps = 1;
    else if (steps > MAX_DECAY_STEPS) steps = MAX_DECAY_STEPS;
    opts.steps = steps;
  }
  return opts;
}
