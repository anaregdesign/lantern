import { describe, expect, it } from "bun:test";
import type { GraphView } from "~/lib/client/usecase/illuminate/selectors";
import { buildTraversalResultModel } from "./traversal-result";

const VIEW: GraphView = {
  nodes: [
    {
      id: "seed",
      label: "seed",
      vertex: { key: "seed" },
      isInitialSeed: true,
      isExpansionOrigin: true,
      importance: 1,
      firstSeenExpansion: 0,
      hopDistance: 0,
    },
    {
      id: "bravo",
      label: "bravo",
      vertex: { key: "bravo" },
      isInitialSeed: false,
      isExpansionOrigin: false,
      importance: 0.5,
      firstSeenExpansion: 0,
      hopDistance: 1,
    },
    {
      id: "alpha",
      label: "alpha",
      vertex: { key: "alpha" },
      isInitialSeed: false,
      isExpansionOrigin: false,
      importance: 0.5,
      firstSeenExpansion: 0,
      hopDistance: Number.POSITIVE_INFINITY,
    },
  ],
  edges: [
    {
      id: "seed→bravo",
      source: "seed",
      target: "bravo",
      weight: 0.2,
      edge: { tail: "seed", head: "bravo", weight: 0.2 },
    },
    {
      id: "seed→alpha",
      source: "seed",
      target: "alpha",
      weight: 0.8,
      edge: { tail: "seed", head: "alpha", weight: 0.8 },
    },
  ],
  latestExpansionOrigin: "seed",
  expansionOrigins: ["seed"],
  overSoftCap: false,
  latestResultVertexKeys: new Set(),
  latestResultEdgeIds: new Set(),
};

describe("buildTraversalResultModel", () => {
  it("keeps executed BFS metadata separate from the canvas projection", () => {
    const result = buildTraversalResultModel(
      {
        command: {
          verb: "bfs",
          seed: "seed",
          step: 3,
          fanOut: 7,
          reduction: "spt",
          objective: "min",
          weighting: "tfidf",
          vertexPrefix: "team:",
        },
      },
      VIEW,
    );
    expect(result.familyLabel).toBe("BFS");
    expect(result.summary).toContainEqual({ label: "Step", value: "3" });
    expect(result.summary).toContainEqual({
      label: "Vertex prefix",
      value: "team:",
    });
    expect(result.vertices.map((row) => row.key)).toEqual([
      "alpha",
      "bravo",
      "seed",
    ]);
  });

  it("ranks PageRank mass descending and breaks ties by key", () => {
    const result = buildTraversalResultModel(
      {
        command: {
          verb: "pagerank",
          seed: "seed",
          topN: 2,
          restartProb: 0.25,
          epsilon: 0.001,
          weighting: "raw",
          vertexPrefix: "",
        },
      },
      VIEW,
    );
    expect(result.familyLabel).toBe("Personalized PageRank");
    expect(result.pageRank).toEqual([
      { rank: 1, key: "alpha", mass: 0.8 },
      { rank: 2, key: "bravo", mass: 0.2 },
    ]);
  });

  it("describes the LocalCommunity max-size sentinel and reduction", () => {
    const result = buildTraversalResultModel(
      {
        command: {
          verb: "community",
          seed: "seed",
          maxSize: 0,
          restartProb: 0,
          epsilon: 0,
          reduction: "mst",
          objective: "max",
          weighting: "bm25",
          vertexPrefix: "",
        },
      },
      VIEW,
    );
    expect(result.familyLabel).toBe("Local community");
    expect(result.summary).toContainEqual({
      label: "Max size",
      value: "unbounded",
    });
    expect(result.summary).toContainEqual({ label: "Reduction", value: "mst" });
  });
});
