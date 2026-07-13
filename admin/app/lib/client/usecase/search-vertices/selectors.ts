import type { SearchVerticesState } from "./state";

/** Formats a result count as "N result(s)". */
export function formatResultCount(count: number): string {
  const noun = count === 1 ? "result" : "results";
  return `${count} ${noun}`;
}

/**
 * Formats a BM25 relevance score for display. Scores are unbounded
 * positive floats whose absolute magnitude is not meaningful to the user;
 * three decimals is enough to distinguish adjacent ranks without implying
 * false precision.
 */
export function formatScore(score: number): string {
  return score.toFixed(3);
}

/**
 * Caption rendered beneath the search box. Mirrors the search lifecycle:
 * an empty query prompts the user, a disabled index explains itself
 * calmly, a failed search surfaces the error, a settled search reports the
 * result count; otherwise the search is still in flight.
 */
export function selectCaption(state: SearchVerticesState): string {
  if (state.query.length === 0) {
    return "Type a query to search vertex content.";
  }
  if (state.status === "disabled") {
    return "Content search is not enabled on this server.";
  }
  if (state.status === "error") {
    return state.error ?? "Search failed.";
  }
  if (state.status === "ready") {
    if (state.results.length === 0) return "No vertices match this query.";
    if (state.truncated) {
      const suffix = state.continuationLimited
        ? " Server continuation limit reached."
        : "";
      return `Top ${state.results.length} shown.${suffix}`;
    }
    return formatResultCount(state.results.length);
  }
  return "Searching…";
}
