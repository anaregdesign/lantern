/**
 * Edge-weight label formatting for the Illuminate canvas (#500).
 *
 * The canvas renders each edge's accumulated weight as an on-edge label
 * (`renderEdgeLabels`). Weights are additive `float32` decay counters, so
 * they are frequently whole numbers but can carry a fraction. We format
 * them compactly so the label stays legible where it sits between two
 * vertices:
 *
 *   - A whole number renders without a decimal point (`3`, not `3.0`).
 *   - A fractional value renders to a single decimal (`2.5`, `0.3`).
 *
 * Kept as a pure helper (mirroring the folder's `force-layout` /
 * `hop-palette` / `ttl-decay` modules) so it is unit-testable without a
 * DOM or a live sigma instance.
 */
export function formatEdgeWeight(weight: number): string {
  if (!Number.isFinite(weight)) {
    return "";
  }
  return Number.isInteger(weight) ? String(weight) : weight.toFixed(1);
}
