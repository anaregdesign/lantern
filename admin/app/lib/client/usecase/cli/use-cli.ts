import {
  useCallback,
  useEffect,
  useMemo,
  useReducer,
  useRef,
  useState,
} from "react";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { scanVertices } from "~/lib/client/infrastructure/api/scan-vertices";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import {
  dispatch as dispatchCommand,
  isDestructive,
} from "~/lib/client/usecase/cli/dispatcher";
import { commandResultToGraphView } from "~/lib/client/usecase/cli/graph-view";
import { parse, type Command, type ParseResult } from "~/lib/cli/parser";
import { HELP_TEXT } from "~/lib/cli/verbs";
import { cliReducer } from "./reducer";
import {
  INITIAL_CLI_STATE,
  type LatestGraph,
  type PendingDestructive,
  type ScrollbackEntry,
} from "./state";

export interface UseCliResult {
  /** Durable scrollback log, ready to render. */
  scrollback: ScrollbackEntry[];
  /** Current prompt text. */
  input: string;
  /** Destructive verb awaiting confirmation, if any. */
  pending: PendingDestructive | null;
  /** Per-session "do not ask again" flag. */
  skipConfirm: boolean;
  /** True while a dispatch is in flight (drives `Cancel` + disabled prompt). */
  busy: boolean;
  /** Most recent graph-producing command's view, or null. */
  latestGraph: LatestGraph | null;
  /**
   * Vertex keys available for Tab completion (#515): the union of a
   * best-effort mount scan and every key currently on the canvas.
   */
  knownKeys: string[];
  /** Update the prompt text. */
  setInput: (value: string) => void;
  /** Run the current prompt text (Enter). */
  submit: () => void;
  /** Run an arbitrary raw line (click-to-illuminate). */
  runRaw: (raw: string) => void;
  /** Recall an older history entry (ArrowUp). */
  historyPrev: () => void;
  /** Move toward the newest history entry (ArrowDown). */
  historyNext: () => void;
  /** Reset the scrollback to the banner (Clear / Ctrl+L). */
  clearScrollback: () => void;
  /** Abort the in-flight dispatch (Cancel / Esc). */
  cancelInFlight: () => void;
  /** Toggle the per-session confirmation skip. */
  setSkipConfirm: (value: boolean) => void;
  /** Confirm and run the pending destructive command. */
  confirmRun: () => void;
  /** Dismiss the pending destructive command. */
  confirmCancel: () => void;
}

/**
 * Owns the /cli route's stateful dispatch loop: command parsing,
 * per-verb dispatch through `lantern-sdk/web`, arrow-key history,
 * destructive-verb confirmation, cancellation via `AbortController`,
 * and the graph projection that feeds the canvas.
 *
 * Per the skill's stateful-flow compromise, the lifecycle-heavy
 * orchestration (async dispatch + abort handle) lives here in the
 * controller hook while `CliPage` stays render-only. The reducer
 * (`./reducer`) keeps every state transition pure and unit-testable;
 * the `AbortController` is the one runtime handle and is held in a ref,
 * never in reducer state.
 */
