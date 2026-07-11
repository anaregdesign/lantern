import {
  initialBanner,
  type CliState,
  type LatestGraph,
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
  /**
   * A raw line was accepted for execution: push it to `history` (at run
   * time, so history order === execution order — queued lines join here
   * when they drain, not when they were typed). Does NOT touch the prompt.
   */
  | { type: "HISTORY_APPENDED"; raw: string }
  /** The prompt was submitted (Enter): clear the input box + history cursor. */
  | { type: "PROMPT_CLEARED" }
  /**
   * A line was submitted while a dispatch was in flight (#945): append it
   * to the pending-command queue instead of dropping the keystrokes. No
   * scrollback echo yet — the echo happens when the command actually runs
   * (see `HISTORY_APPENDED`/`ENTRY_APPENDED`), so scrollback order stays
   * execution order.
   */
  | { type: "ENQUEUE"; input: string }
  /** Drop the head of the pending-command queue (the drain step ran it). */
  | { type: "DEQUEUE" }
  /**
   * Discard every pending command (#945). Wired to cancellation (Esc /
   * Cancel): hitting Esc mid-script means "stop the script", not "skip one
   * line". No scrollback entry — the `aborted` line already explains it.
   */
  | { type: "QUEUE_CLEARED" }
  /** Append a scrollback line (auto-assigns the next id). */
  | { type: "ENTRY_APPENDED"; entry: Omit<ScrollbackEntry, "id"> }
  /** Reset the scrollback to just the banner (Clear / Ctrl+L). */
  | { type: "SCROLLBACK_CLEARED" }
  /** A dispatch started: enter the `running` phase. */
  | { type: "RUN_STARTED" }
  /** A dispatch settled (ok, error, or abort): return to `idle`. */
  | { type: "RUN_SETTLED" }
  /**
   * A command errored (#945). Clears the remaining queue so a fail-fast
   * halt keeps a typo'd or rejected line from letting the rest of a pasted
   * script mutate the graph, and — when there were pending commands —
   * appends an info line naming how many were dropped. The red error chip
   * itself is a separate `ENTRY_APPENDED`; this action only owns the
   * queue-clear + the "dropped N" notice. Phase returns to idle via the
   * always-run `RUN_SETTLED`.
   */
  | { type: "RUN_FAILED" }
  /** A graph-producing verb landed: replace the canvas view. */
  | { type: "GRAPH_UPDATED"; graph: LatestGraph }
  /**
   * A mutating verb (`put`/`add`) landed: fold the new element onto the
   * live frame instead of replacing it (#518), so the operator's context
   * survives the write. Opens a fresh frame when the canvas was empty.
   */
  | { type: "GRAPH_MERGED"; source: string; merge: GraphMerge };

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
    case "HISTORY_APPENDED": {
      return {
        ...state,
        history: [...state.history, action.raw],
        historyIndex: null,
      };
    }
    case "PROMPT_CLEARED": {
      return { ...state, historyIndex: null, input: "" };
    }
    case "ENQUEUE": {
      return { ...state, queue: [...state.queue, action.input] };
    }
    case "DEQUEUE": {
      if (state.queue.length === 0) {
        return state;
      }
      return { ...state, queue: state.queue.slice(1) };
    }
    case "QUEUE_CLEARED": {
      if (state.queue.length === 0) {
        return state;
      }
      return { ...state, queue: [] };
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
    case "RUN_FAILED": {
      // Fail-fast (#945): drop the rest of a pasted/queued script the
      // moment one line errors, so a mistyped or server-rejected command
      // can't let the lines after it keep mutating the graph. The operator
      // re-issues from a known-good point instead of chasing a half-applied
      // batch. Only annotate the drop when commands were actually pending —
      // a lone failing command (queue empty) just shows its red error chip.
      const pending = state.queue.length;
      if (pending === 0) {
        return state;
      }
      return {
        ...state,
        queue: [],
        scrollback: [
          ...state.scrollback,
          {
            id: state.nextEntryId,
            input: "",
            kind: "info",
            text: `queue cleared after error (${pending} pending command${
              pending === 1 ? "" : "s"
            } dropped)`,
          },
        ],
        nextEntryId: state.nextEntryId + 1,
      };
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
          // A merged write is a new canvas frame, not a traversal result. Do not
          // present stale family parameters as if they described this frame.
          traversal: null,
        },
      };
    }
    default: {
      const exhaustive: never = action;
      return exhaustive;
    }
  }
}
