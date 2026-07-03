/**
 * In-process E2E test for the Connect-Node-backed `Lantern` class.
 *
 * Stands up a Node http server that mounts a stub implementation of
 * the LanternService Connect handler via `connectNodeAdapter`, then
 * drives `connect()` (the Node entrypoint helper from
 * `lantern-sdk`) against it. The stub is intentionally
 * narrow — it only implements the RPCs the round-trip tests exercise
 * — so the test stays focused on the transport bridge rather than the
 * service semantics. The full service contract is covered by the Go
 * server tests under `server/service/`.
 *
 * Why the stub stops at a handful of methods: importing server/service
 * would pull in mutationlog / replication / otel / prometheus, which
 * would balloon sdks/node's dev dependencies just to exercise a happy
 * path. The connectNodeAdapter accepts a partial implementation
 * (missing methods return UNIMPLEMENTED), so the stub stays minimal.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import * as http from "node:http";
import { type AddressInfo } from "node:net";

import { Code, ConnectError, type ConnectRouter } from "@connectrpc/connect";
import { connectNodeAdapter } from "@connectrpc/connect-node";
import { create } from "@bufbuild/protobuf";

import {
  Lantern,
  FailedPreconditionError,
  InvalidArgumentError,
  NotFoundError,
  connect,
  Reduction,
} from "../src/index.js";
import { LanternService, VertexSchema } from "../src/gen/graph/v1/graph_pb.js";

interface StubState {
  vertices: Map<string, ReturnType<typeof create<typeof VertexSchema>>>;
  /** Last IlluminateRequest the stub observed, for request-building assertions (#605, #846). */
  lastIlluminate?: {
    seed: string;
    vertexPrefix: string;
    paramsCase: string | undefined;
    step: number;
    fanOut: number;
    reduction: number;
    topN: number;
    restartProb: number;
    epsilon: number;
  };
  /** Last SearchVerticesRequest the stub observed, for request-building assertions (#639). */
  lastSearch?: { query: string; limit: number; prefix: string };
  /** Ranked hits the searchVertices stub returns (descending relevance). */
  searchHits?: { key: string; score: number }[];
  /** When true, searchVertices rejects with FAILED_PRECONDITION (index disabled). */
  searchDisabled?: boolean;
  /** Last AddEdgeRequest the stub observed, for contrib-id assertions (#895). */
  lastAddEdge?: { contribId: Uint8Array };
  /** Every AddEdgesRequest the stub observed, in order, for chunk-alignment assertions (#895). */
  addEdgesCalls: { contribIds: Uint8Array[]; tails: string[] }[];
  /** Edge weights keyed tail → head → weight, seeded by DeleteEdgesByPrefix tests (#899). */
  edges: Map<string, Map<string, number>>;
  /** Last DeleteEdgesByPrefixRequest the stub observed, for request-building assertions (#899). */
  lastDeleteEdgesByPrefix?: {
    tailPrefix: string;
    headPrefix: string;
    limit: number;
    dryRun: boolean;
  };
}

