import type { Vertex } from "~/lib/client/infrastructure/api/scan-vertices";

/**
 * One page of vertices returned by `ScanVertices`. We keep the cursor that
 * STARTED this page (`startCursor`) so the user can step backwards by
 * re-issuing the request — the protocol itself is forward-only, so Prev is
 * implemented by maintaining a history stack on the client.
 */
export interface VertexPage {
  vertices: Vertex[];
  startCursor: string;
  nextCursor: string;
}

export type BrowseVerticesStatus = "idle" | "loading" | "ready" | "error";

export type BrowseVerticesFailure = {
  kind: "denied" | "unavailable";
  message: string;
};

export type VertexCountState =
  | { status: "idle" | "loading" }
  | { status: "success"; count: number }
  | { status: "denied" | "unavailable"; error: string };

export interface BrowseVerticesState {
  /** The prefix typed by the user (already debounced upstream). */
  prefix: string;
  /** History of pages fetched for the current prefix. */
  pages: VertexPage[];
  /** Zero-based index into `pages` of the page on screen. */
  currentPageIndex: number;
  /** Result of the most recent `CountVerticesByPrefix` call. */
  count: VertexCountState;
  status: BrowseVerticesStatus;
  error: BrowseVerticesFailure | null;
  /** Independent generations also reject late responses after a refresh. */
  pageRequestId: number;
  countRequestId: number;
  /**
   * Monotonic counter bumped on every prefix change. Async handlers carry
   * the value they saw at dispatch time and discard their result if the
   * counter has moved on (poor-man's request cancellation).
   */
  prefixEpoch: number;
}

export const INITIAL_BROWSE_VERTICES_STATE: BrowseVerticesState = {
  prefix: "",
  pages: [],
  currentPageIndex: -1,
  count: { status: "idle" },
  status: "idle",
  error: null,
  pageRequestId: 0,
  countRequestId: 0,
  prefixEpoch: 0,
};
