/**
 * Aggregation-mode selection for the Ops Metrics section.
 *
 * A panel's series can be rendered either **per-replica** (one line per
 * server instance, identified by a short stable alias) or collapsed to a
 * single **cluster sum**. The mode is a small fixed set so the selector can
 * be a segmented control and the persisted value is trivially validatable;
 * `selectors.ts#composeQuery` turns it into the concrete `sum by (…)`
 * wrapping for each catalog query.
 */
export type AggMode = "per-replica" | "sum";

/**
 * Default to per-replica: the whole point of the section is per-box
 * visibility, and the cluster sum is one click away.
 */
export const DEFAULT_MODE: AggMode = "per-replica";

export const AGG_MODE_OPTIONS: ReadonlyArray<{ key: AggMode; label: string }> =
  [
    { key: "per-replica", label: "Per-replica" },
    { key: "sum", label: "Sum" },
  ];

/** Type guard for a persisted / user-supplied aggregation-mode string. */
export function isAggMode(value: string): value is AggMode {
  return AGG_MODE_OPTIONS.some((option) => option.key === value);
}
