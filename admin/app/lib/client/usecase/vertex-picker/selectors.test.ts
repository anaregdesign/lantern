import { describe, expect, it } from "bun:test";
import { formatMatchCount, selectCaption } from "./selectors";
import { INITIAL_VERTEX_PICKER_STATE, type VertexPickerState } from "./state";

describe("formatMatchCount", () => {
  it("uses the singular noun for exactly one match", () => {
    expect(formatMatchCount(1)).toBe("1 match");
  });

  it("groups digits with underscores", () => {
    expect(formatMatchCount(0)).toBe("0 matches");
    expect(formatMatchCount(42)).toBe("42 matches");
    expect(formatMatchCount(1234)).toBe("1_234 matches");
    expect(formatMatchCount(1234567)).toBe("1_234_567 matches");
  });

  it("renders the clamp ceiling with a trailing +", () => {
    expect(formatMatchCount(Number.MAX_SAFE_INTEGER)).toBe(
      "9_007_199_254_740_991+ matches",
    );
  });
});

describe("selectCaption", () => {
  function state(partial: Partial<VertexPickerState>): VertexPickerState {
    return { ...INITIAL_VERTEX_PICKER_STATE, ...partial };
  }

  it("prompts the user when the prefix is empty", () => {
    expect(selectCaption(state({ prefix: "" }))).toBe(
      "Type at least 1 character to search.",
    );
  });

  it("surfaces the error message on a failed scan", () => {
    expect(
      selectCaption(
        state({ prefix: "x", status: "error", error: "unavailable" }),
      ),
    ).toBe("unavailable");
  });

  it("reports the match count once known", () => {
    expect(
      selectCaption(state({ prefix: "x", status: "ready", matchCount: 3 })),
    ).toBe("3 matches");
  });

  it("shows a settling label while the count is still pending", () => {
    expect(
      selectCaption(
        state({ prefix: "x", status: "loading", matchCount: null }),
      ),
    ).toBe("Searching…");
  });
});
