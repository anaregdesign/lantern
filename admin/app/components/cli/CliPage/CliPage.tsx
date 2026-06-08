import {
  Button,
  Checkbox,
  Input,
  type InputProps,
} from "@fluentui/react-components";
import { useCallback, useEffect, useMemo, useRef } from "react";
import { formatIlluminateClick } from "~/lib/cli/illuminate-axes";
import { useCli } from "~/lib/client/usecase/cli/use-cli";
import { useCliSplitter } from "~/lib/client/usecase/cli/use-cli-splitter";
import { useCliAxisPicker } from "~/lib/client/usecase/cli/use-cli-axis-picker";
import { CliAxisPicker } from "~/components/cli/CliAxisPicker/CliAxisPicker";
import { IlluminateCanvas } from "~/components/illuminate/IlluminateCanvas/IlluminateCanvas";
import styles from "./CliPage.module.css";

/**
 * The /cli admin route. A Fluent UI command-line panel shared with the
 * Go REPL via the `lib/cli/parser` TypeScript port (#411).
 *
 * This component is render-only: the stateful dispatch loop (parsing,
 * per-verb dispatch, history, destructive confirmation, cancellation,
 * graph projection) lives in the `useCli` controller hook (#494). The
 * splitter and axis-picker keep their own feature-local hooks.
 */
export function CliPage() {
  const cli = useCli();
  const scrollRef = useRef<HTMLDivElement>(null);
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

  // Auto-scroll the scrollback to the bottom on every new entry so the
  // operator always sees their most recent output without chasing the
  // panel's scrollbar.
  useEffect(() => {
    const el = scrollRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [cli.scrollback]);

  // Click-to-illuminate (#439, #464). Writes the picker-formatted
  // illuminate command into the prompt and submits it through the same
  // parser path the user would hit by typing it. Illuminate is
  // non-destructive, so the confirmation chip never fires here. With the
  // picker at its defaults the formatter emits the canonical short form
  // `illuminate <key> 2 5` (regression guard).
  const onNodeClick = useCallback(
    (key: string) => {
      if (cli.busy || cli.pending !== null) return;
      cli.runRaw(formatIlluminateClick(key, axisPicker.axes));
    },
    [cli, axisPicker.axes],
  );

  const onKeyDown = useCallback(
    (e: React.KeyboardEvent<HTMLInputElement>) => {
      // Esc / Ctrl+L are owned by the window-level handler in `useCli`
      // so they work even while the input is `disabled` (busy state) or
      // without focus. Enter / ArrowUp / ArrowDown stay local because
      // they read and write input state.
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

  const onChange: InputProps["onChange"] = (_e, data) => {
    cli.setInput(data.value);
  };

  const renderedScrollback = useMemo(
    () =>
      cli.scrollback.map((entry) => (
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
      {cli.latestGraph !== null ? (
        <div className={styles.leftColumn} data-testid="cli-left-column">
          <div className={styles.canvasPanel} data-testid="cli-canvas-panel">
            <div className={styles.canvasHeader}>
              <span className={styles.canvasHeaderLabel}>from:</span>{" "}
              <code className={styles.canvasHeaderSource}>
                {cli.latestGraph.source}
              </code>
              <span className={styles.canvasHeaderHint}>
                click any node to run{" "}
                <code data-testid="cli-click-hint">
                  {formatIlluminateClick("<key>", axisPicker.axes)}
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
              />
            </div>
          </div>
        </div>
      ) : null}
      {cli.latestGraph !== null ? (
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
            onClick={cli.clearScrollback}
            disabled={cli.scrollback.length <= 1}
            data-testid="cli-clear"
            aria-label="Clear scrollback (Ctrl+L)"
            title="Clear scrollback (Ctrl+L)"
          >
            Clear
          </Button>
          {cli.busy ? (
            <Button
              appearance="secondary"
              size="small"
              onClick={cli.cancelInFlight}
              data-testid="cli-cancel"
              aria-label="Cancel in-flight command (Esc)"
              title="Cancel in-flight command (Esc)"
            >
              Cancel
            </Button>
          ) : null}
          <CliAxisPicker
            axes={axisPicker.axes}
            setAxis={axisPicker.setAxis}
            disabled={cli.busy || cli.pending !== null}
          />
        </div>
        <div className={styles.scrollback} ref={scrollRef} aria-live="polite">
          {renderedScrollback}
        </div>
        {cli.pending !== null ? (
          <div className={styles.confirmBar} data-testid="cli-confirm">
            <span className={styles.confirmText}>
              About to run: <code>{cli.pending.rendered}</code> — this mutates
              server state.
            </span>
            <Checkbox
              label="Do not ask again this session"
              checked={cli.skipConfirm}
              onChange={(_e, data) => cli.setSkipConfirm(Boolean(data.checked))}
              data-testid="cli-skip-confirm"
            />
            <Button
              appearance="secondary"
              onClick={cli.confirmCancel}
              data-testid="cli-confirm-cancel"
            >
              Cancel
            </Button>
            <Button
              appearance="primary"
              onClick={cli.confirmRun}
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
            value={cli.input}
            onChange={onChange}
            onKeyDown={onKeyDown}
            placeholder="get vertex alice    |    illuminate alice 2 5 algorithm=spt"
            disabled={cli.busy || cli.pending !== null}
            data-testid="cli-input"
            aria-label="CLI command input"
          />
        </div>
      </div>
    </div>
  );
}
