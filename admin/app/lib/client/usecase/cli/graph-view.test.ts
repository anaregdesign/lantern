import { describe, expect, it } from "bun:test";
import {
  commandResultToGraphMerge,
  commandResultToGraphView,
  mergeGraphView,
} from "./graph-view";
import type { Command } from "~/lib/cli/types";
import type {
  Edge,
  IlluminateResponse,
  ScanEdgesResponse,
  ScanVerticesResponse,
  Vertex,
} from "~/lib/client/infrastructure/api/types";

describe("commandResultToGraphView — illuminate", () => {
  const cmd: Command = {
    verb: "illuminate",
    seed: "a",
    step: 2,
    k: 5,
    algorithm: "none",
    objective: "min",
    weighting: "raw",
    vertexPrefix: "",
    restartProb: 0,
    epsilon: 0,
  };

  it("marks the seed and renders all returned vertices + edges", () => {
    const response: IlluminateResponse = {
      graph: {
        vertices: [{ key: "a" }, { key: "b" }, { key: "c" }],
        edges: [
          { tail: "a", head: "b", weight: 1 },
          { tail: "a", head: "c", weight: 3 },
        ],
      },
    };
    const view = commandResultToGraphView(cmd, response)!;
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b", "c"]);
    expect(view.nodes.find((n) => n.id === "a")!.isInitialSeed).toBe(true);
    expect(view.nodes.find((n) => n.id === "a")!.importance).toBe(1);
    expect(view.edges.map((e) => e.id).sort()).toEqual(["a→b", "a→c"]);
  });

  it("drops edges that reference unknown vertices", () => {
    const response: IlluminateResponse = {
      graph: {
        vertices: [{ key: "a" }, { key: "b" }],
        edges: [
          { tail: "a", head: "b", weight: 1 },
          { tail: "a", head: "missing", weight: 1 },
        ],
      },
    };
    const view = commandResultToGraphView(cmd, response)!;
    expect(view.edges.map((e) => e.id)).toEqual(["a→b"]);
  });

  it("renders an empty view when the response carries no graph", () => {
    const view = commandResultToGraphView(cmd, {} as IlluminateResponse)!;
    expect(view.nodes).toEqual([]);
    expect(view.edges).toEqual([]);
  });
});

describe("commandResultToGraphView — get vertex", () => {
  const cmd: Command = { verb: "get", objective: "vertex", key: "alice" };

  it("renders a single seed node", () => {
    const vertex: Vertex = { key: "alice", string: "wonderland" };
    const view = commandResultToGraphView(cmd, vertex)!;
    expect(view.nodes).toHaveLength(1);
    expect(view.nodes[0].id).toBe("alice");
    expect(view.nodes[0].isInitialSeed).toBe(true);
    expect(view.nodes[0].importance).toBe(1);
    expect(view.edges).toEqual([]);
  });

  it("renders an empty view when the server returned NotFound", () => {
    const view = commandResultToGraphView(cmd, null)!;
    expect(view.nodes).toEqual([]);
    expect(view.edges).toEqual([]);
  });
});

describe("commandResultToGraphView — get edge", () => {
  const cmd: Command = {
    verb: "get",
    objective: "edge",
    tail: "alice",
    head: "bob",
  };

  it("renders both endpoints plus the edge", () => {
    const edge: Edge = { tail: "alice", head: "bob", weight: 7 };
    const view = commandResultToGraphView(cmd, edge)!;
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["alice", "bob"]);
    expect(view.nodes.find((n) => n.id === "alice")!.isInitialSeed).toBe(true);
    expect(view.nodes.find((n) => n.id === "bob")!.isInitialSeed).toBe(false);
    expect(view.edges).toHaveLength(1);
    expect(view.edges[0].id).toBe("alice→bob");
    expect(view.edges[0].weight).toBe(7);
  });

  it("renders an empty view when the server returned NotFound", () => {
    const view = commandResultToGraphView(cmd, null)!;
    expect(view.nodes).toEqual([]);
    expect(view.edges).toEqual([]);
  });
});

