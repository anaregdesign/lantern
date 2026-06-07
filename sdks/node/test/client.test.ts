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

import { Lantern, NotFoundError, connect } from "../src/index.js";
import { LanternService, VertexSchema } from "../src/gen/graph/v1/graph_pb.js";

interface StubState {
  vertices: Map<string, ReturnType<typeof create<typeof VertexSchema>>>;
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
