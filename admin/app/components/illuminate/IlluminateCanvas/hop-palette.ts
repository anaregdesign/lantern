/**
 * Pure mapping from a node's hop distance (#460) to its base canvas
 * fill. Lives next to {@link SigmaPalette} so the swatch source of
 * truth stays in one place; the canvas reducer composes the result of
 * this function with the TTL fade (#459) and hover dim (#458) in the
 * locked order `hop hue → TTL alpha → hover dim`.
 *
 * Kept pure (no React, no DOM, no sigma) so the unit suite can pin
 * the boundary behaviour at every stop without booting a renderer,
 * and so swapping the palette source (Fluent token resolution vs
 * test-injected literal) doesn't change the colour decision.
 *
 * Boundary semantics:
 *   hopDistance === 0       → `palette.hop0` (the expansion origin itself)
 *   hopDistance === 1       → `palette.hop1`
 *   hopDistance === 2       → `palette.hop2`
 *   hopDistance >= 3        → `palette.hopFar`
 *   hopDistance is +Infinity → `palette.hopUnreachable`
 *   hopDistance is NaN/-1   → defensive: treated as unreachable
 *
 * The "≥3 collapses to `hopFar`" rule is deliberate: distinguishing
 * 3-hop from 5-hop visually adds noise without informational value
 * when the canvas is also encoding TTL alpha and hover focus on the
 * same pixel. The unreachable swatch is reserved for vertices truly
 * disconnected from every origin — the canvas never gets there via
 * normal Illuminate calls, but the selector emits it defensively so
 * the test bridge and a stale-accumulator scenario have a stable
 * colour to render.
 */
import { HOP_FAR_THRESHOLD } from "~/lib/client/usecase/illuminate/selectors";
import type { SigmaPalette } from "./palette";

/**
 * Re-exported from the use-case layer so existing `./hop-palette` import
 * sites keep resolving the same constant. {@link HOP_FAR_THRESHOLD} is
 * now canonically owned by `usecase/illuminate/selectors` (shared with the
 * legend read-model); `colorForHop` / `describeHop` below still use it to
 * pin the `>= 3` boundary unambiguously for the unit suite.
 */
export { HOP_FAR_THRESHOLD };

export function colorForHop(
  hopDistance: number,
  palette: SigmaPalette,
): string {
  // `NaN` and negative-finite values are nonsense from the selector's
  // point of view, but the runtime contract here is "always return a
  // renderable colour" — fall through to the unreachable tone so the
  // canvas paints something instead of an empty string.
  if (!Number.isFinite(hopDistance) || hopDistance < 0) {
    return palette.hopUnreachable;
  }
  if (hopDistance === 0) return palette.hop0;
  if (hopDistance === 1) return palette.hop1;
  if (hopDistance === 2) return palette.hop2;
  // hopDistance >= HOP_FAR_THRESHOLD (3+)
  return palette.hopFar;
}

/**
 * Stable, human-readable label for a hop distance. Powers the canvas
 * legend (#460) and the node tooltip's "hop = N" line. Kept pure so
 * the legend doesn't need to duplicate the bucketing logic.
 */
export function describeHop(hopDistance: number): string {
  if (!Number.isFinite(hopDistance) || hopDistance < 0) {
    return "unreachable";
  }
  if (hopDistance === 0) return "origin";
  if (hopDistance === 1) return "1 hop";
  if (hopDistance === 2) return "2 hops";
  return `${HOP_FAR_THRESHOLD}+ hops`;
}
