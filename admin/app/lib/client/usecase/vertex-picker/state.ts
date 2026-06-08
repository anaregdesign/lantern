/**
 * State for the type-ahead vertex picker (#457).
 *
 * The picker debounces the user's prefix, scans for matching vertex keys,
 * and counts the total matches in parallel. Async I/O lives in
 * `handlers.ts`; this module and `reducer.ts` are the pure, unit-testable
 * core. Every server response carries the `prefixEpoch` it was issued
 * under so a stale reply from an abandoned prefix can never clobber the
 * current suggestions — the same cancellation invariant Browse Vertices
 * (#409) relies on.
 */
export type VertexPickerStatus = "idle" | "loading" | "ready" | "error";

export interface VertexPickerState {
  /** The debounced prefix currently being searched. */
  prefix: string;
  /** Monotonic counter bumped on every prefix change. */
  prefixEpoch: number;
  /** Lifecycle of the in-flight (or last completed) scan. */
  status: VertexPickerStatus;
  /** Vertex keys matching `prefix`, in server order. */
  suggestions: string[];
  /** Total matches for `prefix`, or `null` before the count arrives. */
  matchCount: number | null;
  /** Human-readable error from the last failed scan, if any. */
  error: string | null;
}

export const INITIAL_VERTEX_PICKER_STATE: VertexPickerState = {
  prefix: "",
  prefixEpoch: 0,
  status: "idle",
  suggestions: [],
  matchCount: null,
  error: null,
};