function newStubRoutes(state: StubState) {
  return (router: ConnectRouter) => {
    router.service(LanternService, {
      async getVertex(req) {
        const v = state.vertices.get(req.key);
        if (!v) {
          throw new ConnectError("not found", Code.NotFound);
        }
        return { vertex: v };
      },
      async putVertex(req) {
        if (req.vertex) {
          if (req.ifAbsent && state.vertices.has(req.vertex.key)) {
            return { written: false };
          }
          state.vertices.set(req.vertex.key, req.vertex);
        }
        return { written: true };
      },
      async putVertices(req) {
        if (req.ifAbsent) {
          let written = 0;
          const skippedKeys: string[] = [];
          for (const v of req.vertices) {
            if (state.vertices.has(v.key)) {
              skippedKeys.push(v.key);
              continue;
            }
            state.vertices.set(v.key, v);
            written++;
          }
          return { written, skippedKeys };
        }
        for (const v of req.vertices) {
          state.vertices.set(v.key, v);
        }
        return { written: req.vertices.length, skippedKeys: [] };
      },
      // Capture Add requests so #895 tests can assert how the SDK wires
      // contrib_ids (singular forwards into contrib_id; plural is
      // index-aligned with edges across chunks).
      async addEdge(req) {
        state.lastAddEdge = { contribId: req.contribId };
        return { effectiveWeight: req.edge?.weight ?? 0 };
      },
      async addEdges(req) {
        state.addEdgesCalls.push({
          contribIds: req.contribIds,
          tails: req.edges.map((e) => e.tail),
        });
        return { effectiveWeights: req.edges.map((e) => e.weight) };
      },
      async deleteVertex(req) {
        const existed = state.vertices.delete(req.key);
        return { existed };
      },
      async getServerStatus() {
        return { version: "test" };
      },
      // Captures the request so tests can assert how the SDK assembles
      // IlluminateRequest (e.g. vertexPrefix wiring, #605). Returns an
      // empty graph — the round-trip semantics of the result are out of
      // scope here.
      async illuminate(req) {
        const bfs = req.params.case === "bfs" ? req.params.value : undefined;
        const ppr = req.params.case === "ppr" ? req.params.value : undefined;
        state.lastIlluminate = {
          seed: req.seed,
          vertexPrefix: req.vertexPrefix,
          paramsCase: req.params.case,
          step: bfs?.step ?? 0,
          fanOut: bfs?.fanOut ?? 0,
          reduction: bfs?.reduction ?? 0,
          topN: ppr?.topN ?? 0,
          restartProb: ppr?.restartProb ?? 0,
          epsilon: ppr?.epsilon ?? 0,
        };
        return {};
      },
      // Captures the request so tests can assert how the SDK assembles
      // SearchVerticesRequest (#639) and exercises the FAILED_PRECONDITION
      // (index-disabled) branch. Returns the configured ranked hits.
      async searchVertices(req) {
        state.lastSearch = { query: req.query, limit: req.limit, prefix: req.prefix };
        if (state.searchDisabled) {
          throw new ConnectError("search index disabled", Code.FailedPrecondition);
        }
        return { hits: state.searchHits ?? [] };
      },
      // Keys-only prefix scan (#674): returns sorted matching keys with an
      // opaque last-key cursor so pagination round-trips. The cursor shape
      // here is a stub (raw last-key bytes); the SDK treats it as opaque.
      async scanVertexKeys(req) {
        const all = [...state.vertices.keys()].filter((k) => k.startsWith(req.prefix)).sort();
        const after = req.cursor.length > 0 ? new TextDecoder().decode(req.cursor) : "";
        const remaining = after ? all.filter((k) => k > after) : all;
        const limit = req.limit > 0 ? req.limit : 100;
        const page = remaining.slice(0, limit);
        const hitLimit = remaining.length > limit;
        return {
          keys: page,
          nextCursor:
            hitLimit && page.length > 0
              ? new TextEncoder().encode(page[page.length - 1])
              : new Uint8Array(),
        };
      },
      // Edge-prefix bulk delete (#899): mirrors the server's validation
      // (at least one prefix non-empty → InvalidArgument) and the
      // intersection-of-prefixes delete semantics, honoring `limit` and
      // `dryRun` (dry-run counts without mutating). Captures the request
      // so tests can assert the SDK's option wiring.
      async deleteEdgesByPrefix(req) {
        state.lastDeleteEdgesByPrefix = {
          tailPrefix: req.tailPrefix,
          headPrefix: req.headPrefix,
          limit: req.limit,
          dryRun: req.dryRun,
        };
        if (req.tailPrefix === "" && req.headPrefix === "") {
          throw new ConnectError(
            "at least one of tail_prefix / head_prefix must be non-empty",
            Code.InvalidArgument,
          );
        }
        const cap = req.limit > 0 ? req.limit : Number.MAX_SAFE_INTEGER;
        const victims: { tail: string; head: string }[] = [];
        for (const [tail, heads] of state.edges) {
          if (!tail.startsWith(req.tailPrefix)) continue;
          for (const head of heads.keys()) {
            if (!head.startsWith(req.headPrefix)) continue;
            if (victims.length >= cap) break;
            victims.push({ tail, head });
          }
          if (victims.length >= cap) break;
        }
        if (!req.dryRun) {
          for (const { tail, head } of victims) {
            const heads = state.edges.get(tail);
            heads?.delete(head);
            if (heads && heads.size === 0) state.edges.delete(tail);
          }
        }
        return { deleted: BigInt(victims.length) };
      },
      // Remaining methods are intentionally absent — the connect-node
      // adapter rejects them with Code.Unimplemented, which the SDK
      // surfaces as the generic LanternError. Tests that need these
      // RPCs would extend the stub.
    });
  };
}