describe("commandResultToGraphView — scan vertices", () => {
  it("renders each returned vertex with no edges", () => {
    const cmd: Command = {
      verb: "scan",
      objective: "vertices",
      prefix: "user:",
      limit: 10,
      all: false,
    };
    const response: ScanVerticesResponse & { count: number } = {
      vertices: [
        { key: "user:alice" },
        { key: "user:bob" },
        { key: "user:carol" },
      ],
      count: 3,
    };
    const view = commandResultToGraphView(cmd, response)!;
    expect(view.nodes.map((n) => n.id).sort()).toEqual([
      "user:alice",
      "user:bob",
      "user:carol",
    ]);
    expect(view.edges).toEqual([]);
    expect(view.nodes.every((n) => !n.isInitialSeed)).toBe(true);
  });

  it("marks the exact-match vertex as a seed when prefix is non-empty", () => {
    const cmd: Command = {
      verb: "scan",
      objective: "vertices",
      prefix: "alice",
      limit: 10,
      all: false,
    };
    const response: ScanVerticesResponse = {
      vertices: [{ key: "alice" }, { key: "aliceland" }],
    };
    const view = commandResultToGraphView(cmd, response)!;
    expect(view.nodes.find((n) => n.id === "alice")!.isInitialSeed).toBe(true);
    expect(view.nodes.find((n) => n.id === "aliceland")!.isInitialSeed).toBe(
      false,
    );
  });
});

describe("commandResultToGraphView — scan edges", () => {
  const cmd: Command = {
    verb: "scan",
    objective: "edges",
    tailPrefix: "alice",
    headPrefix: "",
    limit: 10,
    all: false,
  };

  it("synthesises endpoints from edges and dedupes shared vertices", () => {
    const response: ScanEdgesResponse = {
      edges: [
        { tail: "alice", head: "bob", weight: 1 },
        { tail: "alice", head: "carol", weight: 2 },
        { tail: "alice", head: "bob", weight: 5 }, // duplicate endpoint
      ],
    };
    const view = commandResultToGraphView(cmd, response)!;
    expect(view.nodes.map((n) => n.id).sort()).toEqual([
      "alice",
      "bob",
      "carol",
    ]);
    expect(view.edges).toHaveLength(3);
  });

  it("marks the tail-prefix match as a seed", () => {
    const response: ScanEdgesResponse = {
      edges: [{ tail: "alice", head: "bob", weight: 1 }],
    };
    const view = commandResultToGraphView(cmd, response)!;
    expect(view.nodes.find((n) => n.id === "alice")!.isInitialSeed).toBe(true);
    expect(view.nodes.find((n) => n.id === "bob")!.isInitialSeed).toBe(false);
  });

  it("does not seed any node when the tail prefix is empty", () => {
    const empty: Command = {
      verb: "scan",
      objective: "edges",
      tailPrefix: "",
      headPrefix: "",
      limit: 10,
      all: false,
    };
    const response: ScanEdgesResponse = {
      edges: [{ tail: "alice", head: "bob", weight: 1 }],
    };
    const view = commandResultToGraphView(empty, response)!;
    expect(view.nodes.every((n) => !n.isInitialSeed)).toBe(true);
  });
});

describe("commandResultToGraphView — non-graph verbs", () => {
  it("returns null for put vertex", () => {
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "alice",
      value: "wonderland",
      ttlSeconds: 0,
      valueType: "auto",
    };
    expect(commandResultToGraphView(cmd, { ok: true })).toBeNull();
  });

  it("returns null for put edge", () => {
    const cmd: Command = {
      verb: "put",
      objective: "edge",
      tail: "alice",
      head: "bob",
      weight: 1,
      ttlSeconds: 0,
    };
    expect(commandResultToGraphView(cmd, { ok: true })).toBeNull();
  });

  it("returns null for add edge", () => {
    const cmd: Command = {
      verb: "add",
      objective: "edge",
      tail: "alice",
      head: "bob",
      weight: 1,
      ttlSeconds: 0,
    };
    expect(commandResultToGraphView(cmd, { ok: true })).toBeNull();
  });

  it("returns null for delete vertex", () => {
    const cmd: Command = {
      verb: "delete",
      objective: "vertex",
      keys: ["alice"],
    };
    expect(commandResultToGraphView(cmd, { existed: true })).toBeNull();
  });

  it("returns null for delete edge", () => {
    const cmd: Command = {
      verb: "delete",
      objective: "edge",
      pairs: [{ tail: "alice", head: "bob" }],
    };
    expect(commandResultToGraphView(cmd, { existed: true })).toBeNull();
  });

  it("returns null for exit", () => {
    const cmd: Command = { verb: "exit" };
    expect(commandResultToGraphView(cmd, null)).toBeNull();
  });
});

