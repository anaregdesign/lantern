import type { Command } from "~/lib/cli/types";
import type { GraphView } from "~/lib/client/usecase/illuminate/selectors";

export type EntryKind = "ok" | "error" | "info";

export interface ScrollbackEntry {
  id: number;
  input: string;
  kind: EntryKind;
  text: string;
  durationMs?: number;
}

export interface LatestGraph {
  /** The exact CLI input that produced this graph (`get vertex alice`, …). */
  source: string;
  view: GraphView;
}

export interface PendingDestructive {
  command: Command;
  rendered: string;
}

/**
 * Lifecycle phase of the dispatch loop. `idle` is the resting state;
 * `running` covers an in-flight RPC that the `Cancel` action can abort.
 * Kept as an explicit phase (rather than a bare `busy` boolean) per the
 * skill's stateful-flow rules — the controller still exposes a derived
 * `busy` for the view.
 */
export type CliPhase = "idle" | "running";

export interface CliState {
  /** Durable scrollback log; seeded with the banner entry (id 1). */
  scrollback: ScrollbackEntry[];
  /** Current prompt text (bound to the `<Input>`). */
  input: string;
  /** Durable command history for arrow-up / arrow-down recall. */
  history: string[];
  /** Cursor into `history`; null means "not navigating history". */
  historyIndex: number | null;
  /** Set while a destructive verb awaits confirmation. */
  pending: PendingDestructive | null;
  /** Per-session "do not ask again" for destructive verbs. */
  skipConfirm: boolean;
  /** Dispatch lifecycle phase. */
  phase: CliPhase;
  /**
   * Most recent graph-producing command's view. Mutating verbs leave
   * this alone so the operator keeps their exploration context after a
   * write. Null until the first graph-producing command lands.
   */
  latestGraph: LatestGraph | null;
  /** Monotonic id source for scrollback entries. */
  nextEntryId: number;
}

/**
 * The banner printed at the top of a fresh session and after `Clear`.
 * Always carries id 1, so `nextEntryId` resets to 2 alongside it.
 */
export function initialBanner(): ScrollbackEntry {
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

export const INITIAL_CLI_STATE: CliState = {
  scrollback: [initialBanner()],
  input: "",
  history: [],
  historyIndex: null,
  pending: null,
  skipConfirm: false,
  phase: "idle",
  latestGraph: null,
  nextEntryId: 2,
};