let server: http.Server;
let baseUrl: string;
const state: StubState = { vertices: new Map(), addEdgesCalls: [], edges: new Map() };

beforeAll(async () => {
  // HTTP/1.1 server (not http2) — Bun's test runner has rough edges
  // with Node http2 client reuse across tests, surfacing as spurious
  // "Premature close" errors on the second call. HTTP/1.1 is plenty
  // for the round-trip semantics this test covers, and connect-node
  // negotiates the Connect protocol over both transports identically.
  server = http.createServer(connectNodeAdapter({ routes: newStubRoutes(state) }));
  await new Promise<void>((resolve) => server.listen(0, "127.0.0.1", resolve));
  const addr = server.address() as AddressInfo;
  baseUrl = `http://127.0.0.1:${addr.port}`;
});

afterAll(async () => {
  await new Promise<void>((resolve) => server.close(() => resolve()));
});

function newClient(): Lantern {
  // Pin the transport to httpVersion: "1.1" because the test server
  // is a plain http.Server (see beforeAll). The transport default is
  // "2" which would attempt h2 upgrade against an http/1 server.
  return connect(baseUrl, {
    transportOptions: { httpVersion: "1.1" },
  });
}

describe("Lantern client", () => {
  test("connect rejects baseUrl without scheme", () => {
    expect(() => connect("lantern:6380")).toThrow(/scheme/);
  });

  test("connect rejects empty baseUrl", () => {
    expect(() => connect("")).toThrow(/baseUrl/);
  });

  test("PutVertex + GetVertex round-trips a string value", async () => {
    const c = newClient();
    try {
      await c.putVertex({ key: "users/42", value: "alice" });
      const v = await c.getVertex("users/42");
      expect(v.key).toBe("users/42");
      expect(v.kind).toBe("string");
      expect(v.value).toBe("alice");
    } finally {
      c.close();
    }
  });

  test("GetVertex on a missing key surfaces NotFoundError", async () => {
    const c = newClient();
    try {
      await expect(c.getVertex("definitely/missing")).rejects.toThrow(NotFoundError);
    } finally {
      c.close();
    }
  });

  test("DeleteVertex returns existed=true after Put then false after delete", async () => {
    const c = newClient();
    try {
      await c.putVertex({ key: "to-delete", value: "x" });
      await expect(c.deleteVertex("to-delete")).resolves.toBe(true);
      await expect(c.deleteVertex("to-delete")).resolves.toBe(false);
    } finally {
      c.close();
    }
  });

  test("scanVertexKeys lists keys-only and paginates (incl. scanVertexKeysAll)", async () => {
    const c = newClient();
    try {
      await c.putVertices([
        { key: "kx:1", value: "a" },
        { key: "kx:2", value: "b" },
        { key: "kx:3", value: "c" },
      ]);

      const p1 = await c.scanVertexKeys("kx:", { limit: 2 });
      expect(p1.keys).toEqual(["kx:1", "kx:2"]);
      expect(p1.nextCursor.length).toBeGreaterThan(0);

      const p2 = await c.scanVertexKeys("kx:", { limit: 2, cursor: p1.nextCursor });
      expect(p2.keys).toEqual(["kx:3"]);
      expect(p2.nextCursor.length).toBe(0);

      const all: string[] = [];
      for await (const page of c.scanVertexKeysAll("kx:", 2)) all.push(...page);
      expect(all).toEqual(["kx:1", "kx:2", "kx:3"]);
    } finally {
      c.close();
    }
  });

  test("putVertices batches and round-trips multiple values", async () => {
    const c = newClient();
    try {
      await c.putVertices([
        { key: "batch/a", value: "alpha" },
        { key: "batch/b", value: 42 }, // integer → int64 (promoted back to number on the wire)
        { key: "batch/c", value: true },
        { key: "batch/d", value: null },
      ]);
      const a = await c.getVertex("batch/a");
      expect(a.kind).toBe("string");
      const b = await c.getVertex("batch/b");
      expect(b.kind).toBe("int64");
      // int64 values that fit safely in Number come back as Number; only
      // out-of-safe-range integers promote to bigint.
      expect(b.value).toBe(42);
      const c2 = await c.getVertex("batch/c");
      expect(c2.kind).toBe("bool");
      const d = await c.getVertex("batch/d");
      expect(d.kind).toBe("nil");
    } finally {
      c.close();
    }
  });

  test("putVertexIfAbsent writes when absent and skips a live key (#896)", async () => {
    const c = newClient();
    try {
      await expect(c.putVertexIfAbsent({ key: "nx/k", value: "one" })).resolves.toBe(true);
      // Second attempt over a now-live key is a no-op.
      await expect(c.putVertexIfAbsent({ key: "nx/k", value: "two" })).resolves.toBe(false);
      const v = await c.getVertex("nx/k");
      expect(v.value).toBe("one"); // untouched by the skipped write
    } finally {
      c.close();
    }
  });

  test("putVerticesIfAbsent reports written count and skipped keys (#896)", async () => {
    const c = newClient();
    try {
      await c.putVertex({ key: "nx/live", value: "old" });
      const { written, skippedKeys } = await c.putVerticesIfAbsent([
        { key: "nx/fresh", value: "a" },
        { key: "nx/live", value: "b" }, // skipped: already live
      ]);
      expect(written).toBe(1);
      expect(skippedKeys).toEqual(["nx/live"]);
      const live = await c.getVertex("nx/live");
      expect(live.value).toBe("old"); // untouched
    } finally {
      c.close();
    }
  });
});

