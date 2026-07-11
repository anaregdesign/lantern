import { Button, Spinner } from "@fluentui/react-components";
import { BookQuestionMark20Regular } from "@fluentui/react-icons";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useSearchParams } from "react-router";
import {
  CLI_CLICK_AXIS_DEFAULTS,
  formatFamilyClick,
} from "~/lib/cli/illuminate-axes";
import { completeCommandLine, longestCommonPrefix } from "~/lib/cli/complete";
import { useCli } from "~/lib/client/usecase/cli/use-cli";
import { useCliSplitter } from "~/lib/client/usecase/cli/use-cli-splitter";
import { useCliAxisPicker } from "~/lib/client/usecase/cli/use-cli-axis-picker";
import type { ScrollbackEntry } from "~/lib/client/usecase/cli/state";
import { CliAxisPicker } from "~/components/cli/CliAxisPicker/CliAxisPicker";
import { CliCommandReference } from "~/components/cli/CliCommandReference/CliCommandReference";
import { JsonView } from "~/components/cli/JsonView/JsonView";
import { IlluminateCanvas } from "~/components/illuminate/IlluminateCanvas/IlluminateCanvas";
import styles from "./CliPage.module.css";

/**
 * Decode a `?seed=` query value. The browser already percent-decodes
 * `URLSearchParams`, but the seed handoff mirrors the retired Illuminate
 * page: try one more decode (some keys arrive double-encoded) and fall
 * back to the raw value when it is already decoded.
 */
function decodeSeed(raw: string): string {
  if (raw === "") return "";
  try {
    return decodeURIComponent(raw);
  } catch {
    // Browser already decoded once; pass through unmodified.
    return raw;
  }
}

/**
 * The /cli admin route. A Fluent UI command-line panel shared with the
 * Go REPL via the `lib/cli/parser` TypeScript port (#411).
 *
 * This component is render-only: the stateful dispatch loop (parsing,
 * per-verb dispatch, history, cancellation, graph projection) lives in
 * the `useCli` controller hook (#494). The splitter and axis-picker keep
 * their own feature-local hooks.
 */
