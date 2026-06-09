import { describe, expect, it } from "bun:test";
import { cliReducer } from "./reducer";
import { commandResultToGraphMerge } from "./graph-view";
import type { Command } from "~/lib/cli/types";
import {
  INITIAL_CLI_STATE,
  initialBanner,
  type CliState,
  type LatestGraph,
} from "./state";

function withHistory(history: string[], historyIndex: number | null): CliState {
  return { ...INITIAL_CLI_STATE, history, historyIndex };
}

const GRAPH: LatestGraph = {
  source: "get vertex alice",
  view: {
    nodes: [],
    edges: [],
    latestExpansionOrigin: null,
    expansionOrigins: [],
    overSoftCap: false,
    latestResultVertexKeys: new Set<string>(),
    latestResultEdgeIds: new Set<string>(),
  },
};

describe("cliReducer", () => {
  describe("INPUT_CHANGED", () => {
    it("updates the prompt text", () => {
      const next = cliReducer(INITIAL_CLI_STATE, {
        type: "INPUT_CHANGED",
        value: "get vertex a",
      });
      expect(next.input).toBe("get vertex a");
    });

    it("is identity when the value is unchanged", () => {
      const next = cliReducer(INITIAL_CLI_STATE, {
        type: "INPUT_CHANGED",
        value: "",
      });
      expect(next).toBe(INITIAL_CLI_STATE);
    });
  });

  describe("HISTORY_PREV", () => {
    it("is a no-op when history is empty", () => {
      const next = cliReducer(INITIAL_CLI_STATE, { type: "HISTORY_PREV" });
      expect(next).toBe(INITIAL_CLI_STATE);
    });

    it("recalls the newest entry when not yet navigating", () => {
      const base = withHistory(["a", "b", "c"], null);
      const next = cliReducer(base, { type: "HISTORY_PREV" });
      expect(next.historyIndex).toBe(2);
      expect(next.input).toBe("c");
    });

    it("steps toward older entries and clamps at zero", () => {
      let state = withHistory(["a", "b", "c"], 1);
      state = cliReducer(state, { type: "HISTORY_PREV" });
      expect(state.historyIndex).toBe(0);
      expect(state.input).toBe("a");
      // Already at the oldest entry — stays put.
      state = cliReducer(state, { type: "HISTORY_PREV" });
      expect(state.historyIndex).toBe(0);
      expect(state.input).toBe("a");
    });
  });

  describe("HISTORY_NEXT", () => {
    it("is a no-op when not navigating history", () => {
      const base = withHistory(["a", "b"], null);
      const next = cliReducer(base, { type: "HISTORY_NEXT" });
      expect(next).toBe(base);
    });

    it("advances toward the newest entry", () => {
      const base = withHistory(["a", "b", "c"], 0);
      const next = cliReducer(base, { type: "HISTORY_NEXT" });
      expect(next.historyIndex).toBe(1);
      expect(next.input).toBe("b");
    });

    it("clears the prompt when stepping past the newest entry", () => {
      const base = withHistory(["a", "b"], 1);
      const next = cliReducer(base, { type: "HISTORY_NEXT" });
      expect(next.historyIndex).toBeNull();
      expect(next.input).toBe("");
    });
  });

  describe("COMMAND_SUBMITTED", () => {
    it("pushes to history, resets the cursor, and clears the prompt", () => {
      const base = withHistory(["a"], 0);
      const next = cliReducer(
        { ...base, input: "get vertex bob" },
        { type: "COMMAND_SUBMITTED", raw: "get vertex bob" },
      );
      expect(next.history).toEqual(["a", "get vertex bob"]);
      expect(next.historyIndex).toBeNull();
      expect(next.input).toBe("");
    });
  });

  describe("ENTRY_APPENDED", () => {
    it("appends with the next id and bumps the counter", () => {
      const next = cliReducer(INITIAL_CLI_STATE, {
        type: "ENTRY_APPENDED",
        entry: { input: "help", kind: "info", text: "..." },
      });
      expect(next.scrollback).toHaveLength(2);
      expect(next.scrollback[1].id).toBe(2);
      expect(next.nextEntryId).toBe(3);
    });

    it("assigns monotonic ids across multiple appends", () => {
      let state = cliReducer(INITIAL_CLI_STATE, {
        type: "ENTRY_APPENDED",
        entry: { input: "a", kind: "ok", text: "OK" },
      });
      state = cliReducer(state, {
        type: "ENTRY_APPENDED",
        entry: { input: "b", kind: "ok", text: "OK" },
      });
      expect(state.scrollback.map((e) => e.id)).toEqual([1, 2, 3]);
      expect(state.nextEntryId).toBe(4);
    });
  });

  describe("SCROLLBACK_CLEARED", () => {
    it("resets to just the banner and rewinds the id counter", () => {
      let state = cliReducer(INITIAL_CLI_STATE, {
        type: "ENTRY_APPENDED",
        entry: { input: "a", kind: "ok", text: "OK" },
      });
      state = cliReducer(state, { type: "SCROLLBACK_CLEARED" });
      expect(state.scrollback).toEqual([initialBanner()]);
      expect(state.nextEntryId).toBe(2);
    });

    it("preserves history (clear-screen, not reset)", () => {
      const base: CliState = {
        ...INITIAL_CLI_STATE,
        history: ["a", "b"],
      };
      const next = cliReducer(base, { type: "SCROLLBACK_CLEARED" });
      expect(next.history).toEqual(["a", "b"]);
    });
  });

  describe("phase transitions", () => {
    it("enters running on RUN_STARTED and idle on RUN_SETTLED", () => {
      const running = cliReducer(INITIAL_CLI_STATE, { type: "RUN_STARTED" });
      expect(running.phase).toBe("running");
      const idle = cliReducer(running, { type: "RUN_SETTLED" });
      expect(idle.phase).toBe("idle");
    });
  });

  describe("GRAPH_UPDATED", () => {
    it("replaces the latest graph view", () => {
      const next = cliReducer(INITIAL_CLI_STATE, {
        type: "GRAPH_UPDATED",
        graph: GRAPH,
      });
      expect(next.latestGraph).toBe(GRAPH);
    });
  });

  describe("GRAPH_MERGED", () => {
    const putX: Command = {
      verb: "put",
      objective: "vertex",
      key: "x",
      value: "1",
      ttlSeconds: 0,
    };
    const putY: Command = {
      verb: "put",
      objective: "vertex",
      key: "y",
      value: "1",
      ttlSeconds: 0,
    };

    it("folds a put onto an empty canvas and records the source", () => {
      const next = cliReducer(INITIAL_CLI_STATE, {
        type: "GRAPH_MERGED",
        source: "put vertex x 1",
        merge: commandResultToGraphMerge(putX)!,
      });
      expect(next.latestGraph?.source).toBe("put vertex x 1");
      expect(next.latestGraph?.view.nodes.map((n) => n.id)).toEqual(["x"]);
    });

    it("merges onto the existing frame instead of replacing it", () => {
      const first = cliReducer(INITIAL_CLI_STATE, {
        type: "GRAPH_MERGED",
        source: "put vertex x 1",
        merge: commandResultToGraphMerge(putX)!,
      });
      const second = cliReducer(first, {
        type: "GRAPH_MERGED",
        source: "put vertex y 1",
        merge: commandResultToGraphMerge(putY)!,
      });
      expect(second.latestGraph?.view.nodes.map((n) => n.id).sort()).toEqual([
        "x",
        "y",
      ]);
      expect(second.latestGraph?.source).toBe("put vertex y 1");
    });
  });
});
