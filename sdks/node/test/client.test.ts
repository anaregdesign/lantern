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

import { Lantern, FailedPreconditionError, NotFoundError, connect } from "../src/index.js";
import { LanternService, VertexSchema } from "../src/gen/graph/v1/graph_pb.js";

interface StubState {
  vertices: Map<string, ReturnType<typeof create<typeof VertexSchema>>>;
  /** Last IlluminateRequest the stub observed, for request-building assertions (#605). */
  lastIlluminate?: { seed: string; step: number; k: number; vertexPrefix: string };
  /** Last SearchVerticesRequest the stub observed, for request-building assertions (#639). */
  lastSearch?: { query: string; limit: number; prefix: string };
  /** Ranked hits the searchVertices stub returns (descending relevance). */
  searchHits?: { key: string; score: number }[];
  /** When true, searchVertices rejects with FAILED_PRECONDITION (index disabled). */
  searchDisabled?: boolean;
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
          state.vertices.set(req.vertex.key, req.vertex);
        }
        return {};
      },
      async putVertices(req) {
        for (const v of req.vertices) {
          state.vertices.set(v.key, v);
        }
        return {};
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
        state.lastIlluminate = {
          seed: req.seed,
          step: req.step,
          k: req.k,
          vertexPrefix: req.vertexPrefix,
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
      // Remaining methods are intentionally absent — the connect-node
      // adapter rejects them with Code.Unimplemented, which the SDK
      // surfaces as the generic LanternError. Tests that need these
      // RPCs would extend the stub.
    });
  };
}

let server: http.Server;
let baseUrl: string;
const state: StubState = { vertices: new Map() };

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
});

describe("illuminate request building (#605)", () => {
  test("forwards vertexPrefix onto the request", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", { step: 2, k: 5, vertexPrefix: "users/" });
      expect(state.lastIlluminate?.seed).toBe("alice");
      expect(state.lastIlluminate?.step).toBe(2);
      expect(state.lastIlluminate?.k).toBe(5);
      expect(state.lastIlluminate?.vertexPrefix).toBe("users/");
    } finally {
      c.close();
    }
  });

  test("omitting vertexPrefix yields an empty string (no filter)", async () => {
    const c = newClient();
    try {
      await c.illuminate("alice", { step: 1 });
      expect(state.lastIlluminate?.vertexPrefix).toBe("");
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
