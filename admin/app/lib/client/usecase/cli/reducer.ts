import {
  initialBanner,
  type CliState,
  type LatestGraph,
  type PendingDestructive,
  type ScrollbackEntry,
} from "./state";
import { mergeGraphView, type GraphMerge } from "./graph-view";

export type CliAction =
  /** Prompt text changed (typing into the `<Input>`). */
  | { type: "INPUT_CHANGED"; value: string }
  /** Arrow-up: recall an older history entry into the prompt. */
  | { type: "HISTORY_PREV" }
  /** Arrow-down: move toward the newest history entry / empty prompt. */
  | { type: "HISTORY_NEXT" }
  /** A raw line was submitted: push to history and clear the prompt. */
  | { type: "COMMAND_SUBMITTED"; raw: string }
  /** Append a scrollback line (auto-assigns the next id). */
  | { type: "ENTRY_APPENDED"; entry: Omit<ScrollbackEntry, "id"> }
  /** Reset the scrollback to just the banner (Clear / Ctrl+L). */
  | { type: "SCROLLBACK_CLEARED" }
  /** A dispatch started: enter the `running` phase. */
  | { type: "RUN_STARTED" }
  /** A dispatch settled (ok, error, or abort): return to `idle`. */
  | { type: "RUN_SETTLED" }
  /** A graph-producing verb landed: replace the canvas view. */
  | { type: "GRAPH_UPDATED"; graph: LatestGraph }
  /**
   * A mutating verb (`put`/`add`) landed: fold the new element onto the
   * live frame instead of replacing it (#518), so the operator's context
   * survives the write. Opens a fresh frame when the canvas was empty.
   */
  | { type: "GRAPH_MERGED"; source: string; merge: GraphMerge }
  /** A destructive verb needs confirmation. */
  | { type: "PENDING_SET"; pending: PendingDestructive }
  /** Confirmation resolved (Run or Cancel) — clear the prompt chip. */
  | { type: "PENDING_CLEARED" }
  /** Toggle the per-session "do not ask again" flag. */
  | { type: "SKIP_CONFIRM_CHANGED"; value: boolean };

export function cliReducer(state: CliState, action: CliAction): CliState {
  switch (action.type) {
    case "INPUT_CHANGED": {
      if (action.value === state.input) {
        return state;
      }
      return { ...state, input: action.value };
    }
    case "HISTORY_PREV": {
      if (state.history.length === 0) {
        return state;
      }
      const next =
        state.historyIndex === null
          ? state.history.length - 1
          : Math.max(0, state.historyIndex - 1);
      return { ...state, historyIndex: next, input: state.history[next] };
    }
    case "HISTORY_NEXT": {
      if (state.historyIndex === null) {
        return state;
      }
      const next = state.historyIndex + 1;
      if (next >= state.history.length) {
        return { ...state, historyIndex: null, input: "" };
      }
      return { ...state, historyIndex: next, input: state.history[next] };
    }
    case "COMMAND_SUBMITTED": {
      return {
        ...state,
        history: [...state.history, action.raw],
        historyIndex: null,
        input: "",
      };
    }
    case "ENTRY_APPENDED": {
      return {
        ...state,
        scrollback: [
          ...state.scrollback,
          { ...action.entry, id: state.nextEntryId },
        ],
        nextEntryId: state.nextEntryId + 1,
      };
    }
    case "SCROLLBACK_CLEARED": {
      return { ...state, scrollback: [initialBanner()], nextEntryId: 2 };
    }
    case "RUN_STARTED": {
      return { ...state, phase: "running" };
    }
    case "RUN_SETTLED": {
      return { ...state, phase: "idle" };
    }
    case "GRAPH_UPDATED": {
      return { ...state, latestGraph: action.graph };
    }
    case "GRAPH_MERGED": {
      return {
        ...state,
        latestGraph: {
          source: action.source,
          view: mergeGraphView(state.latestGraph?.view ?? null, action.merge),
        },
      };
    }
    case "PENDING_SET": {
      return { ...state, pending: action.pending };
    }
    case "PENDING_CLEARED": {
      if (state.pending === null) {
        return state;
      }
      return { ...state, pending: null };
    }
    case "SKIP_CONFIRM_CHANGED": {
      if (action.value === state.skipConfirm) {
        return state;
      }
      return { ...state, skipConfirm: action.value };
    }
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
}
