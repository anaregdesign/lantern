import type { VertexPickerState } from "./state";

/**
 * The ceiling `countVerticesByPrefix` clamps to (wire `uint64` →
 * `Number.MAX_SAFE_INTEGER`). Rendered with a trailing `+` so the user
 * knows the true count may be larger than what JavaScript can represent
 * exactly.
 */
const MATCH_CEILING = Number.MAX_SAFE_INTEGER;

/** Groups digits with underscores: 1234567 → "1_234_567". */
function groupDigits(n: number): string {
  return Math.trunc(n)
    .toString()
    .replace(/\B(?=(\d{3})+(?!\d))/g, "_");
}

/**
 * Formats a match count as "N matches" with underscore digit grouping.
 * The clamp ceiling renders as "9_007_199_254_740_991+ matches".
 */
export function formatMatchCount(count: number): string {
  const noun = count === 1 ? "match" : "matches";
  const suffix = count >= MATCH_CEILING ? "+" : "";
  return `${groupDigits(count)}${suffix} ${noun}`;
}

/**
 * Caption rendered beneath the combobox. Mirrors the picker lifecycle: an
 * empty prefix prompts the user, a failed scan surfaces the error, a known
 * count reports "N matches"; otherwise the search is still settling.
 */
export function selectCaption(state: VertexPickerState): string {
  if (state.prefix.length === 0) {
    return "Type at least 1 character to search.";
  }
  if (state.status === "error") {
    return state.error ?? "Search failed.";
  }
  if (state.matchCount !== null) {
    return formatMatchCount(state.matchCount);
  }
  return "Searching…";
}
