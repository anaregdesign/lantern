import {
  Button,
  Checkbox,
  Input,
  type InputProps,
} from "@fluentui/react-components";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { dispatch, isDestructive } from "~/lib/cli/dispatcher";
import { parse, type Command, type ParseResult } from "~/lib/cli/parser";
import { HELP_TEXT } from "~/lib/cli/verbs";
import { commandResultToGraphView } from "~/lib/cli/graph-view";
import { useCliSplitter } from "~/lib/cli/use-cli-splitter";
import { IlluminateCanvas } from "~/components/illuminate/IlluminateCanvas/IlluminateCanvas";
import type { GraphView } from "~/lib/client/usecase/illuminate/selectors";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import styles from "./CliPage.module.css";

type EntryKind = "ok" | "error" | "info";

/** Default step/k for click-to-illuminate. Matches the placeholder text. */
const CLICK_TO_ILLUMINATE = { step: 2, k: 5 } as const;

interface LatestGraph {
  /** The exact CLI input that produced this graph (`get vertex alice`, …). */
  source: string;
  view: GraphView;
}

interface ScrollbackEntry {
  id: number;
  input: string;
  kind: EntryKind;
  text: string;
  durationMs?: number;
}

interface PendingDestructive {
  command: Command;
  rendered: string;
}

/**
 * The /cli admin route. A Fluent UI command-line panel shared with
 * the Go REPL via the `lib/cli/parser` TypeScript port (#411).
 *
 * Features:
 * - Per-verb dispatch through `lantern-sdk/web` via the
 *   `lib/client/infrastructure/api` adapters.
 * - Arrow-up / arrow-down history.
 * - Confirmation chip for destructive verbs (`delete`, `put`, `add`)
 *   with a per-session "do not ask again" toggle.
 * - Per-command timing badge in the `OK (1.4ms)` style the Go REPL
 *   prints.
 */