export function useCli(): UseCliResult {
  const client = useLanternClient();
  const [state, dispatch] = useReducer(cliReducer, INITIAL_CLI_STATE);
  // Backs the `Cancel` action (#433). `runCommand` populates this with
  // a fresh controller before each dispatch and clears it on settle;
  // `cancelInFlight` calls `.abort()` on it while busy. The dispatcher
  // plumbs `signal` through every underlying RPC, so the abort
  // propagates end-to-end.
  const abortRef = useRef<AbortController | null>(null);

  // Best-effort completion vocabulary (#515). One page of vertex keys is
  // pulled on mount so Tab can complete real keys, not just the ones the
  // canvas already shows. This is a convenience, never a correctness
  // dependency: a failed/aborted scan simply degrades completion to the
  // canvas-derived keys merged in by `knownKeys` below.
  const [scannedKeys, setScannedKeys] = useState<string[]>([]);

  useEffect(() => {
    const controller = new AbortController();
    void (async () => {
      try {
        const page = await scanVertices(
          client,
          { prefix: "", limit: 1000 },
          { signal: controller.signal },
        );
        setScannedKeys(
          (page.vertices ?? [])
            .map((v) => v.key)
            .filter((k): k is string => typeof k === "string"),
        );
      } catch {
        // Swallow — Tab completion falls back to canvas-only keys.
      }
    })();
    return () => controller.abort();
  }, [client]);

  const busy = state.phase === "running";

  const runCommand = useCallback(
    async (rawInput: string, command: Command) => {
      dispatch({ type: "RUN_STARTED" });
      const controller = new AbortController();
      abortRef.current = controller;
      const start = performance.now();
      try {
        const out = await dispatchCommand({
          client,
          command,
          signal: controller.signal,
        });
        const elapsed = performance.now() - start;
        dispatch({
          type: "ENTRY_APPENDED",
          entry: {
            input: rawInput,
            kind: "ok",
            text:
              out === null || out === undefined
                ? "OK"
                : "OK\n" + JSON.stringify(out, replacer, 2),
            durationMs: elapsed,
          },
        });
        // Project graph-shaped results onto the canvas. null means the
        // verb carries no graph payload (put/add/delete/exit) — leave
        // the previous canvas alone in that case.
        const view = commandResultToGraphView(command, out);
        if (view !== null) {
          dispatch({
            type: "GRAPH_UPDATED",
            graph: { source: rawInput, view },
          });
        }
      } catch (err) {
        const elapsed = performance.now() - start;
        // Cancellation via the Cancel button / Esc — render an `info`
        // line ("aborted") rather than the red error chip so the
        // operator can tell the difference between a failed RPC and a
        // deliberate stop (#433).
        if (isAbortError(err) || controller.signal.aborted) {
          dispatch({
            type: "ENTRY_APPENDED",
            entry: {
              input: rawInput,
              kind: "info",
              text: "aborted",
              durationMs: elapsed,
            },
          });
        } else {
          dispatch({
            type: "ENTRY_APPENDED",
            entry: {
              input: rawInput,
              kind: "error",
              text: errorMessage(err),
              durationMs: elapsed,
            },
          });
        }
      } finally {
        abortRef.current = null;
        dispatch({ type: "RUN_SETTLED" });
      }
    },
    [client],
  );

  const runRaw = useCallback(
    async (raw: string) => {
      if (raw.trim() === "") return;
      dispatch({ type: "COMMAND_SUBMITTED", raw });
      const result: ParseResult = parse(raw);
      if (!result.ok) {
        dispatch({
          type: "ENTRY_APPENDED",
          entry: { input: raw, kind: "error", text: result.usage },
        });
        return;
      }
      if (result.command.verb === "exit") {
        dispatch({
          type: "ENTRY_APPENDED",
          entry: {
            input: raw,
            kind: "info",
            text: "(exit is a no-op in the web CLI; close the tab to leave)",
          },
        });
        return;
      }
      if (result.command.verb === "help") {
        dispatch({
          type: "ENTRY_APPENDED",
          entry: { input: raw, kind: "info", text: HELP_TEXT },
        });
        return;
      }
      if (isDestructive(result.command) && !state.skipConfirm) {
        dispatch({
          type: "PENDING_SET",
          pending: { command: result.command, rendered: raw },
        });
        return;
      }
      await runCommand(raw, result.command);
    },
    [runCommand, state.skipConfirm],
  );

  const submit = useCallback(() => {
    void runRaw(state.input);
  }, [runRaw, state.input]);

  const setInput = useCallback((value: string) => {
    dispatch({ type: "INPUT_CHANGED", value });
  }, []);

  const historyPrev = useCallback(() => {
    dispatch({ type: "HISTORY_PREV" });
  }, []);

  const historyNext = useCallback(() => {
    dispatch({ type: "HISTORY_NEXT" });
  }, []);

  /**
   * Resets the scrollback to just the banner line. Wired to the toolbar
   * `Clear` button and `Ctrl+L` / `Cmd+L` (#433). Gateway override,
   * skipConfirm, and history are deliberately preserved so a clear
   * behaves like an editor's "clear screen", not a hard reset.
   */
  const clearScrollback = useCallback(() => {
    dispatch({ type: "SCROLLBACK_CLEARED" });
  }, []);

  /**
   * Aborts the in-flight dispatch, if any. Wired to the toolbar
   * `Cancel` button and `Esc` (#433). The `runCommand` catch handler
   * renders the `aborted` scrollback line.
   */
  const cancelInFlight = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const setSkipConfirm = useCallback((value: boolean) => {
    dispatch({ type: "SKIP_CONFIRM_CHANGED", value });
  }, []);

  const confirmRun = useCallback(() => {
    const p = state.pending;
    dispatch({ type: "PENDING_CLEARED" });
    if (p) {
      void runCommand(p.rendered, p.command);
    }
  }, [runCommand, state.pending]);

  const confirmCancel = useCallback(() => {
    dispatch({ type: "PENDING_CLEARED" });
  }, []);

  // Window-level keyboard shortcuts (#433):
  //   - Esc while a command is in flight aborts the dispatch. The prompt
  //     `<Input>` is `disabled` while busy and so cannot receive its own
  //     keydown events; binding at window scope is the only way to make
  //     Esc reach `cancelInFlight`.
  //   - Ctrl+L / Cmd+L clears the scrollback. Bound globally so it works
  //     whether the prompt has focus or not (matches xterm / bash).
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "l") {
        e.preventDefault();
        clearScrollback();
        return;
      }
      if (e.key === "Escape" && busy) {
        e.preventDefault();
        cancelInFlight();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [busy, cancelInFlight, clearScrollback]);

  // Completion vocabulary = mount scan ∪ keys currently on the canvas.
  // The canvas keys keep completion fresh after writes the mount scan
  // never saw (e.g. a `put vertex` issued this session).
  const knownKeys = useMemo<string[]>(() => {
    const keys = new Set<string>(scannedKeys);
    for (const node of state.latestGraph?.view.nodes ?? []) {
      keys.add(node.id);
    }
    return Array.from(keys).sort((a, b) => a.localeCompare(b));
  }, [scannedKeys, state.latestGraph]);

  return useMemo<UseCliResult>(
    () => ({
      scrollback: state.scrollback,
      input: state.input,
      pending: state.pending,
      skipConfirm: state.skipConfirm,
      busy,
      latestGraph: state.latestGraph,
      knownKeys,
      setInput,
      submit,
      runRaw: (raw: string) => void runRaw(raw),
      historyPrev,
      historyNext,
      clearScrollback,
      cancelInFlight,
      setSkipConfirm,
      confirmRun,
      confirmCancel,
    }),
    [
      state.scrollback,
      state.input,
      state.pending,
      state.skipConfirm,
      busy,
      state.latestGraph,
      knownKeys,
      setInput,
      submit,
      runRaw,
      historyPrev,
      historyNext,
      clearScrollback,
      cancelInFlight,
      setSkipConfirm,
      confirmRun,
      confirmCancel,
    ],
  );
}

function replacer(_key: string, value: unknown): unknown {
  if (typeof value === "bigint") {
    return String(value);
  }
  return value;
}

function errorMessage(err: unknown): string {
  if (err instanceof LanternApiError) {
    return `[${err.code}] ${err.grpcMessage || err.message}`;
  }
  if (err instanceof Error) {
    return err.message;
  }
  return String(err);
}

/**
 * Distinguishes a deliberate cancellation (from `AbortController.abort`)
 * from a genuine RPC failure. The Connect SDK and `fetch` both raise
 * `DOMException`/`Error` instances whose `name` is `"AbortError"` —
 * `error.ts` deliberately lets these through unwrapped so this check
 * works without needing a `LanternApiError` adapter.
 */
function isAbortError(err: unknown): boolean {
  return (
    typeof err === "object" &&
    err !== null &&
    "name" in err &&
    (err as { name?: string }).name === "AbortError"
  );
}
