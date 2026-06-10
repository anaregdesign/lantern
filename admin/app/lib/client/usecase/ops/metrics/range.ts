/**
 * Time-range selection for the Ops Metrics section.
 *
 * Ranges are a small fixed set so the selector can be a segmented control
 * and the persisted value is trivially validatable. Each key maps to a
 * concrete `(rangeSeconds, stepSeconds)` window in `selectors.ts`.
 */
export type RangeKey = "15m" | "1h" | "6h" | "24h";

export const DEFAULT_RANGE: RangeKey = "1h";

export const RANGE_OPTIONS: ReadonlyArray<{ key: RangeKey; label: string }> = [
  { key: "15m", label: "15m" },
  { key: "1h", label: "1h" },
  { key: "6h", label: "6h" },
  { key: "24h", label: "24h" },
];

/** Type guard for a persisted / user-supplied range string. */
export function isRangeKey(value: string): value is RangeKey {
  return RANGE_OPTIONS.some((option) => option.key === value);
}
