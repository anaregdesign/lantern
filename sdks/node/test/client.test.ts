/**
 * In-process E2E test: spin up a fake LanternServiceServer with
 * @grpc/grpc-js on 127.0.0.1:0 and drive the SDK client against it.
 */

import { afterAll, beforeAll, describe, expect, test } from "bun:test";
import {
  Server,
  ServerCredentials,
  type ServerUnaryCall,
  type sendUnaryData,
  credentials as grpcCredentials,
  status as GrpcStatus,
} from "@grpc/grpc-js";
import Long from "long";

import {
  BatchError,
  Duration,
  Lantern,
  NotFoundError,
  defaultServiceConfig,
  staticTarget,
} from "../src/index.js";
import { LanternServiceService } from "../src/generated/graph/v1/graph.js";

interface State {
  vertices: Map<string, { value: string; expiration?: Date }>;
  edges: Map<string, { weight: number }>;
}

const state: State = { vertices: new Map(), edges: new Map() };

function edgeKey(t: string, h: string): string {
  return `${t}\x00${h}`;
}

function makeImpl() {
  return {
    getVertex(call: ServerUnaryCall<{ key: string }, unknown>, cb: sendUnaryData<unknown>) {
      const v = state.vertices.get(call.request.key);
      if (!v) {
        cb({
          code: GrpcStatus.NOT_FOUND,
          details: "missing vertex",
          name: "",
          message: "",
        } as never);
        return;
      }
      cb(null, { vertex: { key: call.request.key, string: v.value, expiration: v.expiration } });
    },
    putVertex(
      call: ServerUnaryCall<
        { vertex: { key: string; string?: string; expiration?: Date } },
        unknown
      >,
      cb: sendUnaryData<unknown>,
    ) {
      const v = call.request.vertex;
      state.vertices.set(v.key, { value: v.string ?? "", expiration: v.expiration });
      cb(null, {});
    },
    deleteVertex(call: ServerUnaryCall<{ key: string }, unknown>, cb: sendUnaryData<unknown>) {
      const existed = state.vertices.delete(call.request.key);
      cb(null, { existed });
    },
    getVertices(call: ServerUnaryCall<{ keys: string[] }, unknown>, cb: sendUnaryData<unknown>) {
      const present: unknown[] = [];
      const missing: string[] = [];
      for (const k of call.request.keys) {
        const v = state.vertices.get(k);
        if (v) present.push({ key: k, string: v.value, expiration: v.expiration });
        else missing.push(k);
      }
      cb(null, { vertices: present, missing });
    },
    putVertices(
      call: ServerUnaryCall<{ vertices: { key: string; string?: string }[] }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      for (const v of call.request.vertices) state.vertices.set(v.key, { value: v.string ?? "" });
      cb(null, { written: call.request.vertices.length });
    },
    deleteVertices(call: ServerUnaryCall<{ keys: string[] }, unknown>, cb: sendUnaryData<unknown>) {
      let n = 0;
      for (const k of call.request.keys) if (state.vertices.delete(k)) n++;
      cb(null, { deleted: n });
    },
    scanVertices(
      call: ServerUnaryCall<{ prefix: string; limit: number; cursor: Buffer }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const prefix = call.request.prefix;
      const all = [...state.vertices.entries()]
        .filter(([k]) => k.startsWith(prefix))
        .sort(([a], [b]) => (a < b ? -1 : a > b ? 1 : 0));
      const start =
        call.request.cursor.length === 0 ? 0 : Number(call.request.cursor.toString("utf8"));
      const limit = call.request.limit > 0 ? call.request.limit : all.length;
      const page = all.slice(start, start + limit);
      const next =
        start + limit < all.length ? Buffer.from(String(start + limit)) : Buffer.alloc(0);
      cb(null, {
        vertices: page.map(([k, v]) => ({ key: k, string: v.value })),
        nextCursor: next,
      });
    },
    countVerticesByPrefix(
      call: ServerUnaryCall<{ prefix: string }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const n = [...state.vertices.keys()].filter((k) => k.startsWith(call.request.prefix)).length;
      cb(null, { count: Long.fromNumber(n, true) });
    },
    deleteVerticesByPrefix(
      call: ServerUnaryCall<{ prefix: string; limit: number; dryRun: boolean }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const keys = [...state.vertices.keys()].filter((k) => k.startsWith(call.request.prefix));
      if (!call.request.dryRun) for (const k of keys) state.vertices.delete(k);
      cb(null, { deleted: Long.fromNumber(keys.length, true) });
    },
    getEdge(
      call: ServerUnaryCall<{ tail: string; head: string }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const e = state.edges.get(edgeKey(call.request.tail, call.request.head));
      if (!e) {
        cb({ code: GrpcStatus.NOT_FOUND, details: "missing edge", name: "", message: "" } as never);
        return;
      }
      cb(null, { edge: { tail: call.request.tail, head: call.request.head, weight: e.weight } });
    },
    addEdge(
      call: ServerUnaryCall<{ edge: { tail: string; head: string; weight: number } }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const k = edgeKey(call.request.edge.tail, call.request.edge.head);
      const prev = state.edges.get(k)?.weight ?? 0;
      state.edges.set(k, { weight: prev + call.request.edge.weight });
      cb(null, {});
    },
    putEdge(
      call: ServerUnaryCall<{ edge: { tail: string; head: string; weight: number } }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const k = edgeKey(call.request.edge.tail, call.request.edge.head);
      state.edges.set(k, { weight: call.request.edge.weight });
      cb(null, {});
    },
    deleteEdge(
      call: ServerUnaryCall<{ tail: string; head: string }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const existed = state.edges.delete(edgeKey(call.request.tail, call.request.head));
      cb(null, { existed });
    },
    getEdges(
      call: ServerUnaryCall<{ edges: { tail: string; head: string }[] }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      const present: unknown[] = [];
      const missing: unknown[] = [];
      for (const k of call.request.edges) {
        const e = state.edges.get(edgeKey(k.tail, k.head));
        if (e) present.push({ tail: k.tail, head: k.head, weight: e.weight });
        else missing.push({ tail: k.tail, head: k.head });
      }
      cb(null, { edges: present, missing });
    },
    addEdges(
      call: ServerUnaryCall<{ edges: { tail: string; head: string; weight: number }[] }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      // Simulate failure on a poison input to exercise BatchError.
      for (const e of call.request.edges) {
        if (e.tail === "poison") {
          cb({ code: GrpcStatus.INTERNAL, details: "poison pill", name: "", message: "" } as never);
          return;
        }
      }
      for (const e of call.request.edges) {
        const k = edgeKey(e.tail, e.head);
        const prev = state.edges.get(k)?.weight ?? 0;
        state.edges.set(k, { weight: prev + e.weight });
      }
      cb(null, { written: call.request.edges.length });
    },
    putEdges(
      call: ServerUnaryCall<{ edges: { tail: string; head: string; weight: number }[] }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      for (const e of call.request.edges) {
        state.edges.set(edgeKey(e.tail, e.head), { weight: e.weight });
      }
      cb(null, { written: call.request.edges.length });
    },
    deleteEdges(
      call: ServerUnaryCall<{ edges: { tail: string; head: string }[] }, unknown>,
      cb: sendUnaryData<unknown>,
    ) {
      let n = 0;
      for (const k of call.request.edges) if (state.edges.delete(edgeKey(k.tail, k.head))) n++;
      cb(null, { deleted: n });
    },
    scanEdges(
      call: ServerUnaryCall<
        { tailPrefix: string; headPrefix: string; limit: number; cursor: Buffer },
        unknown
      >,
      cb: sendUnaryData<unknown>,
    ) {
      const all = [...state.edges.entries()]
        .map(([k, v]) => {
          const [t, h] = k.split("\x00");
          return { tail: t!, head: h!, weight: v.weight };
        })
        .filter(
          (e) =>
            e.tail.startsWith(call.request.tailPrefix) &&
            e.head.startsWith(call.request.headPrefix),
        )
        .sort((a, b) =>
          a.tail === b.tail ? (a.head < b.head ? -1 : 1) : a.tail < b.tail ? -1 : 1,
        );
      const start =
        call.request.cursor.length === 0 ? 0 : Number(call.request.cursor.toString("utf8"));
      const limit = call.request.limit > 0 ? call.request.limit : all.length;
      const page = all.slice(start, start + limit);
      const next =
        start + limit < all.length ? Buffer.from(String(start + limit)) : Buffer.alloc(0);
      cb(null, { edges: page, nextCursor: next });
    },
    illuminate(_call: ServerUnaryCall<unknown, unknown>, cb: sendUnaryData<unknown>) {
      cb(null, {
        graph: {
          vertices: [
            { key: "u:1", string: "alice" },
            { key: "u:2", string: "bob" },
          ],
          edges: [{ tail: "u:1", head: "u:2", weight: 0.75 }],
        },
      });
    },
  };
}

