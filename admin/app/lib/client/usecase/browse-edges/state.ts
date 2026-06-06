import type { Edge } from "~/lib/client/infrastructure/api/scan-edges";

export interface EdgePage {
  edges: Edge[];
  startCursor: string;
  nextCursor: string;
}

export type BrowseEdgesStatus = "idle" | "loading" | "ready" | "error";

export interface BrowseEdgesState {
  tailPrefix: string;
  headPrefix: string;
  pages: EdgePage[];
  currentPageIndex: number;
  status: BrowseEdgesStatus;
  error: string | null;
  /** Bumped on every prefix change; see browse-vertices/state.ts. */
  prefixEpoch: number;
}

export const INITIAL_BROWSE_EDGES_STATE: BrowseEdgesState = {
  tailPrefix: "",
  headPrefix: "",
  pages: [],
  currentPageIndex: -1,
  status: "idle",
  error: null,
  prefixEpoch: 0,
};