describe("AddEdge contrib IDs (#895)", () => {
  function idempotentClient(): Lantern {
    return connect(baseUrl, {
      options: { idempotentAdds: true },
      transportOptions: { httpVersion: "1.1" },
    });
  }

  test("addEdge omits contrib_id by default (legacy additive path)", async () => {
    const c = newClient();
    try {
      state.lastAddEdge = undefined;
      await c.addEdge({ tail: "a", head: "b", weight: 1 });
      expect(state.lastAddEdge?.contribId.length).toBe(0);
    } finally {
      c.close();
    }
  });

  test("addEdge mints a 24-byte contrib_id when idempotentAdds is on", async () => {
    const c = idempotentClient();
    try {
      state.lastAddEdge = undefined;
      await c.addEdge({ tail: "a", head: "b", weight: 1 });
      expect(state.lastAddEdge?.contribId.length).toBe(24);
    } finally {
      c.close();
    }
  });

  test("addEdge forwards a caller-supplied contrib_id verbatim", async () => {
    const c = newClient();
    const id = new Uint8Array(24).fill(7);
    try {
      state.lastAddEdge = undefined;
      await c.addEdge({ tail: "a", head: "b", weight: 1, contribId: id });
      expect(state.lastAddEdge?.contribId).toEqual(id);
    } finally {
      c.close();
    }
  });

  test("addEdge rejects a wrong-length contrib_id before hitting the wire", async () => {
    const c = newClient();
    try {
      state.lastAddEdge = undefined;
      await expect(
        c.addEdge({ tail: "a", head: "b", weight: 1, contribId: new Uint8Array(16) }),
      ).rejects.toThrow(InvalidArgumentError);
      expect(state.lastAddEdge).toBeUndefined();
    } finally {
      c.close();
    }
  });

  test("addEdges with idempotentAdds mints index-aligned, per-index-distinct ids", async () => {
    const c = idempotentClient();
    try {
      state.addEdgesCalls = [];
      await c.addEdges([
        { tail: "a", head: "b", weight: 1 },
        { tail: "a", head: "c", weight: 1 },
        { tail: "a", head: "d", weight: 1 },
      ]);
      expect(state.addEdgesCalls.length).toBe(1);
      const { contribIds } = state.addEdgesCalls[0];
      expect(contribIds.length).toBe(3);
      for (const id of contribIds) expect(id.length).toBe(24);
      // Shared nonce (bytes 0..15), distinct low bytes (16..23) per index.
      const lows = contribIds.map((id) => [...id.slice(16)].join(","));
      expect(new Set(lows).size).toBe(3);
      const nonce0 = [...contribIds[0].slice(0, 16)].join(",");
      const nonce1 = [...contribIds[1].slice(0, 16)].join(",");
      expect(nonce1).toBe(nonce0);
    } finally {
      c.close();
    }
  });

  test("addEdges omits contrib_ids entirely when neither option nor caller ids apply", async () => {
    const c = newClient();
    try {
      state.addEdgesCalls = [];
      await c.addEdges([
        { tail: "a", head: "b", weight: 1 },
        { tail: "a", head: "c", weight: 1 },
      ]);
      expect(state.addEdgesCalls[0].contribIds.length).toBe(0);
    } finally {
      c.close();
    }
  });

  test("addEdges fills empty slots around a caller-supplied id (mixed batch)", async () => {
    const c = newClient();
    const id = new Uint8Array(24).fill(9);
    try {
      state.addEdgesCalls = [];
      await c.addEdges([
        { tail: "a", head: "b", weight: 1 },
        { tail: "a", head: "c", weight: 1, contribId: id },
      ]);
      const { contribIds } = state.addEdgesCalls[0];
      expect(contribIds.length).toBe(2);
      expect(contribIds[0].length).toBe(0);
      expect(contribIds[1]).toEqual(id);
    } finally {
      c.close();
    }
  });

  test("addEdges keeps caller ids aligned with edges across chunk boundaries", async () => {
    // batchChunkSize 2 splits 3 edges into [0,1] and [2]; each caller id must
    // stay paired with its edge in whichever chunk it lands.
    const c = connect(baseUrl, {
      options: { batchChunkSize: 2 },
      transportOptions: { httpVersion: "1.1" },
    });
    const ids = [
      new Uint8Array(24).fill(1),
      new Uint8Array(24).fill(2),
      new Uint8Array(24).fill(3),
    ];
    try {
      state.addEdgesCalls = [];
      await c.addEdges([
        { tail: "a", head: "b", weight: 1, contribId: ids[0] },
        { tail: "a", head: "c", weight: 1, contribId: ids[1] },
        { tail: "a", head: "d", weight: 1, contribId: ids[2] },
      ]);
      expect(state.addEdgesCalls.length).toBe(2);
      expect(state.addEdgesCalls[0].tails.length).toBe(2);
      expect(state.addEdgesCalls[0].contribIds[0]).toEqual(ids[0]);
      expect(state.addEdgesCalls[0].contribIds[1]).toEqual(ids[1]);
      expect(state.addEdgesCalls[1].contribIds[0]).toEqual(ids[2]);
    } finally {
      c.close();
    }
  });

  test("successive idempotent addEdges calls advance the sequence (distinct ids)", async () => {
    const c = idempotentClient();
    try {
      state.addEdgesCalls = [];
      await c.addEdges([{ tail: "a", head: "b", weight: 1 }]);
      await c.addEdges([{ tail: "a", head: "b", weight: 1 }]);
      expect(state.addEdgesCalls.length).toBe(2);
      const first = [...state.addEdgesCalls[0].contribIds[0]].join(",");
      const second = [...state.addEdgesCalls[1].contribIds[0]].join(",");
      expect(second).not.toBe(first);
    } finally {
      c.close();
    }
  });

  // #897: Add returns the post-accumulation effective weight so callers can
  // implement counters without a follow-up read.
  test("addEdge returns the effective weight", async () => {
    const c = newClient();
    try {
      const effective = await c.addEdge({ tail: "a", head: "b", weight: 2.5 });
      expect(effective).toBe(2.5);
    } finally {
      c.close();
    }
  });

  test("addEdges returns index-aligned effective weights across chunk boundaries", async () => {
    // batchChunkSize 2 splits 3 edges into [0,1] and [2]; the returned slice
    // must stay index-aligned with the inputs across both chunks.
    const c = connect(baseUrl, {
      options: { batchChunkSize: 2 },
      transportOptions: { httpVersion: "1.1" },
    });
    try {
      const effective = await c.addEdges([
        { tail: "a", head: "b", weight: 1 },
        { tail: "a", head: "c", weight: 2 },
        { tail: "a", head: "d", weight: 3 },
      ]);
      expect(effective).toEqual([1, 2, 3]);
    } finally {
      c.close();
    }
  });

  test("addEdges returns an empty slice for an empty input", async () => {
    const c = newClient();
    try {
      expect(await c.addEdges([])).toEqual([]);
    } finally {
      c.close();
    }
  });
});

