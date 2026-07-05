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
import { dispatch as dispatchCommand } from "~/lib/client/usecase/cli/dispatcher";
import {
  commandResultToGraphView,
  commandResultToGraphMerge,
} from "~/lib/client/usecase/cli/graph-view";
import { parse, type Command, type ParseResult } from "~/lib/cli/parser";
import { HELP_TEXT } from "~/lib/cli/verbs";
import { cliReducer } from "./reducer";
import {
  INITIAL_CLI_STATE,
  type LatestGraph,
  type ScrollbackEntry,
} from "./state";

/**
 * Upper bound on the number of commands a single paste may enqueue (#945).
 * A batch larger than this is refused wholesale with a scrollback error so a
 * mispaste (e.g. dropping a whole file onto the prompt) can't flood the
 * server with hundreds of RPCs. Generous enough for the seed scripts the
 * page is meant to run — the #942 barbell script is 14 lines.
 */
const MAX_PASTE_LINES = 100;

export interface UseCliResult {
  /** Durable scrollback log, ready to render. */
  scrollback: ScrollbackEntry[];
  /** Current prompt text. */
  input: string;
  /**
   * True while a dispatch is in flight. Drives the `Cancel` control and the
   * busy spinner; the prompt itself stays editable so keystrokes buffer
   * instead of dropping (#945).
   */
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
  /** Submit the current prompt text (Enter): enqueue it and clear the box. */
  submit: () => void;
  /** Enqueue an arbitrary raw line (click-to-illuminate / seed handoff). */
  runRaw: (raw: string) => void;
  /**
   * Enqueue a pasted multi-line script (#945): each non-blank line becomes a
   * queued command, run in order. Over-cap batches are refused.
   */
  enqueueScript: (lines: string[]) => void;
  /** Recall an older history entry (ArrowUp). */
  historyPrev: () => void;
  /** Move toward the newest history entry (ArrowDown). */
  historyNext: () => void;
  /** Reset the scrollback to the banner (Clear / Ctrl+L). */
  clearScrollback: () => void;
  /** Abort the in-flight dispatch (Cancel / Esc). */
  cancelInFlight: () => void;
}