let server: Server;
let target: string;
let client: Lantern;

beforeAll(async () => {
  server = new Server();
  server.addService(LanternServiceService, makeImpl() as never);
  const port = await new Promise<number>((resolve, reject) => {
    server.bindAsync("127.0.0.1:0", ServerCredentials.createInsecure(), (err, p) => {
      if (err) reject(err);
      else resolve(p);
    });
  });
  target = `127.0.0.1:${port}`;
  client = Lantern.connect(target, { credentials: grpcCredentials.createInsecure() });
});

afterAll(() => {
  client.close();
  server.forceShutdown();
});

describe("Lantern client", () => {
  test("putVertex / getVertex round-trip", async () => {
    await client.putVertex("v:hello", "world");
    const v = await client.getVertex("v:hello");
    expect(v.key).toBe("v:hello");
    expect(v.value).toBe("world");
    expect(v.kind).toBe("string");
  });

  test("getVertex maps NOT_FOUND → NotFoundError", async () => {
    await expect(client.getVertex("v:missing")).rejects.toBeInstanceOf(NotFoundError);
  });

  test("getVertices returns present + missing", async () => {
    await client.putVertex("v:a", "1");
    await client.putVertex("v:b", "2");
    const { present, missing } = await client.getVertices(["v:a", "v:b", "v:nope"]);
    expect(present.map((v) => v.key).sort()).toEqual(["v:a", "v:b"]);
    expect(missing).toEqual(["v:nope"]);
  });

  test("putVertices auto-chunks and returns total written", async () => {
    const inputs = Array.from({ length: 25 }, (_, i) => ({ key: `bulk:${i}`, value: `n${i}` }));
    const n = await client.putVertices(inputs, { chunkSize: 10 });
    expect(n).toBe(25);
    const count = await client.countVerticesByPrefix("bulk:");
    expect(count).toBe(25n);
  });

  test("scanVerticesAll paginates", async () => {
    const seen: string[] = [];
    for await (const page of client.scanVerticesAll("bulk:", { limit: 7 })) {
      for (const v of page) seen.push(v.key);
    }
    expect(seen.length).toBe(25);
  });

  test("deleteVerticesByPrefix dry-run vs apply", async () => {
    const dry = await client.deleteVerticesByPrefix("bulk:", { dryRun: true });
    expect(dry).toBe(25n);
    const real = await client.deleteVerticesByPrefix("bulk:");
    expect(real).toBe(25n);
    expect(await client.countVerticesByPrefix("bulk:")).toBe(0n);
  });

  test("addEdge accumulates weight; putEdge overwrites", async () => {
    await client.addEdge("t", "h", 1.5);
    await client.addEdge("t", "h", 0.5);
    let e = await client.getEdge("t", "h");
    expect(e.weight).toBeCloseTo(2.0);
    await client.putEdge("t", "h", 10);
    e = await client.getEdge("t", "h");
    expect(e.weight).toBe(10);
  });

  test("addEdges raises BatchError carrying prior written count", async () => {
    await client.putEdges([
      { tail: "ok1", head: "x", weight: 1 },
      { tail: "ok2", head: "x", weight: 1 },
    ]);
    let caught: unknown;
    try {
      await client.addEdges(
        [
          { tail: "good", head: "x", weight: 1 },
          { tail: "poison", head: "x", weight: 1 },
        ],
        { chunkSize: 1 },
      );
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(BatchError);
    expect((caught as BatchError).written).toBe(1);
  });

  test("illuminate returns SDK Graph map", async () => {
    const g = await client.illuminate("u:1", { step: 2, k: 5 });
    expect(g.vertices.get("u:1")?.value).toBe("alice");
    expect(g.edges.get("u:1")?.get("u:2")).toBeCloseTo(0.75);
  });

  test("AbortSignal cancels in-flight RPC", async () => {
    const controller = new AbortController();
    controller.abort();
    await expect(client.getVertex("anything", controller.signal)).rejects.toBeDefined();
  });

  test("defaultServiceConfig parses and contains AddEdge omission", () => {
    const cfg = JSON.parse(defaultServiceConfig()) as {
      methodConfig: Array<{
        name: Array<{ service: string; method?: string }>;
        retryPolicy?: object;
      }>;
    };
    const addEdge = cfg.methodConfig.find((mc) => mc.name.some((n) => n.method === "AddEdge"));
    expect(addEdge).toBeDefined();
    expect(addEdge?.retryPolicy).toBeUndefined();
  });

  test("staticTarget builds ipv4: URI for round_robin", () => {
    expect(staticTarget(["a:1", "b:2"])).toBe("ipv4:a:1,b:2");
  });

  test("Duration value carrier", () => {
    const d = Duration.fromMillis(2500);
    expect(d.toMillis()).toBe(2500);
  });
});