describe("commandResultToGraphMerge", () => {
  it("projects a put vertex into a single focus node carrying its value", () => {
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "alice",
      value: "wonderland",
      ttlSeconds: 0,
      valueType: "auto",
    };
    const merge = commandResultToGraphMerge(cmd)!;
    expect(merge.nodes).toHaveLength(1);
    expect(merge.nodes[0].id).toBe("alice");
    expect(merge.nodes[0].vertex.key).toBe("alice");
    expect(merge.nodes[0].vertex.string).toBe("wonderland");
    expect(typeof merge.nodes[0].vertex.expiration).toBe("string");
    expect(merge.edges).toEqual([]);
    expect(merge.additive).toBe(false);
    expect(merge.focus).toBe("alice");
  });

  it("coerces a numeric put vertex value onto int64", () => {
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "n",
      value: "42",
      ttlSeconds: 0,
      valueType: "auto",
    };
    const merge = commandResultToGraphMerge(cmd)!;
    expect(merge.nodes[0].vertex.int64).toBe("42");
  });

  it("projects a put edge into two key-only endpoints plus the edge", () => {
    const cmd: Command = {
      verb: "put",
      objective: "edge",
      tail: "alice",
      head: "bob",
      weight: 2,
      ttlSeconds: 0,
    };
    const merge = commandResultToGraphMerge(cmd)!;
    expect(merge.nodes.map((n) => n.id).sort()).toEqual(["alice", "bob"]);
    // endpoints are placeholders — identity only, no value fields.
    expect(
      merge.nodes.every(
        (n) => Object.keys(n.vertex).filter((k) => k !== "key").length === 0,
      ),
    ).toBe(true);
    expect(merge.edges).toHaveLength(1);
    expect(merge.edges[0].id).toBe("alice→bob");
    expect(merge.edges[0].weight).toBe(2);
    expect(merge.edges[0].edge.weight).toBe(2);
    expect(merge.additive).toBe(false);
    expect(merge.focus).toBe("alice");
  });

  it("marks an add edge as additive", () => {
    const cmd: Command = {
      verb: "add",
      objective: "edge",
      tail: "alice",
      head: "bob",
      weight: 1,
      ttlSeconds: 0,
    };
    const merge = commandResultToGraphMerge(cmd)!;
    expect(merge.additive).toBe(true);
    expect(merge.focus).toBe("alice");
  });

  it("projects an add decaying-edge as an additive edge at its S(0) weight", () => {
    const cmd: Command = {
      verb: "add",
      objective: "decaying-edge",
      tail: "alice",
      head: "bob",
      initialWeight: 16,
      ratio: 0.5,
      steps: 5,
      intervalSeconds: 1,
    };
    const merge = commandResultToGraphMerge(cmd)!;
    expect(merge.nodes.map((n) => n.id).sort()).toEqual(["alice", "bob"]);
    expect(merge.edges).toHaveLength(1);
    expect(merge.edges[0].id).toBe("alice→bob");
    // The canvas shows the initial live weight (S(0)); per-step contributions
    // are an SDK/server detail.
    expect(merge.edges[0].weight).toBe(16);
    expect(merge.edges[0].edge.weight).toBe(16);
    // Expiry is the full decay horizon (steps × interval = 5s).
    expect(typeof merge.edges[0].edge.expiration).toBe("string");
    expect(merge.additive).toBe(true);
    expect(merge.focus).toBe("alice");
  });

  it("returns null for verbs that carry no mergeable element", () => {
    expect(
      commandResultToGraphMerge({
        verb: "delete",
        objective: "vertex",
        keys: ["x"],
      }),
    ).toBeNull();
    expect(
      commandResultToGraphMerge({
        verb: "delete",
        objective: "edge",
        pairs: [{ tail: "a", head: "b" }],
      }),
    ).toBeNull();
    expect(commandResultToGraphMerge({ verb: "exit" })).toBeNull();
    expect(
      commandResultToGraphMerge({ verb: "get", objective: "vertex", key: "a" }),
    ).toBeNull();
  });
});

