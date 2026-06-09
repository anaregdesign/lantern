import { describe, expect, it } from "bun:test";
import {
  decideLayoutRegime,
  diffRenderSets,
  type LayoutRegime,
} from "./reconcile";

const s = (...ids: string[]): Set<string> => new Set(ids);

describe("diffRenderSets", () => {
  it("counts a cold mount (empty → populated) as all added", () => {
    const diff = diffRenderSets(s(), s("a", "b"), s(), s("a→b"));
    expect(diff.addedCount).toBe(2);
    expect(diff.droppedCount).toBe(0);
    expect(diff.edgeSetChanged).toBe(true);
  });

  it("reports no delta when both sets are identical", () => {
    const diff = diffRenderSets(s("a", "b"), s("a", "b"), s("a→b"), s("a→b"));
    expect(diff.addedCount).toBe(0);
    expect(diff.droppedCount).toBe(0);
    expect(diff.edgeSetChanged).toBe(false);
  });

  it("counts a single added node", () => {
    const diff = diffRenderSets(s("a"), s("a", "b"), s(), s());
    expect(diff.addedCount).toBe(1);
    expect(diff.droppedCount).toBe(0);
  });

  it("counts a single dropped node", () => {
    const diff = diffRenderSets(s("a", "b"), s("a"), s(), s());
    expect(diff.addedCount).toBe(0);
    expect(diff.droppedCount).toBe(1);
  });

  it("counts a swap (one added, one dropped) independently", () => {
    const diff = diffRenderSets(s("a", "b"), s("a", "c"), s(), s());
    expect(diff.addedCount).toBe(1);
    expect(diff.droppedCount).toBe(1);
  });

  it("flags an edge-only change among unchanged nodes (#500)", () => {
    const diff = diffRenderSets(
      s("a", "b"),
      s("a", "b"),
      s("a→b"),
      s("a→b", "b→a"),
    );
    expect(diff.addedCount).toBe(0);
    expect(diff.droppedCount).toBe(0);
    expect(diff.edgeSetChanged).toBe(true);
  });

  it("detects an equal-size edge set with different membership", () => {
    // Same size (1) but different id: the size short-circuit must NOT
    // mask the membership change.
    const diff = diffRenderSets(s("a"), s("a"), s("a→b"), s("a→c"));
    expect(diff.edgeSetChanged).toBe(true);
  });

  it("reports edgeSetChanged=false for an equal, identical edge set", () => {
    const diff = diffRenderSets(
      s("a"),
      s("a"),
      s("a→b", "b→c"),
      s("b→c", "a→b"),
    );
    expect(diff.edgeSetChanged).toBe(false);
  });
});

describe("decideLayoutRegime", () => {
  const regime = (
    over: Partial<Parameters<typeof decideLayoutRegime>[0]>,
  ): LayoutRegime =>
    decideLayoutRegime({
      nextNodeCount: 0,
      forceNodeCount: 0,
      addedCount: 0,
      droppedCount: 0,
      edgeSetChanged: false,
      ...over,
    });

  it("returns 'cold' for a from-scratch mount (no survivors)", () => {
    expect(regime({ nextNodeCount: 3, forceNodeCount: 3, addedCount: 3 })).toBe(
      "cold",
    );
  });

  it("returns 'cold' for a full swap that retains no survivors", () => {
    // prev {a,b} → next {c,d}: added 2, dropped 2, survivorCount 0.
    expect(
      regime({
        nextNodeCount: 2,
        forceNodeCount: 2,
        addedCount: 2,
        droppedCount: 2,
      }),
    ).toBe("cold");
  });

  it("falls through to 'static' when a cold start rendered no nodes", () => {
    // All-new render set but every vertex filtered out (forceNodeCount 0):
    // the original guard `isColdStart && forceNodes.length > 0` excludes it.
    expect(regime({ nextNodeCount: 2, forceNodeCount: 0, addedCount: 2 })).toBe(
      "static",
    );
  });

  it("returns 'incremental' when a node is added alongside survivors", () => {
    expect(regime({ nextNodeCount: 4, forceNodeCount: 4, addedCount: 1 })).toBe(
      "incremental",
    );
  });

  it("returns 'incremental' on a drop-only delta with survivors", () => {
    expect(
      regime({ nextNodeCount: 2, forceNodeCount: 2, droppedCount: 1 }),
    ).toBe("incremental");
  });

  it("returns 'incremental' on an edge-only change with survivors (#500)", () => {
    expect(
      regime({ nextNodeCount: 3, forceNodeCount: 3, edgeSetChanged: true }),
    ).toBe("incremental");
  });

  it("returns 'static' for a pure attribute/weight refresh", () => {
    expect(regime({ nextNodeCount: 3, forceNodeCount: 3 })).toBe("static");
  });

  it("returns 'static' for an empty graph (Clear)", () => {
    expect(regime({ nextNodeCount: 0, forceNodeCount: 0 })).toBe("static");
  });
});
