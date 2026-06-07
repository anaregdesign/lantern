import {
  Button,
  Checkbox,
  Input,
  type InputProps,
} from "@fluentui/react-components";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { dispatch, isDestructive } from "~/lib/cli/dispatcher";
import { parse, type Command, type ParseResult } from "~/lib/cli/parser";
import { commandResultToGraphView } from "~/lib/cli/graph-view";
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
      const start = performance.now();
      try {
        const out = await dispatch({ client, command });
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
        append({
          input: rawInput,
          kind: "error",
          text: errorMessage(err),
          durationMs: elapsed,
        });
      } finally {
        setBusy(false);
      }
    },
    [append, client],
  );

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
    <div className={styles.root} data-testid="cli-root">
      {latestGraph !== null ? (
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
              onNodeClick={onNodeClick}
              isBusy={busy}
            />
          </div>
        </div>
      ) : null}
      <div className={styles.scrollback} ref={scrollRef} aria-live="polite">
        {renderedScrollback}
      </div>
      {pending !== null ? (
        <div className={styles.confirmBar} data-testid="cli-confirm">
          <span className={styles.confirmText}>
            About to run: <code>{pending.rendered}</code> — this mutates server
            state.
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
      "Verbs: get | put | delete | add | scan | illuminate | exit",
      'Quoting: "double" with C-style escapes (\\" \\\\ \\n \\r \\t); \'single\' verbatim. Verb/objective case-insensitive; args preserve case.',
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