describe("deleteEdgesByPrefix (#899)", () => {
  function seedEdges(pairs: readonly [string, string][]): void {
    state.edges = new Map();
    for (const [tail, head] of pairs) {
      let heads = state.edges.get(tail);
      if (!heads) {
        heads = new Map();
        state.edges.set(tail, heads);
      }
      heads.set(head, 1);
    }
  }

  test("forwards both prefixes, limit and dryRun onto the request", async () => {
    const c = newClient();
    try {
      seedEdges([["users/a", "posts/1"]]);
      await c.deleteEdgesByPrefix({
        tailPrefix: "users/",
        headPrefix: "posts/",
        limit: 7,
        dryRun: true,
      });
      expect(state.lastDeleteEdgesByPrefix?.tailPrefix).toBe("users/");
      expect(state.lastDeleteEdgesByPrefix?.headPrefix).toBe("posts/");
      expect(state.lastDeleteEdgesByPrefix?.limit).toBe(7);
      expect(state.lastDeleteEdgesByPrefix?.dryRun).toBe(true);
    } finally {
      c.close();
    }
  });

  test("defaults omitted options to empty prefixes, limit 0 and dryRun false", async () => {
    const c = newClient();
    try {
      seedEdges([["users/a", "posts/1"]]);
      await c.deleteEdgesByPrefix({ tailPrefix: "users/" });
      expect(state.lastDeleteEdgesByPrefix?.headPrefix).toBe("");
      expect(state.lastDeleteEdgesByPrefix?.limit).toBe(0);
      expect(state.lastDeleteEdgesByPrefix?.dryRun).toBe(false);
    } finally {
      c.close();
    }
  });

  test("deletes the tail∩head intersection and returns the count", async () => {
    const c = newClient();
    try {
      seedEdges([
        ["users/a", "posts/1"],
        ["users/a", "logs/9"],
        ["orgs/x", "posts/2"],
      ]);
      const deleted = await c.deleteEdgesByPrefix({
        tailPrefix: "users/",
        headPrefix: "posts/",
      });
      expect(deleted).toBe(1n);
      expect(state.edges.get("users/a")?.has("posts/1")).toBeFalsy();
      expect(state.edges.get("users/a")?.has("logs/9")).toBe(true);
      expect(state.edges.get("orgs/x")?.has("posts/2")).toBe(true);
    } finally {
      c.close();
    }
  });

  test("dryRun reports the count without mutating", async () => {
    const c = newClient();
    try {
      seedEdges([
        ["users/a", "posts/1"],
        ["users/b", "posts/2"],
      ]);
      const would = await c.deleteEdgesByPrefix({ tailPrefix: "users/", dryRun: true });
      expect(would).toBe(2n);
      expect(state.edges.get("users/a")?.has("posts/1")).toBe(true);
      expect(state.edges.get("users/b")?.has("posts/2")).toBe(true);
    } finally {
      c.close();
    }
  });

  test("a both-empty request is rejected as InvalidArgumentError", async () => {
    const c = newClient();
    try {
      seedEdges([["users/a", "posts/1"]]);
      await expect(c.deleteEdgesByPrefix({})).rejects.toBeInstanceOf(InvalidArgumentError);
    } finally {
      c.close();
    }
  });
});

