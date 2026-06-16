import type { Vertex } from "~/lib/client/infrastructure/api/types";

/**
 * State for the vertex content-search screen (#627).
 *
 * The screen debounces the user's query, runs the server's BM25 keyword
 * search, then hydrates the ranked `{ key, score }` hits back into full
 * vertices (preserving rank order) for display. Async I/O lives in
 * `handlers.ts`; this module and `reducer.ts` are the pure, unit-testable
 * core. Every server response carries the `queryEpoch` it was issued
 * under so a stale reply from an abandoned query can never clobber the
 * current results — the same cancellation invariant Browse Vertices
 * (#409) and the vertex picker (#457) rely on.
 *
 * `disabled` is a first-class lifecycle state, not an error: when the
 * server has the keyword index turned off (opt-out via
 * `LANTERN_SEARCH_ENABLED=false`) the screen renders a calm "not enabled"
 * notice rather than an error toast.
 */
export type SearchVerticesStatus =
  | "idle"
  | "loading"
  | "ready"
  | "error"
  | "disabled";

/** One ranked search hit, hydrated with its full vertex when available. */
export interface SearchResultRow {
  /** The matched vertex key. */
  key: string;
  /** BM25 relevance score (higher = more relevant). */
  score: number;
  /**
   * The hydrated vertex, or `null` when the key no longer resolves — a hit
   * whose vertex expired between the search and the hydration (a real,
   * expected TTL race). The row still renders so the rank slot is honest.
   */
  vertex: Vertex | null;
}

export interface SearchVerticesState {
  /** The debounced query currently being searched. */
  query: string;
  /** Monotonic counter bumped on every query change. */
  queryEpoch: number;
  /** Lifecycle of the in-flight (or last completed) search. */
  status: SearchVerticesStatus;
  /** Ranked, hydrated results for `query`, in descending relevance. */
  results: SearchResultRow[];
  /** Human-readable error from the last failed search, if any. */
  error: string | null;
}

export const INITIAL_SEARCH_VERTICES_STATE: SearchVerticesState = {
  query: "",
  queryEpoch: 0,
  status: "idle",
  results: [],
  error: null,
};
