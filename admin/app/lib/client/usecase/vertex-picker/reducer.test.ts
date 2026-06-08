import { describe, expect, it } from "bun:test";
import { vertexPickerReducer, type VertexPickerAction } from "./reducer";
import { INITIAL_VERTEX_PICKER_STATE, type VertexPickerState } from "./state";

function reduce(
  state: VertexPickerState,
  ...actions: VertexPickerAction[]
): VertexPickerState {
  return actions.reduce(vertexPickerReducer, state);
}

describe("vertexPickerReducer", () => {
  it("bumps the epoch and clears prior results on a new prefix", () => {
    const next = reduce(INITIAL_VERTEX_PICKER_STATE, {
      type: "PREFIX_CHANGED",
      prefix: "Distri",
    });
    expect(next.prefix).toBe("Distri");
    expect(next.prefixEpoch).toBe(1);
    expect(next.status).toBe("idle");
    expect(next.suggestions).toEqual([]);
    expect(next.matchCount).toBeNull();
  });

  it("ignores a PREFIX_CHANGED that does not change the prefix", () => {
    const seeded = reduce(INITIAL_VERTEX_PICKER_STATE, {
      type: "PREFIX_CHANGED",
      prefix: "abc",
    });
    const same = vertexPickerReducer(seeded, {
      type: "PREFIX_CHANGED",
      prefix: "abc",
    });
    // Identity preserved → no epoch bump, no re-fetch.
    expect(same).toBe(seeded);
    expect(same.prefixEpoch).toBe(1);
  });

  it("resets to the empty state (but advances epoch) when the prefix is cleared", () => {
    const seeded = reduce(
      INITIAL_VERTEX_PICKER_STATE,
      { type: "PREFIX_CHANGED", prefix: "abc" },
      { type: "SCAN_REQUESTED", epoch: 1 },
      { type: "SCAN_RECEIVED", epoch: 1, suggestions: ["abc"] },
    );
    const cleared = vertexPickerReducer(seeded, {
      type: "PREFIX_CHANGED",
      prefix: "",
    });
    expect(cleared.prefix).toBe("");
    expect(cleared.suggestions).toEqual([]);
    expect(cleared.matchCount).toBeNull();
    expect(cleared.status).toBe("idle");
    // Epoch still advances so any in-flight reply for "abc" is discarded.
    expect(cleared.prefixEpoch).toBe(2);
  });

  it("applies scan + count results that match the live epoch", () => {
    const next = reduce(
      INITIAL_VERTEX_PICKER_STATE,
      { type: "PREFIX_CHANGED", prefix: "Distri" },
      { type: "SCAN_REQUESTED", epoch: 1 },
      {
        type: "SCAN_RECEIVED",
        epoch: 1,
        suggestions: ["Distributed_computing", "Distribution"],
      },
      { type: "COUNT_RECEIVED", epoch: 1, count: 42 },
    );
    expect(next.status).toBe("ready");
    expect(next.suggestions).toEqual(["Distributed_computing", "Distribution"]);
    expect(next.matchCount).toBe(42);
  });

  it("drops a stale scan response from an abandoned prefix (epoch guard)", () => {
    // Type "ab" (epoch 1), then "abc" (epoch 2) before "ab"'s scan lands.
    const seeded = reduce(
      INITIAL_VERTEX_PICKER_STATE,
      { type: "PREFIX_CHANGED", prefix: "ab" },
      { type: "SCAN_REQUESTED", epoch: 1 },
      { type: "PREFIX_CHANGED", prefix: "abc" },
    );
    expect(seeded.prefixEpoch).toBe(2);

    // The slow reply for "ab" (epoch 1) arrives — it must be ignored.
    const afterStale = vertexPickerReducer(seeded, {
      type: "SCAN_RECEIVED",
      epoch: 1,
      suggestions: ["ab_stale"],
    });
    expect(afterStale).toBe(seeded);
    expect(afterStale.suggestions).toEqual([]);

    // The fresh reply for "abc" (epoch 2) is applied.
    const afterFresh = vertexPickerReducer(afterStale, {
      type: "SCAN_RECEIVED",
      epoch: 2,
      suggestions: ["abc_fresh"],
    });
    expect(afterFresh.suggestions).toEqual(["abc_fresh"]);
  });

  it("drops a stale count response (epoch guard)", () => {
    const seeded = reduce(
      INITIAL_VERTEX_PICKER_STATE,
      { type: "PREFIX_CHANGED", prefix: "ab" },
      { type: "PREFIX_CHANGED", prefix: "abc" },
    );
    const afterStaleCount = vertexPickerReducer(seeded, {
      type: "COUNT_RECEIVED",
      epoch: 1,
      count: 999,
    });
    expect(afterStaleCount.matchCount).toBeNull();
  });

  it("records a scan failure and clears suggestions on the live epoch", () => {
    const next = reduce(
      INITIAL_VERTEX_PICKER_STATE,
      { type: "PREFIX_CHANGED", prefix: "boom" },
      { type: "SCAN_REQUESTED", epoch: 1 },
      { type: "SCAN_RECEIVED", epoch: 1, suggestions: ["boom"] },
      { type: "SCAN_FAILED", epoch: 1, error: "unavailable" },
    );
    expect(next.status).toBe("error");
    expect(next.error).toBe("unavailable");
    expect(next.suggestions).toEqual([]);
  });
});