describe("illuminate request building (#605)", () => {
  test("forwards vertexPrefix and the bfs arm onto the request", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", { bfs: { step: 2, fanOut: 5 }, vertexPrefix: "users/" });
      expect(state.lastIlluminate?.seed).toBe("alice");
      expect(state.lastIlluminate?.paramsCase).toBe("bfs");
      expect(state.lastIlluminate?.step).toBe(2);
      expect(state.lastIlluminate?.fanOut).toBe(5);
      expect(state.lastIlluminate?.vertexPrefix).toBe("users/");
    } finally {
      c.close();
    }
  });

  test("omitting both families leaves the params oneof unset (bare illuminate)", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", {});
      expect(state.lastIlluminate?.paramsCase).toBeUndefined();
      expect(state.lastIlluminate?.vertexPrefix).toBe("");
    } finally {
      c.close();
    }
  });

  test("forwards the ppr arm with its knobs (#801/#846)", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", {
        ppr: { topN: 8, restartProb: 0.25, epsilon: 1e-3 },
      });
      expect(state.lastIlluminate?.paramsCase).toBe("ppr");
      expect(state.lastIlluminate?.topN).toBe(8);
      expect(state.lastIlluminate?.restartProb).toBeCloseTo(0.25, 6);
      expect(state.lastIlluminate?.epsilon).toBeCloseTo(1e-3, 9);
    } finally {
      c.close();
    }
  });

  test("omitting PPR knobs yields proto zero values (server defaults)", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", { ppr: {} });
      expect(state.lastIlluminate?.paramsCase).toBe("ppr");
      expect(state.lastIlluminate?.restartProb).toBe(0);
      expect(state.lastIlluminate?.epsilon).toBe(0);
    } finally {
      c.close();
    }
  });

  test("supplying both bfs and ppr is an InvalidArgumentError", async () => {
    const c = newClient();
    try {
      await expect(c.illuminate("alice", { bfs: {}, ppr: {} })).rejects.toThrow(
        /mutually exclusive/,
      );
    } finally {
      c.close();
    }
  });

  test("forwards the reduction on the bfs arm", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", { bfs: { reduction: Reduction.SHORTEST_PATH_TREE } });
      expect(state.lastIlluminate?.paramsCase).toBe("bfs");
      expect(state.lastIlluminate?.reduction).toBe(Reduction.SHORTEST_PATH_TREE);
    } finally {
      c.close();
    }
  });
});