export function CliPage() {
  const client = useLanternClient();
  const [scrollback, setScrollback] = useState<ScrollbackEntry[]>([
    initialBanner(),
  ]);
  const [input, setInput] = useState("");
  const [history, setHistory] = useState<string[]>([]);
  const [historyIndex, setHistoryIndex] = useState<number | null>(null);
  const [pending, setPending] = useState<PendingDestructive | null>(null);
  const [skipConfirm, setSkipConfirm] = useState(false);
  const [busy, setBusy] = useState(false);
  // The most recent graph-producing command's view. Mutating verbs
  // (`put`, `add`, `delete`) leave this alone so the operator keeps
  // their exploration context after a write. Null until the first
  // graph-producing command lands.
  const [latestGraph, setLatestGraph] = useState<LatestGraph | null>(null);
  const scrollRef = useRef<HTMLDivElement>(null);
  const idRef = useRef(2);
  // Backs the `Cancel` action (#433). `runCommand` populates this
  // with a fresh controller before each dispatch and clears it on
  // settle; the toolbar's `Cancel` button calls `.abort()` on it
  // while busy. The dispatcher already plumbs `signal` through every
  // underlying RPC, so the abort propagates end-to-end.
  const abortRef = useRef<AbortController | null>(null);
  // Drives the two-column grid + draggable splitter (#465). Only active
  // when a graph is present; otherwise the right column owns the full
  // width and the splitter handle is hidden by CSS.
  const rootRef = useRef<HTMLDivElement>(null);
  const splitter = useCliSplitter({
    enabled: latestGraph !== null,
    rootRef,
  });

  const append = useCallback((entry: Omit<ScrollbackEntry, "id">) => {
    setScrollback((prev) => [...prev, { ...entry, id: idRef.current++ }]);
  }, []);

  // Auto-scroll the scrollback to the bottom on every new entry so
  // the operator always sees their most recent output without
  // chasing the panel's scrollbar.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [scrollback]);
  const runCommand = useCallback(
    async (rawInput: string, command: Command) => {
      setBusy(true);
      const controller = new AbortController();
      abortRef.current = controller;
      const start = performance.now();
      try {
        const out = await dispatch({
          client,
          command,
          signal: controller.signal,
        });
        const elapsed = performance.now() - start;
        append({
          input: rawInput,
          kind: "ok",
          text:
            out === null || out === undefined
              ? "OK"
              : "OK\n" + JSON.stringify(out, replacer, 2),
          durationMs: elapsed,
        });
        // Project graph-shaped results onto the canvas. null means
        // the verb carries no graph payload (put/add/delete/exit) —
        // leave the previous canvas alone in that case.
        const view = commandResultToGraphView(command, out);
        if (view !== null) {
          setLatestGraph({ source: rawInput, view });
        }
      } catch (err) {
        const elapsed = performance.now() - start;
        // Cancellation via the Cancel button / Esc — render an
        // `info` line ("aborted") rather than the red error chip so
        // the operator can tell the difference between a failed RPC
        // and a deliberate stop (#433).
        if (isAbortError(err) || controller.signal.aborted) {
          append({
            input: rawInput,
            kind: "info",
            text: "aborted",
            durationMs: elapsed,
          });
        } else {
          append({
            input: rawInput,
            kind: "error",
            text: errorMessage(err),
            durationMs: elapsed,
          });
        }
      } finally {
        abortRef.current = null;
        setBusy(false);
      }
    },
    [append, client],
  );

  /**
   * Resets the scrollback to just the banner line. Wired to the
   * toolbar `Clear` button and `Ctrl+L` / `Cmd+L` (#433). Gateway
   * override, skipConfirm, and history are deliberately preserved
   * so a clear behaves like an editor's "clear screen", not a hard
   * reset of the session.
   */
  const clearScrollback = useCallback(() => {
    idRef.current = 2;
    setScrollback([initialBanner()]);
  }, []);

  /**
   * Aborts the in-flight dispatch, if any. Wired to the toolbar
   * `Cancel` button and `Esc` (#433). The `runCommand` catch handler
   * is responsible for rendering the `aborted` scrollback line.
   */
  const cancelInFlight = useCallback(() => {
    abortRef.current?.abort();
  }, []);

  const runRaw = useCallback(
    async (raw: string) => {
      if (raw.trim() === "") return;
      setHistory((prev) => [...prev, raw]);
      setHistoryIndex(null);
      setInput("");
      const result: ParseResult = parse(raw);
      if (!result.ok) {
        append({ input: raw, kind: "error", text: result.usage });
        return;
      }
      if (result.command.verb === "exit") {
        append({
          input: raw,
          kind: "info",
          text: "(exit is a no-op in the web CLI; close the tab to leave)",
        });
        return;
      }
      if (result.command.verb === "help") {
        append({
          input: raw,
          kind: "info",
          text: HELP_TEXT,
        });
        return;
      }
      if (isDestructive(result.command) && !skipConfirm) {
        setPending({
          command: result.command,
          rendered: raw,
        });
        return;
      }
      await runCommand(raw, result.command);
    },
    [append, runCommand, skipConfirm],
  );

  const submit = useCallback(async () => {
    await runRaw(input);
  }, [input, runRaw]);

  // Window-level keyboard shortcuts (#433):
  //   - Esc while a command is in flight aborts the dispatch. The
  //     prompt `<Input>` is `disabled` while busy and so cannot
  //     receive its own keydown events; binding at window scope
  //     is the only way to make Esc reach `cancelInFlight`.
  //   - Ctrl+L / Cmd+L clears the scrollback. Bound globally so it
  //     works whether the prompt has focus or not (matches xterm /
  //     bash behaviour).
  // The handler ignores events from text inputs, contenteditables,
  // and textareas the user might be typing in elsewhere on the
  // panel, except the prompt itself (which still calls the same
  // handlers via its onKeyDown — duplication is harmless because
  // both call sites land in idempotent setters).
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

  // Click-to-illuminate (#439). Writes `illuminate <key> 2 5` into
  // the prompt and submits it through the same parser path the user
  // would hit by typing it. Illuminate is non-destructive, so the
  // confirmation chip never fires here.
  const onNodeClick = useCallback(
    (key: string) => {
      if (busy || pending !== null) return;
      const raw = `illuminate ${key} ${CLICK_TO_ILLUMINATE.step} ${CLICK_TO_ILLUMINATE.k}`;
      setInput(raw);
      void runRaw(raw);
    },
    [busy, pending, runRaw],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      // Esc / Ctrl+L are owned by the window-level handler above so
      // they work even while the input is `disabled` (busy state) or
      // without focus. Enter / ArrowUp / ArrowDown stay local
      // because they read and write input state.
      if (e.key === "Enter") {
        e.preventDefault();
        void submit();
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        if (history.length === 0) return;
        const next =
          historyIndex === null
            ? history.length - 1
            : Math.max(0, historyIndex - 1);
        setHistoryIndex(next);
        setInput(history[next]);
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        if (historyIndex === null) return;
        const next = historyIndex + 1;
        if (next >= history.length) {
          setHistoryIndex(null);
          setInput("");
        } else {
          setHistoryIndex(next);
          setInput(history[next]);
        }
      }
    },
    [history, historyIndex, submit],
  );

  const onChange: InputProps["onChange"] = (_e, data) => {
    setInput(data.value);
  };

  const renderedScrollback = useMemo(
    () =>
      scrollback.map((entry) => (
        <div
          key={entry.id}
          className={styles.entry}
          data-testid={`cli-entry-${entry.kind}`}
        >
          {entry.input !== "" && (
            <div>
              <span className={styles.prompt}>&gt;</span>{" "}
              <span className={styles.entryInput}>{entry.input}</span>
            </div>
          )}
          <div
            className={
              entry.kind === "ok"
                ? styles.entryOk
                : entry.kind === "error"
                  ? styles.entryError
                  : undefined
            }
          >
            {entry.text}
            {entry.durationMs !== undefined ? (
              <span className={styles.entryTiming}>
                ({entry.durationMs.toFixed(1)}ms)
              </span>
            ) : null}
          </div>
        </div>
      )),
    [scrollback],
  );

  return (
    <div
      ref={rootRef}
      className={styles.root}
      data-testid="cli-root"
      data-mode={latestGraph !== null ? "split" : "cli"}
      data-dragging={splitter.dragging ? "true" : undefined}
    >
      {latestGraph !== null ? (
        <div className={styles.leftColumn} data-testid="cli-left-column">
          <div className={styles.canvasPanel} data-testid="cli-canvas-panel">
            <div className={styles.canvasHeader}>
              <span className={styles.canvasHeaderLabel}>from:</span>{" "}
              <code className={styles.canvasHeaderSource}>
                {latestGraph.source}
              </code>
              <span className={styles.canvasHeaderHint}>
                click any node to run{" "}
                <code>
                  illuminate &lt;key&gt; {CLICK_TO_ILLUMINATE.step}{" "}
                  {CLICK_TO_ILLUMINATE.k}
                </code>
              </span>
            </div>
            <div className={styles.canvasBody}>
              <IlluminateCanvas
                nodes={latestGraph.view.nodes}
                edges={latestGraph.view.edges}
                latestExpansionOrigin={latestGraph.view.latestExpansionOrigin}
                onNodeClick={onNodeClick}
                isBusy={busy}
              />
            </div>
          </div>
        </div>
      ) : null}
      {latestGraph !== null ? (
        <div
          className={styles.splitter}
          data-testid="cli-splitter"
          aria-label="Resize canvas vs scrollback"
          {...splitter.handleProps}
        />
      ) : null}
      <div className={styles.rightColumn} data-testid="cli-right-column">
        <div className={styles.toolbar} data-testid="cli-toolbar">
          <Button
            appearance="secondary"
            size="small"
            onClick={clearScrollback}
            disabled={scrollback.length <= 1}
            data-testid="cli-clear"
            aria-label="Clear scrollback (Ctrl+L)"
            title="Clear scrollback (Ctrl+L)"
          >
            Clear
          </Button>
          {busy ? (
            <Button
              appearance="secondary"
              size="small"
              onClick={cancelInFlight}
              data-testid="cli-cancel"
              aria-label="Cancel in-flight command (Esc)"
              title="Cancel in-flight command (Esc)"
            >
              Cancel
            </Button>
          ) : null}
        </div>
        <div className={styles.scrollback} ref={scrollRef} aria-live="polite">
          {renderedScrollback}
        </div>
        {pending !== null ? (
          <div className={styles.confirmBar} data-testid="cli-confirm">
            <span className={styles.confirmText}>
              About to run: <code>{pending.rendered}</code> — this mutates
              server state.
            </span>
            <Checkbox
              label="Do not ask again this session"
              checked={skipConfirm}
              onChange={(_e, data) => setSkipConfirm(Boolean(data.checked))}
              data-testid="cli-skip-confirm"
            />
            <Button
              appearance="secondary"
              onClick={() => setPending(null)}
              data-testid="cli-confirm-cancel"
            >
              Cancel
            </Button>
            <Button
              appearance="primary"
              onClick={async () => {
                const p = pending;
                setPending(null);
                if (p) {
                  await runCommand(p.rendered, p.command);
                }
              }}
              data-testid="cli-confirm-run"
            >
              Run
            </Button>
          </div>
        ) : null}
        <div className={styles.inputRow}>
          <span className={styles.prompt}>&gt;</span>
          <Input
            className={styles.input}
            value={input}
            onChange={onChange}
            onKeyDown={onKeyDown}
            placeholder="get vertex alice    |    illuminate alice 2 5 algorithm=spt"
            disabled={busy || pending !== null}
            data-testid="cli-input"
            aria-label="CLI command input"
          />
        </div>
      </div>
    </div>
  );
}

function initialBanner(): ScrollbackEntry {
  return {
    id: 1,
    input: "",
    kind: "info",
    text: [
      "Lantern admin CLI (#411). Same grammar as `lantern repl`.",
      "Type a verb and Enter; arrow-up / arrow-down cycle history.",
      'Type "help" for verb signatures.',
    ].join("\n"),
  };
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
