import { describe, expect, it } from "bun:test";
import type { Edge, Vertex } from "~/lib/client/infrastructure/api/illuminate";
import {
  HOP_FAR_THRESHOLD,
  selectHopBucketCounts,
  selectRenderTargets,
  type GraphEdge,
  type GraphNode,
} from "./selectors";

function v(key: string): Vertex {
  return { key };
}

function e(tail: string, head: string, weight = 1): Edge {
  return { tail, head, weight };
}

describe("selectHopBucketCounts", () => {
  function node(id: string, hopDistance: number): GraphNode {
    return {
      id,
      label: id,
      vertex: v(id),
      isInitialSeed: false,
      isExpansionOrigin: false,
      importance: 0.5,
      firstSeenExpansion: 0,
      hopDistance,
    };
  }

  // The legend counts only nodes in the resolved render set; this mirrors
  // the cold-start fallback (`selectRenderTargets` returns every id when
  // there is no latest result), i.e. "count them all".
  function renderAll(nodes: GraphNode[]): Set<string> {
    return new Set(nodes.map((n) => n.id));
  }

  it("returns all five tiers in palette-ramp order, zeros included", () => {
    const buckets = selectHopBucketCounts([], new Set());
    expect(buckets).toEqual([
      { key: "origin", count: 0 },
      { key: "1hop", count: 0 },
      { key: "2hop", count: 0 },
      { key: "far", count: 0 },
      { key: "unreachable", count: 0 },
    ]);
  });

  it("buckets each finite hop distance into its own tier", () => {
    const nodes = [node("o", 0), node("a", 1), node("b", 1), node("c", 2)];
    const buckets = selectHopBucketCounts(nodes, renderAll(nodes));
    expect(buckets).toEqual([
      { key: "origin", count: 1 },
      { key: "1hop", count: 2 },
      { key: "2hop", count: 1 },
      { key: "far", count: 0 },
      { key: "unreachable", count: 0 },
    ]);
  });

  it("collapses every distance >= HOP_FAR_THRESHOLD into the far tier", () => {
    const nodes = [node("a", HOP_FAR_THRESHOLD), node("b", 4), node("c", 9)];
    const buckets = selectHopBucketCounts(nodes, renderAll(nodes));
    expect(buckets.find((b) => b.key === "far")?.count).toBe(3);
  });

  it("treats non-finite and negative distances as unreachable", () => {
    const nodes = [
      node("inf", Number.POSITIVE_INFINITY),
      node("nan", Number.NaN),
      node("neg", -1),
    ];
    const buckets = selectHopBucketCounts(nodes, renderAll(nodes));
    expect(buckets.find((b) => b.key === "unreachable")?.count).toBe(3);
  });

  it("counts only the nodes whose id is in the render set (#491)", () => {
    // `dropped` is absent from the render set and must not be tallied,
    // mirroring the canvas dropping it from the rendered frame.
    const buckets = selectHopBucketCounts(
      [node("kept", 0), node("dropped", 1)],
      new Set(["kept"]),
    );
    expect(buckets).toEqual([
      { key: "origin", count: 1 },
      { key: "1hop", count: 0 },
      { key: "2hop", count: 0 },
      { key: "far", count: 0 },
      { key: "unreachable", count: 0 },
    ]);
  });

  it("pins HOP_FAR_THRESHOLD at the #460 spec boundary of 3", () => {
    expect(HOP_FAR_THRESHOLD).toBe(3);
  });
});

describe("selectRenderTargets", () => {
  function node(id: string, hopDistance = 0): GraphNode {
    return {
      id,
      label: id,
      vertex: v(id),
      isInitialSeed: false,
      isExpansionOrigin: false,
      importance: 0.5,
      firstSeenExpansion: 0,
      hopDistance,
    };
  }

  function edge(id: string): GraphEdge {
    const [source, target] = id.split("\u2192");
    return { id, source, target, weight: 1, edge: e(source, target) };
  }

  it("renders exactly the latest result when one is present (#491)", () => {
    const targets = selectRenderTargets({
      nodes: [node("a"), node("b"), node("dropped")],
      edges: [edge("a\u2192b"), edge("a\u2192dropped")],
      latestResultVertexKeys: new Set(["a", "b"]),
      latestResultEdgeIds: new Set(["a\u2192b"]),
    });
    expect([...targets.nodeIds].sort()).toEqual(["a", "b"]);
    expect([...targets.edgeIds]).toEqual(["a\u2192b"]);
  });

  it("returns the caller's own result Set references when present", () => {
    // Reference identity matters: the reconcile stores nodeIds straight
    // into previousNodeIdsRef, so a fresh copy would defeat the diff.
    const latestResultVertexKeys = new Set(["a"]);
    const latestResultEdgeIds = new Set(["a\u2192b"]);
    const targets = selectRenderTargets({
      nodes: [node("a")],
      edges: [edge("a\u2192b")],
      latestResultVertexKeys,
      latestResultEdgeIds,
    });
    expect(targets.nodeIds).toBe(latestResultVertexKeys);
    expect(targets.edgeIds).toBe(latestResultEdgeIds);
  });

  it("falls back to the full accumulator when there is no result (cold start)", () => {
    const targets = selectRenderTargets({
      nodes: [node("a"), node("b")],
      edges: [edge("a\u2192b")],
      latestResultVertexKeys: new Set(),
      latestResultEdgeIds: new Set(),
    });
    expect([...targets.nodeIds].sort()).toEqual(["a", "b"]);
    expect([...targets.edgeIds]).toEqual(["a\u2192b"]);
  });

  it("feeds selectHopBucketCounts the same membership the canvas renders", () => {
    // End-to-end: the legend composition counts only nodes the render
    // target keeps, never a vertex the latest result dropped (#491).
    const nodes = [node("kept", 0), node("dropped", 1)];
    const { nodeIds } = selectRenderTargets({
      nodes,
      edges: [],
      latestResultVertexKeys: new Set(["kept"]),
      latestResultEdgeIds: new Set(),
    });
    const buckets = selectHopBucketCounts(nodes, nodeIds);
    const total = buckets.reduce((sum, b) => sum + b.count, 0);
    expect(total).toBe(1);
    expect(buckets.find((b) => b.key === "origin")?.count).toBe(1);
  });
});
