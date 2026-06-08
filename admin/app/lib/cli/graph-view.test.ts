import { describe, expect, it } from "bun:test";
import { commandResultToGraphView } from "./graph-view";
import type { Command } from "./types";
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
    limit: 10,
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
      limit: 10,
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
    const cmd: Command = { verb: "delete", objective: "vertex", key: "alice" };
    expect(commandResultToGraphView(cmd, { existed: true })).toBeNull();
  });

  it("returns null for delete edge", () => {
    const cmd: Command = {
      verb: "delete",
      objective: "edge",
      tail: "alice",
      head: "bob",
    };
    expect(commandResultToGraphView(cmd, { existed: true })).toBeNull();
  });

  it("returns null for exit", () => {
    const cmd: Command = { verb: "exit" };
    expect(commandResultToGraphView(cmd, null)).toBeNull();
  });
});