/**
 * Owns the /cli route's stateful dispatch loop: command parsing,
 * per-verb dispatch through `lantern-sdk/web`, arrow-key history,
 * cancellation via `AbortController`, and the graph projection that
 * feeds the canvas.
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
        // Project graph-shaped results onto the canvas. A read verb
        // returns a full view that REPLACES the frame; a mutating verb
        // (put/add) returns null here but yields a GraphMerge that is
        // folded onto the live frame (#518) so the new element shows up
        // without discarding the operator's exploration context. delete /
        // exit yield neither and leave the canvas untouched.
        const view = commandResultToGraphView(command, out);
        if (view !== null) {
          dispatch({
            type: "GRAPH_UPDATED",
            graph: { source: rawInput, view },
          });
        } else {
          const merge = commandResultToGraphMerge(command);
          if (merge !== null) {
            dispatch({ type: "GRAPH_MERGED", source: rawInput, merge });
          }
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
          // Esc / Cancel mid-script stops the whole batch, not just this
          // line — discard anything still queued behind it (#945).
          dispatch({ type: "QUEUE_CLEARED" });
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
          // Fail-fast: a server-rejected line drops the rest of the queued
          // script so it can't keep mutating the graph (#945).
          dispatch({ type: "RUN_FAILED" });
        }
      } finally {
        abortRef.current = null;
        dispatch({ type: "RUN_SETTLED" });
      }
    },
    [client],
  );

  // Executes one accepted line end-to-end: records it in `history` (at run
  // time, so history order === execution order — queued lines join when they
  // drain, not when they were typed), then either renders a local response
  // (exit / help / parse error) or dispatches the parsed verb through the RPC
  // path. This is the queue's single execution site — only the drain effect
  // below calls it, so there is exactly one place a command actually runs.
  const execute = useCallback(
    async (raw: string) => {
      if (raw.trim() === "") return;
      dispatch({ type: "HISTORY_APPENDED", raw });
      const result: ParseResult = parse(raw);
      if (!result.ok) {
        dispatch({
          type: "ENTRY_APPENDED",
          entry: { input: raw, kind: "error", text: result.usage },
        });
        // A parse error is an error too: fail-fast so a typo'd line 3 can't
        // let lines 4-14 of a pasted script run (#945).
        dispatch({ type: "RUN_FAILED" });
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
      await runCommand(raw, result.command);
    },
    [runCommand],
  );

  // Drain loop (#945): the sole driver of the pending-command queue.
  // Whenever the dispatch loop is idle and a command is waiting, pull the
  // head and run it — exactly one RPC in flight at a time, in FIFO order.
  // A successful run leaves the tail intact so the next commit drains the
  // next line; a cancel (QUEUE_CLEARED) or error (RUN_FAILED) empties the
  // queue, so the loop stops without any separate "keep going?" flag.
  // Routing every submission through enqueue → this effect (rather than
  // running the first line inline) keeps a synchronous paste loop race-free:
  // the effect always observes committed state, never a stale closure.
  useEffect(() => {
    if (state.phase !== "idle") return;
    if (state.queue.length === 0) return;
    const head = state.queue[0];
    dispatch({ type: "DEQUEUE" });
    void execute(head);
  }, [state.phase, state.queue, execute]);

  // Enter on the prompt. The line is queued (not run inline) so pressing
  // Enter while a command is in flight buffers it instead of dropping the
  // keystrokes — the prompt is no longer `disabled` while busy (#945). The
  // prompt clears now (the Enter affordance); the queued line re-enters
  // history when it actually runs, so the operator can keep typing the next
  // command without losing either one.
  const submit = useCallback(() => {
    if (state.input.trim() === "") return;
    dispatch({ type: "ENQUEUE", input: state.input });
    dispatch({ type: "PROMPT_CLEARED" });
  }, [state.input]);

  // Enqueue an arbitrary raw line without touching the prompt: click-to-
  // illuminate (#439), the `?seed=` handoff (#651), and each pasted script
  // line. Same buffering as `submit`, minus the prompt clear.
  const runRaw = useCallback((raw: string) => {
    if (raw.trim() === "") return;
    dispatch({ type: "ENQUEUE", input: raw });
  }, []);

  // Paste-as-script (#945): enqueue a batch of lines in order. Blank lines
  // are dropped; a batch over the cap is refused wholesale with a scrollback
  // error so a mispaste can't hammer the server. The first line runs as soon
  // as the drain effect sees the idle prompt; the rest follow in FIFO order.
  const enqueueScript = useCallback((lines: string[]) => {
    const cleaned = lines
      .map((line) => line.trim())
      .filter((line) => line !== "");
    if (cleaned.length === 0) return;
    if (cleaned.length > MAX_PASTE_LINES) {
      dispatch({
        type: "ENTRY_APPENDED",
        entry: {
          input: "",
          kind: "error",
          text: `paste rejected: ${cleaned.length} lines exceeds the ${MAX_PASTE_LINES}-line limit`,
        },
      });
      return;
    }
    for (const line of cleaned) {
      dispatch({ type: "ENQUEUE", input: line });
    }
  }, []);

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
   * `Clear` button and `Ctrl+L` / `Cmd+L` (#433). Gateway override and
   * history are deliberately preserved so a clear behaves like an
   * editor's "clear screen", not a hard reset.
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

  // Window-level keyboard shortcuts (#433):
  //   - Esc while a command is in flight aborts the dispatch. Bound at
  //     window scope so it fires regardless of where focus sits (the prompt,
  //     the axis picker, or nowhere) — and, historically, so it worked even
  //     while the prompt was `disabled`. The prompt is now always editable
  //     (#945), but the window binding stays: it's still the simplest way to
  //     reach `cancelInFlight` no matter the focus target, and it clears the
  //     pending-command queue via the abort path.
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
      busy,
      latestGraph: state.latestGraph,
      knownKeys,
      setInput,
      submit,
      runRaw,
      enqueueScript,
      historyPrev,
      historyNext,
      clearScrollback,
      cancelInFlight,
    }),
    [
      state.scrollback,
      state.input,
      busy,
      state.latestGraph,
      knownKeys,
      setInput,
      submit,
      runRaw,
      enqueueScript,
      historyPrev,
      historyNext,
      clearScrollback,
      cancelInFlight,
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
