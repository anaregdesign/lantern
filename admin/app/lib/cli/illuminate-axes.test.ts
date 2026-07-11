import { describe, expect, test } from "bun:test";

import {
  AXIS_STORAGE_KEYS,
  CLI_CLICK_AXIS_DEFAULTS,
  defaultCliClickPickerState,
  formatFamilyClick,
  migrateLegacyCliClickPickerState,
  parseStoredCliClickPickerState,
  replaceCliClickFamilyAxes,
  selectCliClickFamily,
  serialiseCliClickPickerState,
  validateEpsilonInput,
  validateRestartProbInput,
} from "./illuminate-axes";
import { parse } from "./parser";

describe("family-native click formatter", () => {
  test("uses each parser family's documented default and zero sentinel", () => {
    expect(formatFamilyClick("alice", CLI_CLICK_AXIS_DEFAULTS.bfs)).toBe(
      "bfs alice 5 3",
    );
    expect(formatFamilyClick("alice", CLI_CLICK_AXIS_DEFAULTS.pagerank)).toBe(
      "pagerank alice 10",
    );
    expect(formatFamilyClick("alice", CLI_CLICK_AXIS_DEFAULTS.community)).toBe(
      "community alice 0",
    );
  });

  test("emits only family-native BFS fields", () => {
    const command = formatFamilyClick("alice", {
      ...CLI_CLICK_AXIS_DEFAULTS.bfs,
      step: 3,
      fanOut: 8,
      reduction: "spt",
      objective: "min",
      weighting: "tfidf",
      vertexPrefix: "team:blue",
    });
    expect(command).toBe(
      "bfs alice 3 8 reduction=spt objective=min weighting=tfidf prefix=team:blue",
    );
    expect(parse(command)).toEqual({
      ok: true,
      command: {
        verb: "bfs",
        seed: "alice",
        step: 3,
        fanOut: 8,
        reduction: "spt",
        objective: "min",
        weighting: "tfidf",
        vertexPrefix: "team:blue",
      },
    });
  });

  test("emits PageRank top_n=0 and push knobs without tree fields", () => {
    const command = formatFamilyClick("alice", {
      ...CLI_CLICK_AXIS_DEFAULTS.pagerank,
      topN: 0,
      restartProb: 0.25,
      epsilon: 0.001,
      weighting: "bm25",
    });
    expect(command).toBe(
      "pagerank alice 0 weighting=bm25 restart_prob=0.25 epsilon=0.001",
    );
    const result = parse(command);
    expect(result.ok).toBe(true);
    if (!result.ok || result.command.verb !== "pagerank") return;
    expect(result.command.topN).toBe(0);
    expect(result.command.restartProb).toBe(0.25);
    expect(result.command.epsilon).toBeCloseTo(0.001, 7);
  });

  test("emits community max_size=0 and its optional tree fields", () => {
    const command = formatFamilyClick("alice", {
      ...CLI_CLICK_AXIS_DEFAULTS.community,
      maxSize: 0,
      reduction: "mst",
      objective: "min",
      restartProb: 0.15,
      epsilon: 0.0001,
    });
    expect(command).toBe(
      "community alice 0 reduction=mst objective=min restart_prob=0.15 epsilon=0.0001",
    );
    const result = parse(command);
    expect(result.ok).toBe(true);
    if (!result.ok || result.command.verb !== "community") return;
    expect(result.command.maxSize).toBe(0);
    expect(result.command.reduction).toBe("mst");
  });

  test("quotes a free-text seed and prefix as one token", () => {
    const command = formatFamilyClick("audit key", {
      ...CLI_CLICK_AXIS_DEFAULTS.bfs,
      vertexPrefix: "Users/Alice Smith",
    });
    const result = parse(command);
    expect(result.ok).toBe(true);
    if (!result.ok || result.command.verb !== "bfs") return;
    expect(result.command.seed).toBe("audit key");
    expect(result.command.vertexPrefix).toBe("Users/Alice Smith");
  });
});

describe("family picker state", () => {
  test("preserves each family's last values across switches", () => {
    let state = defaultCliClickPickerState();
    state = replaceCliClickFamilyAxes(state, {
      ...state.families.bfs,
      step: 4,
      fanOut: 9,
    });
    state = selectCliClickFamily(state, "pagerank");
    state = replaceCliClickFamilyAxes(state, {
      ...state.families.pagerank,
      topN: 0,
      restartProb: 0.2,
    });
    state = selectCliClickFamily(state, "community");
    state = replaceCliClickFamilyAxes(state, {
      ...state.families.community,
      maxSize: 7,
      reduction: "spt",
    });
    state = selectCliClickFamily(state, "bfs");

    expect(state.families.bfs).toMatchObject({ step: 4, fanOut: 9 });
    expect(state.families.pagerank).toMatchObject({
      topN: 0,
      restartProb: 0.2,
    });
    expect(state.families.community).toMatchObject({
      maxSize: 7,
      reduction: "spt",
    });
  });

  test("refuses a stale update from a no-longer-selected family", () => {
    const state = selectCliClickFamily(
      defaultCliClickPickerState(),
      "pagerank",
    );
    const next = replaceCliClickFamilyAxes(state, {
      ...state.families.bfs,
      step: 1,
    });
    expect(next).toBe(state);
  });

  test("round-trips the versioned snapshot including typed zero sentinels", () => {
    const state = defaultCliClickPickerState();
    const restored = parseStoredCliClickPickerState(
      serialiseCliClickPickerState(state),
    );
    expect(restored).toEqual(state);
  });

  test("migrates legacy k only to the selected family", () => {
    const values = new Map<string, string>([
      [AXIS_STORAGE_KEYS.algorithm, "pagerank"],
      [AXIS_STORAGE_KEYS.step, "4"],
      [AXIS_STORAGE_KEYS.k, "17"],
      [AXIS_STORAGE_KEYS.reduction, "mst"],
      [AXIS_STORAGE_KEYS.objective, "min"],
      [AXIS_STORAGE_KEYS.weighting, "tfidf"],
      [AXIS_STORAGE_KEYS.vertexPrefix, "legacy:"],
      [AXIS_STORAGE_KEYS.restartProb, "0.2"],
      [AXIS_STORAGE_KEYS.epsilon, "0.001"],
    ]);
    const migrated = migrateLegacyCliClickPickerState({
      get: (key) => values.get(key) ?? null,
    });

    expect(migrated.selectedFamily).toBe("pagerank");
    expect(migrated.families.bfs).toMatchObject({ step: 4, fanOut: 3 });
    expect(migrated.families.pagerank).toMatchObject({
      topN: 17,
      restartProb: 0.2,
      epsilon: 0.001,
    });
    expect(migrated.families.community).toMatchObject({ maxSize: 0 });
  });
});

describe("push-knob validation", () => {
  test("accepts blank server defaults and valid float32 values", () => {
    expect(validateRestartProbInput("")).toEqual({
      state: "default",
      value: 0,
    });
    expect(validateRestartProbInput("0.15")).toEqual({
      state: "valid",
      value: 0.15,
    });
    expect(validateEpsilonInput("1e-4")).toEqual({
      state: "valid",
      value: 0.0001,
    });
  });

  test("keeps incomplete drafts separate from invalid values", () => {
    expect(validateRestartProbInput("0.").state).toBe("incomplete");
    expect(validateEpsilonInput("1e-").state).toBe("incomplete");
    expect(validateRestartProbInput("1").state).toBe("invalid");
    expect(validateEpsilonInput("1e-50").state).toBe("invalid");
  });
});