export function CliPage() {
  const cli = useCli();
  const [searchParams] = useSearchParams();
  const scrollRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  // Tab-completion candidates surfaced under the prompt when the active
  // token is ambiguous (#515). Local UI state only — never written to the
  // scrollback log, so it behaves like a shell's transient completion row.
  const [hints, setHints] = useState<string[]>([]);
  // Whether the slide-in "Commands" reference drawer is open (#646). Local
  // UI state — it neither touches the dispatch loop nor the scrollback.
  const [helpOpen, setHelpOpen] = useState(false);
  // Drives the two-column grid + draggable splitter (#465). Only active
  // when a graph is present; otherwise the right column owns the full
  // width and the splitter handle is hidden by CSS.
  const rootRef = useRef<HTMLDivElement>(null);
  const splitter = useCliSplitter({
    enabled: cli.latestGraph !== null,
    rootRef,
  });
  // Owns the click-to-illuminate axis picker strip state (#464). The
  // hook hydrates from localStorage so a refresh preserves a tuned
  // exploration session, and persists each axis change so the next page
  // load picks up where the operator left off.
  const axisPicker = useCliAxisPicker();
  const [isAxisPickerCommandValid, setIsAxisPickerCommandValid] =
    useState(true);
  const axisPickerCommandValidRef = useRef(true);
  const onPushKnobValidityChange = useCallback((valid: boolean) => {
    // Keep the ref in sync synchronously: a node click immediately after an
    // input event must not sneak through before React has rendered the Field
    // validation state.
    axisPickerCommandValidRef.current = valid;
    setIsAxisPickerCommandValid(valid);
  }, []);

  // #651 deep-link handoff: a `/cli?seed=<key>` URL (from the Vertices /
  // Edges Browse rows and the Vertex-detail toolbar) auto-fires one
  // illuminate walk for that key — the same command a canvas click emits.
  // The walk uses the canonical default axes (`bfs <seed> 2 5`) so a
  // cross-surface deep link is deterministic; the operator can re-tune and
  // re-click from the canvas afterwards. `runRaw` is captured in a ref so the
  // one-shot effect depends only on the seed string and never re-fires when
  // the controller's identity changes between renders.
  const seedParam = searchParams.get("seed") ?? "";
  const seedHandoffRef = useRef<string | null>(null);
  const runRawRef = useRef(cli.runRaw);
  useEffect(() => {
    runRawRef.current = cli.runRaw;
  }, [cli.runRaw]);
  useEffect(() => {
    const seed = decodeSeed(seedParam);
    if (seed === "") {
      seedHandoffRef.current = null;
      return;
    }
    // Fire once per distinct seed value; re-navigating to the same seed is a
    // no-op (mirrors the retired Illuminate page's lastSeedRef).
    if (seedHandoffRef.current === seed) return;
    seedHandoffRef.current = seed;
    runRawRef.current(formatFamilyClick(seed, CLI_CLICK_AXIS_DEFAULTS));
  }, [seedParam]);

  // Auto-scroll the scrollback to the bottom on every new entry so the
  // operator always sees their most recent output without chasing the
  // panel's scrollbar. The live prompt lives inside the scroll region
  // now (#515), so it follows the latest output.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [cli.scrollback]);

  // Multi-line paste is a script (#945): each line runs as its own command
  // via the controller's pending-command queue, so pasting a seed script
  // from a doc or an issue reproduces a graph state instead of the newlines
  // flattening into the single-line input. A single-line paste keeps the
  // browser default (drop the text into the prompt for the operator to edit).
  const onPaste = useCallback(
    (e: React.ClipboardEvent<HTMLInputElement>) => {
      const text = e.clipboardData.getData("text");
      if (!text.includes("\n")) return;
      e.preventDefault();
      cli.enqueueScript(text.split("\n"));
    },
    [cli],
  );

  // Click-to-illuminate (#439, #464). Writes the picker-formatted
  // illuminate command into the prompt and submits it through the same
  // parser path the user would hit by typing it. With the picker at its
  // defaults the formatter emits the canonical short form
  // `bfs <key> 2 5` (regression guard). A click while a command is
  // in flight enqueues (#945) rather than being swallowed, so the walk runs
  // as soon as the current dispatch settles.
  const onNodeClick = useCallback(
    (key: string) => {
      if (!axisPickerCommandValidRef.current) return;
      cli.runRaw(formatFamilyClick(key, axisPicker.axes));
    },
    [cli, axisPicker.axes],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      // Esc / Ctrl+L are owned by the window-level handler in `useCli` so
      // they work without focus (and, historically, while the input was
      // `disabled`). The prompt is always editable now (#945), but keeping
      // them at window scope means they still fire from anywhere. Enter /
      // ArrowUp / ArrowDown stay local because they read and write input
      // state.
      if (e.key === "Tab") {
        // Terminal-style completion (#515). Resolve the active token,
        // then either apply the sole candidate, advance to the longest
        // common prefix, or surface the ambiguous set as a hint row.
        e.preventDefault();
        e.stopPropagation();
        const { candidates, start } = completeCommandLine(
          cli.input,
          cli.knownKeys,
        );
        if (candidates.length === 1) {
          const value = candidates[0];
          // Option keys (`algorithm=`) keep the cursor on the value, so
          // no trailing space; completed words advance to the next slot.
          const suffix = value.endsWith("=") ? "" : " ";
          cli.setInput(cli.input.slice(0, start) + value + suffix);
          setHints([]);
        } else if (candidates.length > 1) {
          const lcp = longestCommonPrefix(candidates);
          const active = cli.input.slice(start);
          if (lcp.length > active.length) {
            cli.setInput(cli.input.slice(0, start) + lcp);
          }
          setHints(candidates);
        } else {
          setHints([]);
        }
        // Fluent's focus manager (Tabster) installs invisible
        // `<i tabindex="0" data-tabster-dummy>` sentinels at the
        // FluentProvider boundary and moves focus to one of them on Tab
        // from a window/document *capture-phase* handler — which runs
        // before this bubble-phase onKeyDown, so e.preventDefault() alone
        // cannot keep the caret in the prompt (#519). Restore focus and
        // the caret to the end of the input on the next frame, after
        // Tabster has settled.
        requestAnimationFrame(() => {
          const node = inputRef.current;
          if (!node) return;
          node.focus();
          const end = node.value.length;
          node.setSelectionRange(end, end);
        });
        return;
      }
      // Any other key dismisses a stale completion hint.
      setHints([]);
      if (e.key === "Enter") {
        e.preventDefault();
        cli.submit();
        return;
      }
      if (e.key === "ArrowUp") {
        e.preventDefault();
        cli.historyPrev();
        return;
      }
      if (e.key === "ArrowDown") {
        e.preventDefault();
        cli.historyNext();
      }
    },
    [cli],
  );

  const renderedScrollback = useMemo(
    () =>
      cli.scrollback.map((entry) => (
        <div
          key={entry.id}
          className={styles.entry}
          data-testid={`cli-entry-${entry.kind}`}
        >
          {entry.input !== "" ? (
            <div className={styles.entryEcho}>
              <span className={styles.prompt} aria-hidden="true">
                ❯
              </span>{" "}
              <span className={styles.entryInput}>{entry.input}</span>
            </div>
          ) : null}
          <EntryBody entry={entry} />
        </div>
      )),
    [cli.scrollback],
  );

  return (
    <div
      ref={rootRef}
      className={styles.root}
      data-testid="cli-root"
      data-mode={cli.latestGraph !== null ? "split" : "cli"}
      data-dragging={splitter.dragging ? "true" : undefined}
    >
      {/* Left column — the unified shell terminal. Always present; owns
          the full width until a graph-producing command opens the canvas. */}
      <section className={styles.terminal} data-testid="cli-terminal">
        <div className={styles.chrome}>
          <span className={styles.dots} aria-hidden="true">
            <span className={`${styles.dot} ${styles.dotRed}`} />
            <span className={`${styles.dot} ${styles.dotAmber}`} />
            <span className={`${styles.dot} ${styles.dotGreen}`} />
          </span>
          <span className={styles.chromeTitle}>lantern · cli</span>
          <span className={styles.chromeActions}>
            <Button
              appearance="subtle"
              size="small"
              icon={<BookQuestionMark20Regular />}
              onClick={() => setHelpOpen(true)}
              data-testid="cli-help-toggle"
              aria-label="Show CLI commands"
              title="Show CLI commands"
            >
              Commands
            </Button>
            {cli.busy ? (
              <span className={styles.chromeBusy}>
                <Spinner
                  size="extra-tiny"
                  label="running"
                  labelPosition="before"
                />
                <Button
                  appearance="subtle"
                  size="small"
                  onClick={cli.cancelInFlight}
                  data-testid="cli-cancel"
                  aria-label="Cancel in-flight command (Esc)"
                  title="Cancel in-flight command (Esc)"
                >
                  Cancel
                </Button>
              </span>
            ) : null}
          </span>
        </div>

        <div
          className={styles.scrollback}
          ref={scrollRef}
          aria-live="polite"
          data-testid="cli-scrollback"
          onClick={(e) => {
            // Click empty terminal space to focus the prompt (like a real
            // terminal). Guarded so clicking/selecting output text or a
            // control inside the scroll region never steals the caret.
            if (e.target === e.currentTarget) inputRef.current?.focus();
          }}
        >
          {renderedScrollback}

          {/* Live prompt — an inline terminal line that scrolls with the
              output, not a detached form (#515). Always editable, even
              while a command is in flight: Enter buffers into the
              controller's pending-command queue instead of dropping the
              keystrokes into a `disabled` input (#945). */}
          <div className={styles.promptRow}>
            <span className={styles.prompt} aria-hidden="true">
              ❯
            </span>
            <input
              ref={inputRef}
              className={styles.input}
              value={cli.input}
              onChange={(e) => {
                setHints([]);
                cli.setInput(e.target.value);
              }}
              onKeyDown={onKeyDown}
              onPaste={onPaste}
              placeholder="get vertex alice    |    bfs alice 2 5 reduction=spt"
              data-testid="cli-input"
              aria-label="CLI command input"
              autoComplete="off"
              autoCapitalize="off"
              autoCorrect="off"
              spellCheck={false}
            />
          </div>

          {hints.length > 0 ? (
            <div className={styles.hints} data-testid="cli-hints">
              {hints.map((hint) => (
                <span key={hint} className={styles.hint}>
                  {hint}
                </span>
              ))}
            </div>
          ) : null}
        </div>
      </section>

      {/* Draggable splitter — only in split mode (graph present, ≥1024px). */}
      {cli.latestGraph !== null ? (
        <div
          className={styles.splitter}
          data-testid="cli-splitter"
          aria-label="Resize terminal vs canvas"
          {...splitter.handleProps}
        />
      ) : null}

      {/* Right column — the canvas with the axis picker fused into its
          control header (#512). Mounts only when a graph is available. */}
      {cli.latestGraph !== null ? (
        <section
          className={styles.canvasColumn}
          data-testid="cli-canvas-column"
        >
          <div className={styles.canvasPanel} data-testid="cli-canvas-panel">
            <div className={styles.canvasControls}>
              <CliAxisPicker
                axes={axisPicker.axes}
                setAxis={axisPicker.setAxis}
                disabled={cli.busy}
                onPushKnobValidityChange={onPushKnobValidityChange}
              />
            </div>
            <div className={styles.canvasMeta}>
              <span className={styles.canvasMetaLabel}>from</span>
              <code className={styles.canvasMetaSource}>
                {cli.latestGraph.source}
              </code>
              <span className={styles.canvasMetaHint}>
                click a node →{" "}
                <code data-testid="cli-click-hint">
                  {isAxisPickerCommandValid
                    ? formatFamilyClick("<key>", axisPicker.axes)
                    : "Fix push-knob validation errors before clicking a node."}
                </code>
              </span>
            </div>
            <div className={styles.canvasBody}>
              <IlluminateCanvas
                nodes={cli.latestGraph.view.nodes}
                edges={cli.latestGraph.view.edges}
                latestExpansionOrigin={
                  cli.latestGraph.view.latestExpansionOrigin
                }
                latestResultVertexKeys={
                  cli.latestGraph.view.latestResultVertexKeys
                }
                latestResultEdgeIds={cli.latestGraph.view.latestResultEdgeIds}
                onNodeClick={onNodeClick}
                isBusy={cli.busy}
                fill
              />
            </div>
          </div>
        </section>
      ) : null}

      {/* Slide-in command reference (#646), toggled from the chrome
          "Commands" button. Portals over the viewport, so its position in
          the tree does not matter — it lives here to keep the toggle and
          panel state co-located. */}
      <CliCommandReference open={helpOpen} onClose={() => setHelpOpen(false)} />
    </div>
  );
}

/**
 * Render one scrollback entry's body. Successful command output is
 * formatted by `useCli` as `OK\n<json>`; that JSON gets lightweight,
 * theme-aware colouring via {@link JsonView}. Plain "OK", errors, info
 * lines, and the banner render as plain monospace text. Kept as a tiny
 * render-only subcomponent so the memoised scrollback map stays flat.
 */
function EntryBody({ entry }: { entry: ScrollbackEntry }) {
  const timing =
    entry.durationMs !== undefined ? (
      <span className={styles.entryTiming}>
        ({entry.durationMs.toFixed(1)}ms)
      </span>
    ) : null;

  if (entry.kind === "ok" && entry.text.startsWith("OK\n")) {
    return (
      <div className={styles.entryBody}>
        <div className={styles.entryOkLine}>
          <span className={styles.entryOk}>OK</span>
          {timing}
        </div>
        <JsonView source={entry.text.slice(3)} />
      </div>
    );
  }

  const cls =
    entry.kind === "ok"
      ? styles.entryOk
      : entry.kind === "error"
        ? styles.entryError
        : undefined;
  return (
    <div className={cls}>
      {entry.text}
      {timing}
    </div>
  );
}
