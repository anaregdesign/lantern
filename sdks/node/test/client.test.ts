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
import { connectNodeAdapter, createConnectTransport } from "@connectrpc/connect-node";
import { create } from "@bufbuild/protobuf";

import {
  Lantern,
  FailedPreconditionError,
  InvalidArgumentError,
  NotFoundError,
  ResourceExhaustedError,
  connect,
  Reduction,
  Int32,
  Uint64,
  Float32,
  SearchErrorReason,
  backupRecordToNdjson,
  backupRecordFromNdjson,
  HealthStatusError,
  type BackupRecord,
  type IlluminateOptions,
} from "../src/index.js";
import {
  LanternService,
  VertexSchema,
  EdgeSchema,
  ScanOrder,
  MatchMode,
  SearchErrorDetailSchema,
} from "../src/gen/graph/v1/graph_pb.js";

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
  lastSearch?: {
    query: string;
    limit: number;
    prefix: string;
    matchMode?: number;
    minShouldMatch?: number;
    phrase?: boolean;
    fuzziness?: number;
    prefixTerms?: boolean;
  };
  /** Ranked hits the searchVertices stub returns (descending relevance). */
  searchHits?: { key: string; score: number }[];
  /** When true, searchVertices rejects with FAILED_PRECONDITION (index disabled). */
  searchDisabled?: boolean;
  /** When true, phrase search rejects because positional postings are absent. */
  searchPositionsDisabled?: boolean;
  /** Optional typed RESOURCE_EXHAUSTED search failure. */
  searchResource?: { reason: SearchErrorReason; workKind?: string };
  /** Last AddEdgeRequest the stub observed, for contrib-id assertions (#895). */
  lastAddEdge?: { contribId: Uint8Array };
  /** Every AddEdgesRequest the stub observed, in order, for chunk-alignment assertions (#895). */
  addEdgesCalls: { contribIds: Uint8Array[]; tails: string[]; weights: number[] }[];
  /** Edge weights keyed tail → head → weight, seeded by DeleteEdgesByPrefix tests (#899). */
  edges: Map<string, Map<string, number>>;
  /** Last DeleteEdgesByPrefixRequest the stub observed, for request-building assertions (#899). */
  lastDeleteEdgesByPrefix?: {
    tailPrefix: string;
    headPrefix: string;
    limit: number;
    dryRun: boolean;
  };
  /** ScanOrder of the last scanVertices / scanVertexKeys request (#898). */
  lastScanOrder?: number;
  /**
   * Ordered log of putVertices / putEdges batch sizes, for restore
   * ordering + chunking assertions (#685). Reset per restore test.
   */
  writeLog: { kind: "vertices" | "edges"; count: number }[];
  /**
   * Controls the stub gRPC-Health-v1 endpoint for ping() tests (#685-adjacent
   * parity work). `status` is the JSON `status` string returned on a 200;
   * `httpStatus`, when set to non-200, drives the transport-error branch.
   */
  health?: { status?: string; httpStatus?: number };
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
        state.writeLog.push({ kind: "vertices", count: req.vertices.length });
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
          weights: req.edges.map((e) => e.weight),
        });
        // Model the server's additive accumulation: the effective weight
        // reported for each edge is the running live sum over its (tail,head)
        // seen so far within this request. Distinct endpoints therefore echo
        // their own weight; repeated endpoints (e.g. a decay staircase on one
        // pair) return the telescoped cumulative sum, whose last entry is the
        // full post-add live weight.
        const running = new Map<string, number>();
        const effectiveWeights = req.edges.map((e) => {
          const key = `${e.tail}\u0000${e.head}`;
          const sum = (running.get(key) ?? 0) + e.weight;
          running.set(key, sum);
          return sum;
        });
        return { effectiveWeights };
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
        state.lastSearch = {
          query: req.query,
          limit: req.limit,
          prefix: req.prefix,
          ...(req.options
            ? {
                matchMode: req.options.matchMode,
                minShouldMatch: req.options.minShouldMatch,
                phrase: req.options.phrase,
                fuzziness: req.options.fuzziness,
                prefixTerms: req.options.prefixTerms,
              }
            : {}),
        };
        if (state.searchResource) {
          throw new ConnectError("search execution exhausted", Code.ResourceExhausted, undefined, [
            {
              desc: SearchErrorDetailSchema,
              value: state.searchResource,
            },
          ]);
        }
        if (state.searchPositionsDisabled) {
          throw new ConnectError(
            "phrase search requires positional postings",
            Code.FailedPrecondition,
            undefined,
            [
              {
                desc: SearchErrorDetailSchema,
                value: { reason: SearchErrorReason.SEARCH_POSITIONS_DISABLED },
              },
            ],
          );
        }
        if (state.searchDisabled) {
          throw new ConnectError("search index disabled", Code.FailedPrecondition, undefined, [
            {
              desc: SearchErrorDetailSchema,
              value: { reason: SearchErrorReason.SEARCH_DISABLED },
            },
          ]);
        }
        return { hits: state.searchHits ?? [] };
      },
      // Keys-only prefix scan (#674): returns matching keys in the
      // requested order (ascending default, descending when
      // ScanOrder.DESC — #898) with an opaque last-key cursor so
      // pagination round-trips. The cursor shape here is a stub (raw
      // last-key bytes); the SDK treats it as opaque.
      async scanVertexKeys(req) {
        state.lastScanOrder = req.order;
        const desc = req.order === ScanOrder.DESC;
        const all = [...state.vertices.keys()]
          .filter((k) => k.startsWith(req.prefix))
          .sort((a, b) => (desc ? (a < b ? 1 : a > b ? -1 : 0) : a < b ? -1 : a > b ? 1 : 0));
        const after = req.cursor.length > 0 ? new TextDecoder().decode(req.cursor) : "";
        const remaining = after ? all.filter((k) => (desc ? k < after : k > after)) : all;
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
      // Vertex prefix scan (#836) mirroring scanVertexKeys' ordering /
      // pagination but returning whole vertices, so the #898 descending
      // path is exercised on the values-carrying RPC too.
      async scanVertices(req) {
        state.lastScanOrder = req.order;
        const desc = req.order === ScanOrder.DESC;
        const all = [...state.vertices.keys()]
          .filter((k) => k.startsWith(req.prefix))
          .sort((a, b) => (desc ? (a < b ? 1 : a > b ? -1 : 0) : a < b ? -1 : a > b ? 1 : 0));
        const after = req.cursor.length > 0 ? new TextDecoder().decode(req.cursor) : "";
        const remaining = after ? all.filter((k) => (desc ? k < after : k > after)) : all;
        const limit = req.limit > 0 ? req.limit : 100;
        const pageKeys = remaining.slice(0, limit);
        const hitLimit = remaining.length > limit;
        return {
          vertices: pageKeys.map((k) => state.vertices.get(k)!),
          nextCursor:
            hitLimit && pageKeys.length > 0
              ? new TextEncoder().encode(pageKeys[pageKeys.length - 1])
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
      // Batch edge upsert (#685 restore path): records batch size for
      // ordering/chunking assertions and stores weights into state.edges so
      // a subsequent backup reflects them.
      async putEdges(req) {
        state.writeLog.push({ kind: "edges", count: req.edges.length });
        for (const e of req.edges) {
          if (!state.edges.has(e.tail)) state.edges.set(e.tail, new Map());
          state.edges.get(e.tail)!.set(e.head, e.weight);
        }
        return { written: req.edges.length };
      },
      // Whole-graph point-in-time stream (#685 backup path): emits every
      // vertex (as the `vertex` oneof arm) followed by every edge (as the
      // `edge` arm), honoring the vertexPrefix filter. Server-streaming, so
      // the handler is an async generator.
      async *backupSnapshot(req) {
        for (const [key, v] of state.vertices) {
          if (req.vertexPrefix && !key.startsWith(req.vertexPrefix)) continue;
          yield { record: { case: "vertex" as const, value: v } };
        }
        for (const [tail, heads] of state.edges) {
          for (const [head, weight] of heads) {
            yield {
              record: {
                case: "edge" as const,
                value: create(EdgeSchema, { tail, head, weight }),
              },
            };
          }
        }
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
const state: StubState = { vertices: new Map(), addEdgesCalls: [], edges: new Map(), writeLog: [] };

beforeAll(async () => {
  // HTTP/1.1 server (not http2) — Bun's test runner has rough edges
  // with Node http2 client reuse across tests, surfacing as spurious
  // "Premature close" errors on the second call. HTTP/1.1 is plenty
  // for the round-trip semantics this test covers, and connect-node
  // negotiates the Connect protocol over both transports identically.
  //
  // The gRPC-Health-v1 surface (which ping() probes) rides the same listener
  // on the real server; here we mount a hand-rolled JSON responder for
  // `/grpc.health.v1.Health/Check` in front of the Connect adapter so ping()
  // has an endpoint to hit. Everything else falls through to the adapter.
  const adapter = connectNodeAdapter({ routes: newStubRoutes(state) });
  server = http.createServer((req, res) => {
    if (req.url === "/grpc.health.v1.Health/Check") {
      const httpStatus = state.health?.httpStatus ?? 200;
      res.writeHead(httpStatus, { "Content-Type": "application/json" });
      res.end(
        httpStatus === 200 ? JSON.stringify({ status: state.health?.status ?? "SERVING" }) : "{}",
      );
      return;
    }
    adapter(req, res);
  });
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

  test("batchChunkSize above 65536 is clamped — no contrib-id collision inside one chunk", async () => {
    // A chunk larger than the uint16 index space would wrap index 65536 back
    // to 0 and collide two ids under one seq (#919). Clamp splits at 65536 so
    // the 65537th edge lands in a second chunk under a fresh seq.
    const c = connect(baseUrl, {
      options: { idempotentAdds: true, batchChunkSize: 70000 },
      transportOptions: { httpVersion: "1.1" },
    });
    try {
      state.addEdgesCalls = [];
      const edges = Array.from({ length: 65537 }, (_, i) => ({
        tail: "t",
        head: "h" + i,
        weight: 1,
      }));
      await c.addEdges(edges);

      // Unfixed code sends ONE 65537-edge chunk; fixed code splits at the clamp.
      expect(state.addEdgesCalls.map((call) => call.tails.length)).toEqual([65536, 1]);

      // The item that previously wrapped (index 65536 & 0xffff === 0) now lives
      // in a second chunk under a FRESH seq, so its id differs from chunk 0,
      // index 0.
      const first = state.addEdgesCalls[0].contribIds[0];
      const boundary = state.addEdgesCalls[1].contribIds[0];
      expect([...boundary]).not.toEqual([...first]);
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

describe("addDecayingEdge (#953)", () => {
  test("sends one expanded staircase batch and returns the post-add live weight", async () => {
    const c = newClient();
    try {
      state.addEdgesCalls = [];
      const live = await c.addDecayingEdge("a", "b", {
        initialWeight: 16,
        ratio: 0.5,
        steps: 5,
        intervalSeconds: 1,
      });
      // One AddEdges batch carrying the 8,4,2,1,1 drops...
      expect(state.addEdgesCalls.length).toBe(1);
      const call = state.addEdgesCalls[0];
      expect(call.tails).toEqual(["a", "a", "a", "a", "a"]);
      call.weights.forEach((w, i) => expect(w).toBeCloseTo([8, 4, 2, 1, 1][i], 4));
      // ...and the returned live weight is initialWeight (16), not the raw
      // 16,8,4,2,1 schedule's sum (31) — the telescoping check at the wire.
      expect(live).toBeCloseTo(16, 4);
    } finally {
      c.close();
    }
  });

  test("invalid opts short-circuit before any RPC", async () => {
    const c = newClient();
    try {
      state.addEdgesCalls = [];
      await expect(
        c.addDecayingEdge("a", "b", { initialWeight: 16, ratio: 2, steps: 5, intervalSeconds: 1 }),
      ).rejects.toThrow(InvalidArgumentError);
      expect(state.addEdgesCalls.length).toBe(0);
    } finally {
      c.close();
    }
  });
});

describe("backup / restore (#685)", () => {
  test("backup streams every vertex then every edge, decoded", async () => {
    const c = newClient();
    try {
      state.vertices.clear();
      state.edges.clear();
      await c.putVertex({ key: "bk/v/1", value: "alice" });
      await c.putVertex({ key: "bk/v/2", value: 42 });
      // Seed an edge directly (no AddEdge stub state coupling).
      state.edges.set("bk/v/1", new Map([["bk/v/2", 3.5]]));

      const records: BackupRecord[] = [];
      for await (const rec of c.backup()) records.push(rec);

      const verts = records.filter((r) => r.kind === "vertex");
      const edges = records.filter((r) => r.kind === "edge");
      expect(verts.length).toBe(2);
      expect(edges.length).toBe(1);
      // Vertices precede edges in the stream.
      expect(records.findIndex((r) => r.kind === "edge")).toBe(2);

      const v1 = verts.find((r) => r.kind === "vertex" && r.vertex.key === "bk/v/1");
      expect(v1?.kind === "vertex" && v1.vertex.value).toBe("alice");
      const e = edges[0];
      expect(e.kind === "edge" && e.edge.tail).toBe("bk/v/1");
      expect(e.kind === "edge" && e.edge.head).toBe("bk/v/2");
      expect(e.kind === "edge" && e.edge.weight).toBeCloseTo(3.5, 5);
    } finally {
      c.close();
    }
  });

  test("backup honors the vertex prefix filter", async () => {
    const c = newClient();
    try {
      state.vertices.clear();
      state.edges.clear();
      await c.putVertex({ key: "keep/1", value: 1 });
      await c.putVertex({ key: "drop/1", value: 2 });

      const keys: string[] = [];
      for await (const rec of c.backup({ prefix: "keep/" })) {
        if (rec.kind === "vertex") keys.push(rec.vertex.key);
      }
      expect(keys).toEqual(["keep/1"]);
    } finally {
      c.close();
    }
  });

  test("restore flushes remaining vertices before edges and reports counts", async () => {
    const c = newClient();
    try {
      state.vertices.clear();
      state.edges.clear();
      state.writeLog = [];
      // Interleaved input under a large chunk size: nothing flushes mid-stream,
      // so the final flush ordering is observed in isolation — the remaining
      // vertices must be written before the remaining edges (real vertex values
      // stay authoritative over PutEdges' auto-created bare endpoints).
      const records: BackupRecord[] = [
        { kind: "vertex", vertex: { key: "r/v/1", value: "x", kind: "string", expiration: null } },
        { kind: "edge", edge: { tail: "r/v/1", head: "r/v/2", weight: 1, expiration: null } },
        { kind: "vertex", vertex: { key: "r/v/2", value: "y", kind: "string", expiration: null } },
        { kind: "edge", edge: { tail: "r/v/2", head: "r/v/3", weight: 2, expiration: null } },
      ];

      const stats = await c.restore(records);
      expect(stats).toEqual({ vertices: 2, edges: 2 });
      expect(state.writeLog).toEqual([
        { kind: "vertices", count: 2 },
        { kind: "edges", count: 2 },
      ]);
    } finally {
      c.close();
    }
  });

  test("restore chunks batches at chunkSize", async () => {
    const c = newClient();
    try {
      state.vertices.clear();
      state.edges.clear();
      state.writeLog = [];
      // Five vertices then two edges — the real backup ordering. chunkSize 2
      // flushes vertices in [2,2] mid-stream, leaving a trailing [1]; the two
      // edges flush once at the end.
      const records: BackupRecord[] = [
        { kind: "vertex", vertex: { key: "c/v/1", value: 1, kind: "int64", expiration: null } },
        { kind: "vertex", vertex: { key: "c/v/2", value: 2, kind: "int64", expiration: null } },
        { kind: "vertex", vertex: { key: "c/v/3", value: 3, kind: "int64", expiration: null } },
        { kind: "vertex", vertex: { key: "c/v/4", value: 4, kind: "int64", expiration: null } },
        { kind: "vertex", vertex: { key: "c/v/5", value: 5, kind: "int64", expiration: null } },
        { kind: "edge", edge: { tail: "c/v/1", head: "c/v/2", weight: 1, expiration: null } },
        { kind: "edge", edge: { tail: "c/v/2", head: "c/v/3", weight: 1, expiration: null } },
      ];

      const stats = await c.restore(records, { chunkSize: 2 });
      expect(stats).toEqual({ vertices: 5, edges: 2 });
      expect(state.writeLog.filter((w) => w.kind === "vertices").map((w) => w.count)).toEqual([
        2, 2, 1,
      ]);
      expect(state.writeLog.filter((w) => w.kind === "edges").map((w) => w.count)).toEqual([2]);
    } finally {
      c.close();
    }
  });

  test("backup → restore preserves narrow numeric + bytes kinds losslessly", async () => {
    const c = newClient();
    try {
      state.vertices.clear();
      state.edges.clear();
      await c.putVertex({ key: "orig/i32", value: new Int32(-5) });
      await c.putVertex({ key: "orig/u64", value: new Uint64(2n ** 63n + 7n) });
      await c.putVertex({ key: "orig/f32", value: new Float32(1.5) });
      await c.putVertex({ key: "orig/bytes", value: new Uint8Array([1, 2, 3]) });

      // Dump, re-key under a fresh prefix, replay, then read back — the whole
      // decode → re-encode → putVertices → getVertex path must not widen kinds.
      const dumped: BackupRecord[] = [];
      for await (const rec of c.backup({ prefix: "orig/" })) dumped.push(rec);
      const rekeyed: BackupRecord[] = dumped.map((r) =>
        r.kind === "vertex"
          ? { kind: "vertex", vertex: { ...r.vertex, key: r.vertex.key.replace("orig/", "rest/") } }
          : r,
      );
      await c.restore(rekeyed);

      const i32 = await c.getVertex("rest/i32");
      expect(i32.kind).toBe("int32");
      expect(i32.value).toBe(-5);
      const u64 = await c.getVertex("rest/u64");
      expect(u64.kind).toBe("uint64");
      expect(u64.value).toBe(2n ** 63n + 7n);
      const f32 = await c.getVertex("rest/f32");
      expect(f32.kind).toBe("float32");
      expect(f32.value).toBeCloseTo(1.5, 6);
      const b = await c.getVertex("rest/bytes");
      expect(b.kind).toBe("bytes");
      expect(Array.from(b.value as Uint8Array)).toEqual([1, 2, 3]);
    } finally {
      c.close();
    }
  });

  test("NDJSON line codec round-trips records and is stable", () => {
    const recs: BackupRecord[] = [
      { kind: "vertex", vertex: { key: "n/i32", value: -5, kind: "int32", expiration: null } },
      {
        kind: "vertex",
        vertex: { key: "n/u64", value: 2n ** 63n + 7n, kind: "uint64", expiration: null },
      },
      {
        kind: "vertex",
        vertex: {
          key: "n/bytes",
          value: new Uint8Array([9, 8, 7]),
          kind: "bytes",
          expiration: null,
        },
      },
      { kind: "edge", edge: { tail: "n/i32", head: "n/u64", weight: 2.5, expiration: null } },
    ];
    for (const rec of recs) {
      const line = backupRecordToNdjson(rec);
      expect(line).not.toContain("\n");
      const back = backupRecordFromNdjson(line);
      // Re-serialising the decoded record must be byte-identical (stable codec).
      expect(backupRecordToNdjson(back)).toBe(line);
    }
    // uint64 magnitude is carried as a string, not a lossy JS number.
    expect(backupRecordToNdjson(recs[1])).toContain(`"uint64":"${2n ** 63n + 7n}"`);
  });

  test("backupRecordFromNdjson rejects an unknown record kind", () => {
    expect(() => backupRecordFromNdjson(JSON.stringify({ kind: "bogus", key: "x" }))).toThrow(
      InvalidArgumentError,
    );
  });
});

describe("ping (health check)", () => {
  test("resolves when the server reports SERVING", async () => {
    const c = newClient();
    try {
      state.health = { status: "SERVING" };
      await expect(c.ping()).resolves.toBeUndefined();
    } finally {
      c.close();
    }
  });

  test("accepts the connectrpc-prefixed SERVING_STATUS_SERVING enum name", async () => {
    const c = newClient();
    try {
      state.health = { status: "SERVING_STATUS_SERVING" };
      await expect(c.ping()).resolves.toBeUndefined();
    } finally {
      c.close();
    }
  });

  test("throws HealthStatusError on a non-SERVING status", async () => {
    const c = newClient();
    try {
      state.health = { status: "NOT_SERVING" };
      await expect(c.ping()).rejects.toBeInstanceOf(HealthStatusError);
      await expect(c.ping()).rejects.toMatchObject({ status: "NOT_SERVING" });
    } finally {
      state.health = { status: "SERVING" };
      c.close();
    }
  });

  test("throws a generic LanternError on a non-200 health response", async () => {
    const c = newClient();
    try {
      state.health = { httpStatus: 503 };
      await expect(c.ping()).rejects.toThrow(/HTTP 503/);
    } finally {
      state.health = { status: "SERVING" };
      c.close();
    }
  });

  test("a withTransport client built without a baseUrl rejects ping", async () => {
    const transport = createConnectTransport({ baseUrl, httpVersion: "1.1" });
    const c = Lantern.withTransport(transport);
    try {
      await expect(c.ping()).rejects.toBeInstanceOf(InvalidArgumentError);
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

describe("scan order (#898)", () => {
  test("default and explicit asc send SCAN_ORDER_ASC", async () => {
    const c = newClient();
    try {
      await c.putVertices([
        { key: "so:1", value: "a" },
        { key: "so:2", value: "b" },
      ]);

      state.lastScanOrder = undefined;
      await c.scanVertexKeys("so:", { limit: 10 });
      expect(state.lastScanOrder).toBe(ScanOrder.ASC);

      state.lastScanOrder = undefined;
      await c.scanVertexKeys("so:", { limit: 10, order: "asc" });
      expect(state.lastScanOrder).toBe(ScanOrder.ASC);
    } finally {
      c.close();
    }
  });

  test("descending keys scan walks high-to-low and paginates", async () => {
    const c = newClient();
    try {
      await c.putVertices([
        { key: "sd:1", value: "a" },
        { key: "sd:2", value: "b" },
        { key: "sd:3", value: "c" },
      ]);

      const p1 = await c.scanVertexKeys("sd:", { limit: 2, order: "desc" });
      expect(state.lastScanOrder).toBe(ScanOrder.DESC);
      expect(p1.keys).toEqual(["sd:3", "sd:2"]);
      expect(p1.nextCursor.length).toBeGreaterThan(0);

      const p2 = await c.scanVertexKeys("sd:", { limit: 2, order: "desc", cursor: p1.nextCursor });
      expect(p2.keys).toEqual(["sd:1"]);
      expect(p2.nextCursor.length).toBe(0);

      const all: string[] = [];
      for await (const page of c.scanVertexKeysAll("sd:", 2, undefined, "desc")) all.push(...page);
      expect(all).toEqual(["sd:3", "sd:2", "sd:1"]);
    } finally {
      c.close();
    }
  });

  test("descending vertices scan returns whole vertices high-to-low", async () => {
    const c = newClient();
    try {
      await c.putVertices([
        { key: "sv:1", value: "a" },
        { key: "sv:2", value: "b" },
        { key: "sv:3", value: "c" },
      ]);

      const all: string[] = [];
      for await (const page of c.scanVerticesAll("sv:", 2, undefined, "desc")) {
        for (const v of page) all.push(v.key);
      }
      expect(state.lastScanOrder).toBe(ScanOrder.DESC);
      expect(all).toEqual(["sv:3", "sv:2", "sv:1"]);
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

  test("omitting the family is an InvalidArgumentError", async () => {
    const c = newClient();
    try {
      const before = state.lastIlluminate;
      await expect(c.illuminate("alice", {} as never)).rejects.toThrow(/exactly one/);
      expect(state.lastIlluminate).toBe(before);
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
      await expect(
        c.illuminate("alice", {
          bfs: { step: 1, fanOut: 1 },
          ppr: {},
        } as unknown as IlluminateOptions),
      ).rejects.toThrow(/exactly one/);
    } finally {
      c.close();
    }
  });

  test("rejects a zero BFS step or fanOut before the wire", async () => {
    const c = newClient();
    try {
      const before = state.lastIlluminate;
      await expect(c.illuminate("alice", { bfs: { step: 0, fanOut: 1 } })).rejects.toThrow(
        /must both be positive integers/,
      );
      await expect(c.illuminate("alice", { bfs: { step: 1, fanOut: 0 } })).rejects.toThrow(
        /must both be positive integers/,
      );
      expect(state.lastIlluminate).toBe(before);
    } finally {
      c.close();
    }
  });

  test("forwards the reduction on the bfs arm", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", {
        bfs: { step: 1, fanOut: 1, reduction: Reduction.SHORTEST_PATH_TREE },
      });
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

  test("other relevance options preserve the server-default match mode", async () => {
    const c = newClient();
    try {
      await c.searchVertices("alpha beta", { fuzziness: 1 });
      expect(state.lastSearch?.matchMode).toBe(MatchMode.UNSPECIFIED);
      expect(state.lastSearch?.fuzziness).toBe(1);
    } finally {
      c.close();
    }
  });

  test("forwards the complete valid relevance option set", async () => {
    const c = newClient();
    try {
      await c.searchVertices("alpha beta", {
        matchMode: "min-should",
        minShouldMatch: 2,
        fuzziness: 1,
        prefixTerms: true,
      });
      expect(state.lastSearch).toMatchObject({
        matchMode: MatchMode.MIN_SHOULD,
        minShouldMatch: 2,
        fuzziness: 1,
        prefixTerms: true,
      });
    } finally {
      c.close();
    }
  });

  test.each([
    { minShouldMatch: 1 },
    { matchMode: "any", minShouldMatch: 1 },
    { fuzziness: 3 },
    { limit: Number.NaN },
    { limit: 1.5 },
    { phrase: true, matchMode: "all" },
    { phrase: true, fuzziness: 1 },
    { phrase: true, prefixTerms: true },
  ] as const)("rejects invalid options locally without transport: %o", async (opts) => {
    const c = newClient();
    const before = state.lastSearch;
    try {
      await expect(c.searchVertices("alpha beta", opts as never)).rejects.toBeInstanceOf(
        InvalidArgumentError,
      );
      expect(state.lastSearch).toBe(before);
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
      try {
        await c.searchVertices("q");
        expect.unreachable("search should fail");
      } catch (error) {
        expect(error).toBeInstanceOf(FailedPreconditionError);
        expect((error as FailedPreconditionError).reason).toBe(SearchErrorReason.SEARCH_DISABLED);
      }
    } finally {
      state.searchDisabled = false;
      c.close();
    }
  });

  test("positions-disabled remains a distinct typed reason", async () => {
    const c = newClient();
    state.searchPositionsDisabled = true;
    try {
      await c.searchVertices("alpha beta", { phrase: true });
      expect.unreachable("phrase search should fail");
    } catch (error) {
      expect(error).toBeInstanceOf(FailedPreconditionError);
      expect((error as FailedPreconditionError).reason).toBe(
        SearchErrorReason.SEARCH_POSITIONS_DISABLED,
      );
    } finally {
      state.searchPositionsDisabled = false;
      c.close();
    }
  });

  test("work budget exposes bounded reason and work kind", async () => {
    const c = newClient();
    state.searchResource = {
      reason: SearchErrorReason.SEARCH_WORK_BUDGET_EXHAUSTED,
      workKind: "posting_visits",
    };
    try {
      await c.searchVertices("alpha");
      expect.unreachable("budgeted search should fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ResourceExhaustedError);
      expect((error as ResourceExhaustedError).reason).toBe(
        SearchErrorReason.SEARCH_WORK_BUDGET_EXHAUSTED,
      );
      expect((error as ResourceExhaustedError).workKind).toBe("posting_visits");
    } finally {
      state.searchResource = undefined;
      c.close();
    }
  });

  test("admission remains distinct from work exhaustion", async () => {
    const c = newClient();
    state.searchResource = { reason: SearchErrorReason.SEARCH_ADMISSION_SATURATED };
    try {
      await c.searchVertices("alpha");
      expect.unreachable("saturated search should fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ResourceExhaustedError);
      expect((error as ResourceExhaustedError).reason).toBe(
        SearchErrorReason.SEARCH_ADMISSION_SATURATED,
      );
      expect((error as ResourceExhaustedError).workKind).toBe("");
    } finally {
      state.searchResource = undefined;
      c.close();
    }
  });

  test("index write budget remains a distinct typed reason", async () => {
    const c = newClient();
    state.searchResource = {
      reason: SearchErrorReason.SEARCH_INDEX_BUDGET_EXHAUSTED,
      workKind: "document_bytes",
    };
    try {
      await c.searchVertices("alpha");
      expect.unreachable("budgeted search should fail");
    } catch (error) {
      expect(error).toBeInstanceOf(ResourceExhaustedError);
      expect((error as ResourceExhaustedError).reason).toBe(
        SearchErrorReason.SEARCH_INDEX_BUDGET_EXHAUSTED,
      );
      expect((error as ResourceExhaustedError).workKind).toBe("document_bytes");
    } finally {
      state.searchResource = undefined;
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