describe("mergeGraphView", () => {
  /** A two-node illuminate frame (seed `a` → `b`, origin `a`). */
  function illuminateBase() {
    const cmd: Command = {
      verb: "illuminate",
      seed: "a",
      step: 2,
      k: 5,
      algorithm: "none",
      objective: "min",
      weighting: "raw",
      vertexPrefix: "",
      restartProb: 0,
      epsilon: 0,
    };
    const response: IlluminateResponse = {
      graph: {
        vertices: [{ key: "a" }, { key: "b" }],
        edges: [{ tail: "a", head: "b", weight: 1 }],
      },
    };
    return commandResultToGraphView(cmd, response)!;
  }

  it("opens a fresh frame anchored on the put vertex when the canvas is empty", () => {
    const merge = commandResultToGraphMerge({
      verb: "put",
      objective: "vertex",
      key: "x",
      value: "1",
      ttlSeconds: 0,
      valueType: "auto",
    })!;
    const view = mergeGraphView(null, merge);
    expect(view.nodes.map((n) => n.id)).toEqual(["x"]);
    const x = view.nodes[0];
    expect(x.isInitialSeed).toBe(true);
    expect(x.isExpansionOrigin).toBe(true);
    expect(x.hopDistance).toBe(0);
    expect(view.expansionOrigins).toEqual(["x"]);
    expect(view.latestExpansionOrigin).toBe("x");
  });

  it("opens a fresh frame anchored on the tail when the first command is a put edge", () => {
    const merge = commandResultToGraphMerge({
      verb: "put",
      objective: "edge",
      tail: "a",
      head: "b",
      weight: 2,
      ttlSeconds: 0,
    })!;
    const view = mergeGraphView(null, merge);
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b"]);
    expect(view.edges.map((e) => e.id)).toEqual(["a→b"]);
    expect(view.nodes.find((n) => n.id === "a")!.isExpansionOrigin).toBe(true);
    expect(view.nodes.find((n) => n.id === "a")!.hopDistance).toBe(0);
    expect(view.nodes.find((n) => n.id === "b")!.hopDistance).toBe(1);
    expect(view.expansionOrigins).toEqual(["a"]);
  });

  it("folds a new node onto an existing frame without disturbing its origins", () => {
    const base = illuminateBase();
    const merge = commandResultToGraphMerge({
      verb: "put",
      objective: "vertex",
      key: "c",
      value: "1",
      ttlSeconds: 0,
      valueType: "auto",
    })!;
    const view = mergeGraphView(base, merge);
    expect(view.nodes.map((n) => n.id).sort()).toEqual(["a", "b", "c"]);
    // origins + seed survive the write.
    expect(view.expansionOrigins).toEqual(["a"]);
    expect(view.latestExpansionOrigin).toBe("a");
    expect(view.nodes.find((n) => n.id === "a")!.isInitialSeed).toBe(true);
    // the new node is NOT promoted to a seed/origin in a non-empty frame.
    const c = view.nodes.find((n) => n.id === "c")!;
    expect(c.isInitialSeed).toBe(false);
    expect(c.isExpansionOrigin).toBe(false);
    expect(c.hopDistance).toBe(Number.POSITIVE_INFINITY);
  });

  it("sums an additive edge's weight onto an existing edge", () => {
    const base = illuminateBase();
    const merge = commandResultToGraphMerge({
      verb: "add",
      objective: "edge",
      tail: "a",
      head: "b",
      weight: 3,
      ttlSeconds: 0,
    })!;
    const view = mergeGraphView(base, merge);
    const edge = view.edges.find((e) => e.id === "a→b")!;
    expect(edge.weight).toBe(4);
    expect(edge.edge.weight).toBe(4);
  });

  it("replaces an existing edge's weight on a non-additive put", () => {
    const base = illuminateBase();
    const merge = commandResultToGraphMerge({
      verb: "put",
      objective: "edge",
      tail: "a",
      head: "b",
      weight: 9,
      ttlSeconds: 0,
    })!;
    const view = mergeGraphView(base, merge);
    expect(view.edges.find((e) => e.id === "a→b")!.weight).toBe(9);
  });

  it("refreshes a node's value on put but keeps its role, and a later edge placeholder never clobbers it", () => {
    const base = illuminateBase();
    const enriched = mergeGraphView(
      base,
      commandResultToGraphMerge({
        verb: "put",
        objective: "vertex",
        key: "a",
        value: "rich",
        ttlSeconds: 0,
        valueType: "auto",
      })!,
    );
    const a1 = enriched.nodes.find((n) => n.id === "a")!;
    expect(a1.vertex.string).toBe("rich");
    expect(a1.isInitialSeed).toBe(true);

    const withEdge = mergeGraphView(
      enriched,
      commandResultToGraphMerge({
        verb: "put",
        objective: "edge",
        tail: "a",
        head: "z",
        weight: 1,
        ttlSeconds: 0,
      })!,
    );
    const a2 = withEdge.nodes.find((n) => n.id === "a")!;
    // the key-only "a" placeholder from the edge must not wipe the value.
    expect(a2.vertex.string).toBe("rich");
    expect(a2.isInitialSeed).toBe(true);
    expect(withEdge.nodes.find((n) => n.id === "z")).toBeDefined();
  });
});
