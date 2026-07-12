import type { Vertex } from "~/lib/client/infrastructure/api/types";

/**
 * State for the vertex content-search screen (#627).
 *
 * The screen invalidates on every input immediately, debounces the matching
 * BM25 keyword search, then hydrates ranked `{ key, score }` hits into full
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

/** How a multi-word content-search query's words combine. */
export type SearchMatchMode = "any" | "all" | "min-should";

/** The relevance controls the search box exposes (#892). */
export interface SearchQueryOptions {
  /** Word combination: "any" (OR, default), "all" (AND), or "min-should". */
  matchMode: SearchMatchMode;
  /** Require the query's words to occur adjacently, in order. */
  phrase: boolean;
  /** Tolerate typos and match word prefixes. */
  fuzzy: boolean;
}

export const DEFAULT_SEARCH_QUERY_OPTIONS: SearchQueryOptions = {
  matchMode: "any",
  phrase: false,
  fuzzy: false,
};

export interface SearchVerticesState {
  /** The latest input query; only the matching RPC start is debounced. */
  query: string;
  /** Monotonic counter bumped on every query or option change. */
  queryEpoch: number;
  /** Lifecycle of the in-flight (or last completed) search. */
  status: SearchVerticesStatus;
  /** Ranked, hydrated results for `query`, in descending relevance. */
  results: SearchResultRow[];
  /** Human-readable error from the last failed search, if any. */
  error: string | null;
  /** The relevance controls applied to `query`. */
  options: SearchQueryOptions;
}

export const INITIAL_SEARCH_VERTICES_STATE: SearchVerticesState = {
  query: "",
  queryEpoch: 0,
  status: "idle",
  results: [],
  error: null,
  options: DEFAULT_SEARCH_QUERY_OPTIONS,
};