describe("searchVertices request building (#639)", () => {
  test("forwards query, limit and prefix; returns ranked hits in order", async () => {
    const c = newClient();
    state.searchDisabled = false;
    state.searchHits = [
      { key: "doc/3", score: 9.5 },
      { key: "doc/1", score: 4.2 },
      { key: "doc/2", score: 1 },
    ];
    try {
      const hits = await c.searchVertices("alpha beta", { limit: 5, prefix: "doc/" });
      expect(state.lastSearch).toEqual({ query: "alpha beta", limit: 5, prefix: "doc/" });
      expect(hits).toEqual([
        { key: "doc/3", score: 9.5 },
        { key: "doc/1", score: 4.2 },
        { key: "doc/2", score: 1 },
      ]);
    } finally {
      c.close();
    }
  });

  test("defaults limit to 0 and prefix to empty when opts omitted", async () => {
    const c = newClient();
    state.searchDisabled = false;
    state.searchHits = [];
    try {
      await c.searchVertices("q");
      expect(state.lastSearch).toEqual({ query: "q", limit: 0, prefix: "" });
    } finally {
      c.close();
    }
  });

  test("no matches resolves to an empty array, not an error", async () => {
    const c = newClient();
    state.searchDisabled = false;
    state.searchHits = [];
    try {
      await expect(c.searchVertices("nothing")).resolves.toEqual([]);
    } finally {
      c.close();
    }
  });

  test("disabled index surfaces FailedPreconditionError", async () => {
    const c = newClient();
    state.searchDisabled = true;
    try {
      await expect(c.searchVertices("q")).rejects.toThrow(FailedPreconditionError);
    } finally {
      state.searchDisabled = false;
      c.close();
    }
  });
});

describe("bearer token option (#850)", () => {
  test("token attaches Authorization on every call", async () => {
    let seen: string | null = null;
    const c = connect(baseUrl, {
      token: "n0de-s3cret",
      transportOptions: { httpVersion: "1.1" },
      interceptors: [
        (next) => (req) => {
          seen = req.header.get("Authorization");
          return next(req);
        },
      ],
    });
    try {
      await c.getVertex("nope").catch(() => undefined);
      expect(seen).toBe("Bearer n0de-s3cret");
    } finally {
      c.close();
    }
  });

  test("no token leaves the header absent", async () => {
    let seen: string | null = "sentinel";
    const c = connect(baseUrl, {
      transportOptions: { httpVersion: "1.1" },
      interceptors: [
        (next) => (req) => {
          seen = req.header.get("Authorization");
          return next(req);
        },
      ],
    });
    try {
      await c.getVertex("nope").catch(() => undefined);
      expect(seen).toBeNull();
    } finally {
      c.close();
    }
  });
});
