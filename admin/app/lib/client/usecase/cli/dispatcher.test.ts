/**
 * Tests for `dispatcher.ts` covering the four parity fixes shipped
 * together (#428 typed value coercion, #429 TTL → expiration,
 * #430 NotFound → error chip, #432 drop extra count RPC).
 *
 * The dispatcher's only collaborator is the SDK `Lantern` client, so
 * we hand-roll a `FakeLanternClient` that records calls and lets
 * individual tests stub out specific responses. Anything we do not
 * stub throws, so a test that forgets to wire a method fails loudly
 * rather than silently passing.
 */

import { describe, expect, test } from "bun:test";
import {
  InvalidArgumentError,
  NotFoundError,
  Objective as SdkObjective,
  Reduction as SdkReduction,
} from "lantern-sdk/web";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import {
  coerceValue,
  dispatch,
  ttlSecondsToExpiration,
  writeEcho,
} from "./dispatcher";
import type { Command } from "~/lib/cli/types";
import { parse } from "~/lib/cli/parser";

// ----------------------------------------------------------------------------
// FakeLanternClient
// ----------------------------------------------------------------------------

interface RecordedCall<Args extends readonly unknown[] = readonly unknown[]> {
  method: string;
  args: Args;
}

class FakeLanternClient {
  readonly calls: RecordedCall[] = [];
  readonly stubs = new Map<string, (...args: unknown[]) => unknown>();

  stub(method: string, fn: (...args: unknown[]) => unknown): void {
    this.stubs.set(method, fn);
  }

  private invoke(method: string, args: unknown[]): unknown {
    this.calls.push({ method, args });
    const stub = this.stubs.get(method);
    if (!stub) {
      throw new Error(`FakeLanternClient: unstubbed method ${method}`);
    }
    return stub(...args);
  }

  // ─── only the surface the dispatcher actually touches ────────────────
  getVertex(key: string, signal?: AbortSignal): unknown {
    return this.invoke("getVertex", [key, signal]);
  }
  putVertex(input: unknown, signal?: AbortSignal): unknown {
    return this.invoke("putVertex", [input, signal]);
  }
  deleteVertex(key: string, signal?: AbortSignal): unknown {
    return this.invoke("deleteVertex", [key, signal]);
  }
  scanVertices(prefix: string, opts?: unknown, signal?: AbortSignal): unknown {
    return this.invoke("scanVertices", [prefix, opts, signal]);
  }
  scanVertexKeys(
    prefix: string,
    opts?: unknown,
    signal?: AbortSignal,
  ): unknown {
    return this.invoke("scanVertexKeys", [prefix, opts, signal]);
  }
  searchVerticesPage(
    query: string,
    opts?: unknown,
    signal?: AbortSignal,
  ): unknown {
    return this.invoke("searchVerticesPage", [query, opts, signal]);
  }
  searchVerticesIter(
    query: string,
    opts?: unknown,
    signal?: AbortSignal,
  ): AsyncIterable<unknown> {
    return this.invoke("searchVerticesIter", [
      query,
      opts,
      signal,
    ]) as AsyncIterable<unknown>;
  }
  countVerticesByPrefix(prefix: string, signal?: AbortSignal): unknown {
    return this.invoke("countVerticesByPrefix", [prefix, signal]);
  }
  getEdge(tail: string, head: string, signal?: AbortSignal): unknown {
    return this.invoke("getEdge", [tail, head, signal]);
  }
  putEdge(input: unknown, signal?: AbortSignal): unknown {
    return this.invoke("putEdge", [input, signal]);
  }
  addEdge(input: unknown, signal?: AbortSignal): unknown {
    return this.invoke("addEdge", [input, signal]);
  }
  addDecayingEdge(
    tail: string,
    head: string,
    opts: unknown,
    signal?: AbortSignal,
  ): unknown {
    return this.invoke("addDecayingEdge", [tail, head, opts, signal]);
  }
  deleteEdge(tail: string, head: string, signal?: AbortSignal): unknown {
    return this.invoke("deleteEdge", [tail, head, signal]);
  }
  scanEdges(opts?: unknown, signal?: AbortSignal): unknown {
    return this.invoke("scanEdges", [opts, signal]);
  }
  illuminate(seed: string, opts?: unknown, signal?: AbortSignal): unknown {
    return this.invoke("illuminate", [seed, opts, signal]);
  }
}

const asClient = (fake: FakeLanternClient): LanternClient =>
  fake as unknown as LanternClient;

// ----------------------------------------------------------------------------
// Pure helpers
// ----------------------------------------------------------------------------

describe("coerceValue (#428 cascade matches Go cli/parser Value())", () => {
  test("integer token → int64 string-preserved", () => {
    expect(coerceValue("0")).toEqual({ int64: "0" });
    expect(coerceValue("42")).toEqual({ int64: "42" });
    expect(coerceValue("-7")).toEqual({ int64: "-7" });
    expect(coerceValue("+9")).toEqual({ int64: "+9" });
    // Past 2^53 — must not lose precision.
    expect(coerceValue("9223372036854775000")).toEqual({
      int64: "9223372036854775000",
    });
  });

  test("decimal / scientific → float64", () => {
    expect(coerceValue("3.14")).toEqual({ float64: 3.14 });
    expect(coerceValue(".5")).toEqual({ float64: 0.5 });
    expect(coerceValue("2.")).toEqual({ float64: 2 });
    expect(coerceValue("1e3")).toEqual({ float64: 1000 });
    expect(coerceValue("-2.5E-2")).toEqual({ float64: -0.025 });
  });

  test("Go-style bool tokens → bool (exactly strconv.ParseBool's set)", () => {
    for (const t of ["t", "T", "TRUE", "true", "True"]) {
      expect(coerceValue(t)).toEqual({ bool: true });
    }
    for (const f of ["f", "F", "FALSE", "false", "False"]) {
      expect(coerceValue(f)).toEqual({ bool: false });
    }
    // "1" / "0" are caught by the int branch first, matching Go.
    expect(coerceValue("1")).toEqual({ int64: "1" });
    expect(coerceValue("0")).toEqual({ int64: "0" });
  });

  test("RFC3339 timestamp → timestamp (literal preserved)", () => {
    expect(coerceValue("2024-01-02T03:04:05Z")).toEqual({
      timestamp: "2024-01-02T03:04:05Z",
    });
    expect(coerceValue("2024-01-02T03:04:05.123Z")).toEqual({
      timestamp: "2024-01-02T03:04:05.123Z",
    });
    expect(coerceValue("2024-01-02T03:04:05+09:00")).toEqual({
      timestamp: "2024-01-02T03:04:05+09:00",
    });
  });

  test("anything else → string (fall-through)", () => {
    expect(coerceValue("hello")).toEqual({ string: "hello" });
    expect(coerceValue("CamelValue")).toEqual({ string: "CamelValue" });
    expect(coerceValue("not-a-date")).toEqual({ string: "not-a-date" });
    expect(coerceValue("inf")).toEqual({ string: "inf" });
    expect(coerceValue("nan")).toEqual({ string: "nan" });
    // Almost-but-not RFC3339.
    expect(coerceValue("2024-01-02")).toEqual({ string: "2024-01-02" });
    expect(coerceValue("2024-01-02T03:04")).toEqual({
      string: "2024-01-02T03:04",
    });
  });

  test("#679 type= forces the value field (and rejects a mismatch)", () => {
    expect(coerceValue("123", "string")).toEqual({ string: "123" });
    expect(coerceValue("007", "string")).toEqual({ string: "007" });
    expect(coerceValue("42", "int")).toEqual({ int64: "42" });
    expect(coerceValue("3.5", "float")).toEqual({ float64: 3.5 });
    expect(coerceValue("true", "bool")).toEqual({ bool: true });
    expect(coerceValue("2024-01-02T03:04:05Z", "datetime")).toEqual({
      timestamp: "2024-01-02T03:04:05Z",
    });
    expect(coerceValue("30s", "duration")).toEqual({ duration: "30s" });
    // json objects re-encode to a compact string; json scalars coerce naturally.
    expect(coerceValue('{"a":1}', "json")).toEqual({ string: '{"a":1}' });
    expect(coerceValue("true", "json")).toEqual({ bool: true });
    // a value that does not match its forced type throws.
    expect(() => coerceValue("abc", "int")).toThrow();
    expect(() => coerceValue("abc", "datetime")).toThrow();
    expect(() => coerceValue("{bad", "json")).toThrow();
  });
});

describe("ttlSecondsToExpiration (#429, #523)", () => {
  test("returns now+ttl as ISO8601 for a positive ttl", () => {
    const before = Date.now();
    const iso = ttlSecondsToExpiration(60);
    const after = Date.now();
    expect(iso).toBeDefined();
    const t = new Date(iso!).getTime();
    expect(t).toBeGreaterThanOrEqual(before + 60_000);
    expect(t).toBeLessThanOrEqual(after + 60_000);
    expect(iso!.endsWith("Z")).toBe(true);
  });

  test("returns undefined for a null (omitted) ttl ⇒ permanent (#523)", () => {
    expect(ttlSecondsToExpiration(null)).toBeUndefined();
  });
});

// ----------------------------------------------------------------------------
// Wiring tests
// ----------------------------------------------------------------------------

describe("dispatch put vertex (#428 + #429)", () => {
  test("integer round-trips as BigInt with carried expiration (#428 + #429)", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "answer",
      value: "42",
      ttlSeconds: 120,
      valueType: "auto",
    };
    const before = Date.now();
    await dispatch({ client: asClient(fake), command: cmd });
    const after = Date.now();
    const call = fake.calls.find((c) => c.method === "putVertex");
    expect(call).toBeDefined();
    const input = call!.args[0] as {
      key: string;
      value: unknown;
      expiration?: Date;
    };
    expect(input.key).toBe("answer");
    expect(input.value).toBe(42n);
    expect(input.expiration).toBeInstanceOf(Date);
    const expMs = input.expiration!.getTime();
    expect(expMs).toBeGreaterThanOrEqual(before + 120_000);
    expect(expMs).toBeLessThanOrEqual(after + 120_000);
  });

  test("string falls through and carries expiration", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "greeting",
      value: "hello",
      ttlSeconds: 60,
      valueType: "auto",
    };
    await dispatch({ client: asClient(fake), command: cmd });
    const call = fake.calls.find((c) => c.method === "putVertex");
    const input = call!.args[0] as {
      key: string;
      value: unknown;
      expiration?: Date;
    };
    expect(input.value).toBe("hello");
    expect(input.expiration).toBeInstanceOf(Date);
  });

  test("bool token round-trips as boolean", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "flag",
      value: "true",
      ttlSeconds: 60,
      valueType: "auto",
    };
    await dispatch({ client: asClient(fake), command: cmd });
    const call = fake.calls.find((c) => c.method === "putVertex");
    const input = call!.args[0] as { value: unknown };
    expect(input.value).toBe(true);
  });

  test("float token round-trips as JS number", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "pi",
      value: "3.14",
      ttlSeconds: 60,
      valueType: "auto",
    };
    await dispatch({ client: asClient(fake), command: cmd });
    const call = fake.calls.find((c) => c.method === "putVertex");
    const input = call!.args[0] as { value: unknown };
    expect(input.value).toBe(3.14);
  });

  test("RFC3339 token round-trips as Date", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "when",
      value: "2024-01-02T03:04:05Z",
      ttlSeconds: 60,
      valueType: "auto",
    };
    await dispatch({ client: asClient(fake), command: cmd });
    const call = fake.calls.find((c) => c.method === "putVertex");
    const input = call!.args[0] as { value: unknown };
    expect(input.value).toBeInstanceOf(Date);
    expect((input.value as Date).toISOString()).toBe(
      "2024-01-02T03:04:05.000Z",
    );
  });
});

describe("dispatch put / add edge carry expiration (#429)", () => {
  test("put edge carries expiration", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putEdge", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "edge",
      tail: "a",
      head: "b",
      weight: 1.5,
      ttlSeconds: 30,
    };
    const before = Date.now();
    await dispatch({ client: asClient(fake), command: cmd });
    const after = Date.now();
    const call = fake.calls.find((c) => c.method === "putEdge");
    const input = call!.args[0] as {
      tail: string;
      head: string;
      weight: number;
      expiration?: Date;
    };
    expect(input.tail).toBe("a");
    expect(input.head).toBe("b");
    expect(input.weight).toBe(1.5);
    expect(input.expiration).toBeInstanceOf(Date);
    const ms = input.expiration!.getTime();
    expect(ms).toBeGreaterThanOrEqual(before + 30_000);
    expect(ms).toBeLessThanOrEqual(after + 30_000);
  });

  test("add edge carries expiration", async () => {
    const fake = new FakeLanternClient();
    fake.stub("addEdge", () => undefined);
    const cmd: Command = {
      verb: "add",
      objective: "edge",
      tail: "x",
      head: "y",
      weight: 0.25,
      ttlSeconds: 45,
    };
    await dispatch({ client: asClient(fake), command: cmd });
    const call = fake.calls.find((c) => c.method === "addEdge");
    const input = call!.args[0] as { expiration?: Date };
    expect(input.expiration).toBeInstanceOf(Date);
  });
});

describe("dispatch write echo surfaces the applied TTL/expiry (#653)", () => {
  test("put vertex with a TTL echoes ttlSeconds + absolute expiresAt", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "a",
      value: "a",
      ttlSeconds: 1,
      valueType: "auto",
    };
    const before = Date.now();
    const out = (await dispatch({ client: asClient(fake), command: cmd })) as {
      key: string;
      ttlSeconds: number | null;
      expiresAt: string | null;
    };
    const after = Date.now();
    expect(out.key).toBe("a");
    expect(out.ttlSeconds).toBe(1);
    expect(out.expiresAt).not.toBeNull();
    const ms = new Date(out.expiresAt!).getTime();
    expect(ms).toBeGreaterThanOrEqual(before + 1_000);
    expect(ms).toBeLessThanOrEqual(after + 1_000);
  });

  test("put vertex without a TTL echoes nulls (permanent, no decay)", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putVertex", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "vertex",
      key: "permkey",
      value: "permval",
      ttlSeconds: null,
      valueType: "auto",
    };
    const out = await dispatch({ client: asClient(fake), command: cmd });
    expect(out).toEqual({ key: "permkey", ttlSeconds: null, expiresAt: null });
  });

  test("put edge echoes its identity + ttlSeconds + expiresAt", async () => {
    const fake = new FakeLanternClient();
    fake.stub("putEdge", () => undefined);
    const cmd: Command = {
      verb: "put",
      objective: "edge",
      tail: "a",
      head: "b",
      weight: 1.5,
      ttlSeconds: null,
    };
    const out = await dispatch({ client: asClient(fake), command: cmd });
    expect(out).toEqual({
      tail: "a",
      head: "b",
      weight: 1.5,
      ttlSeconds: null,
      expiresAt: null,
    });
  });

  test("add edge echoes its identity + ttlSeconds + expiresAt", async () => {
    const fake = new FakeLanternClient();
    fake.stub("addEdge", () => undefined);
    const cmd: Command = {
      verb: "add",
      objective: "edge",
      tail: "x",
      head: "y",
      weight: 0.25,
      ttlSeconds: 30,
    };
    const before = Date.now();
    const out = (await dispatch({ client: asClient(fake), command: cmd })) as {
      tail: string;
      head: string;
      weight: number;
      ttlSeconds: number | null;
      expiresAt: string | null;
    };
    const after = Date.now();
    expect(out.tail).toBe("x");
    expect(out.head).toBe("y");
    expect(out.weight).toBe(0.25);
    expect(out.ttlSeconds).toBe(30);
    const ms = new Date(out.expiresAt!).getTime();
    expect(ms).toBeGreaterThanOrEqual(before + 30_000);
    expect(ms).toBeLessThanOrEqual(after + 30_000);
  });
});

describe("dispatch add decaying-edge (#953)", () => {
  test("calls addDecayingEdge with the parsed DecayOptions", async () => {
    const fake = new FakeLanternClient();
    fake.stub("addDecayingEdge", () => 31);
    const cmd: Command = {
      verb: "add",
      objective: "decaying-edge",
      tail: "x",
      head: "y",
      initialWeight: 16,
      ratio: 0.5,
      steps: 5,
      intervalSeconds: 1,
    };
    await dispatch({ client: asClient(fake), command: cmd });
    const call = fake.calls.find((c) => c.method === "addDecayingEdge");
    expect(call).toBeDefined();
    expect(call!.args[0]).toBe("x");
    expect(call!.args[1]).toBe("y");
    expect(call!.args[2]).toEqual({
      initialWeight: 16,
      ratio: 0.5,
      steps: 5,
      intervalSeconds: 1,
    });
  });

  test("echoes the decay params, returned total, and full-horizon expiry", async () => {
    const fake = new FakeLanternClient();
    fake.stub("addDecayingEdge", () => 31);
    const cmd: Command = {
      verb: "add",
      objective: "decaying-edge",
      tail: "x",
      head: "y",
      initialWeight: 16,
      ratio: 0.5,
      steps: 5,
      intervalSeconds: 1,
    };
    const before = Date.now();
    const out = (await dispatch({ client: asClient(fake), command: cmd })) as {
      tail: string;
      head: string;
      initialWeight: number;
      ratio: number;
      steps: number;
      total: number;
      ttlSeconds: number | null;
      expiresAt: string | null;
    };
    const after = Date.now();
    expect(out.tail).toBe("x");
    expect(out.head).toBe("y");
    expect(out.initialWeight).toBe(16);
    expect(out.ratio).toBe(0.5);
    expect(out.steps).toBe(5);
    expect(out.total).toBe(31);
    // Horizon = steps × intervalSeconds = 5 × 1 = 5s.
    expect(out.ttlSeconds).toBe(5);
    const ms = new Date(out.expiresAt!).getTime();
    expect(ms).toBeGreaterThanOrEqual(before + 5_000);
    expect(ms).toBeLessThanOrEqual(after + 5_000);
  });
});

describe("writeEcho (#653)", () => {
  test("a positive TTL surfaces ttlSeconds + the absolute expiry it was given", () => {
    expect(writeEcho({ key: "a" }, 1, "2026-06-16T12:34:57.000Z")).toEqual({
      key: "a",
      ttlSeconds: 1,
      expiresAt: "2026-06-16T12:34:57.000Z",
    });
  });

  test("an omitted TTL (null) maps undefined expiration → null (permanent)", () => {
    expect(writeEcho({ key: "permkey" }, null, undefined)).toEqual({
      key: "permkey",
      ttlSeconds: null,
      expiresAt: null,
    });
  });

  test("carries arbitrary identity fields verbatim (edge tail/head/weight)", () => {
    expect(
      writeEcho({ tail: "a", head: "b", weight: 1.5 }, null, undefined),
    ).toEqual({
      tail: "a",
      head: "b",
      weight: 1.5,
      ttlSeconds: null,
      expiresAt: null,
    });
  });
});

describe("dispatch get → LanternApiError on NotFound (#430)", () => {
  test("get vertex throws LanternApiError(not_found) when SDK 404s", async () => {
    const fake = new FakeLanternClient();
    fake.stub("getVertex", () => {
      throw new NotFoundError("vertex not found");
    });
    const cmd: Command = { verb: "get", objective: "vertex", key: "ghost" };
    let caught: unknown = null;
    try {
      await dispatch({ client: asClient(fake), command: cmd });
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(LanternApiError);
    expect((caught as LanternApiError).code).toBe("not_found");
    expect((caught as LanternApiError).rpc).toBe("GetVertex");
    expect((caught as LanternApiError).grpcMessage).toContain("ghost");
  });

  test("get edge throws LanternApiError(not_found) when SDK 404s", async () => {
    const fake = new FakeLanternClient();
    fake.stub("getEdge", () => {
      throw new NotFoundError("edge not found");
    });
    const cmd: Command = {
      verb: "get",
      objective: "edge",
      tail: "a",
      head: "b",
    };
    let caught: unknown = null;
    try {
      await dispatch({ client: asClient(fake), command: cmd });
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(LanternApiError);
    expect((caught as LanternApiError).code).toBe("not_found");
    expect((caught as LanternApiError).rpc).toBe("GetEdge");
  });

  test("get vertex passes a non-404 error through unchanged", async () => {
    const fake = new FakeLanternClient();
    fake.stub("getVertex", () => {
      throw new InvalidArgumentError("bad");
    });
    const cmd: Command = { verb: "get", objective: "vertex", key: "k" };
    let caught: unknown = null;
    try {
      await dispatch({ client: asClient(fake), command: cmd });
    } catch (err) {
      caught = err;
    }
    expect(caught).toBeInstanceOf(LanternApiError);
    expect((caught as LanternApiError).code).toBe("invalid_argument");
  });
});

describe("dispatch scan vertices (#432 drops extra count RPC)", () => {
  test("scan vertices calls only scanVertices — no countVerticesByPrefix", async () => {
    const fake = new FakeLanternClient();
    fake.stub("scanVertices", () => ({
      vertices: [{ key: "a", value: "v" }],
      nextCursor: new Uint8Array(),
    }));
    const cmd: Command = {
      verb: "scan",
      objective: "vertices",
      prefix: "p",
      limit: 10,
      all: false,
    };
    const result = await dispatch({ client: asClient(fake), command: cmd });
    const methods = fake.calls.map((c) => c.method);
    expect(methods).toEqual(["scanVertices"]);
    // Result is the scan page (vertices + nextCursor) — no synthetic
    // `count` field added.
    expect((result as { count?: unknown }).count).toBeUndefined();
    expect((result as { vertices: unknown[] }).vertices.length).toBe(1);
  });
});

describe("dispatch keys (#674)", () => {
  test("keys calls scanVertexKeys with the prefix + limit and returns keys", async () => {
    const fake = new FakeLanternClient();
    fake.stub("scanVertexKeys", () => ({
      keys: ["user:1", "user:2"],
      nextCursor: new Uint8Array(),
    }));
    const cmd: Command = { verb: "keys", prefix: "user:", limit: 50 };
    const result = await dispatch({ client: asClient(fake), command: cmd });
    expect(fake.calls.map((c) => c.method)).toEqual(["scanVertexKeys"]);
    const [prefix, opts] = fake.calls[0].args as [string, { limit?: number }];
    expect(prefix).toBe("user:");
    expect(opts.limit).toBe(50);
    expect((result as { keys: string[] }).keys).toEqual(["user:1", "user:2"]);
  });
});

describe("dispatch family verbs map to the per-family oneofs (#975)", () => {
  // Since #975 the traversal family is the verb itself (bfs / pagerank /
  // community). These tests drive the full parser → dispatcher → adapter
  // seam and assert the right oneof reaches the SDK — and that the
  // reduction / objective / α / ε knobs ride along on the family that owns
  // them.
  const emptyGraph = () => ({ vertices: new Map(), edges: new Map() });

  test("community builds the community oneof, not bfs/ppr", async () => {
    const fake = new FakeLanternClient();
    fake.stub("illuminate", emptyGraph);
    const parsed = parse("community a1 5");
    if (!parsed.ok) {
      throw new Error(`fixture did not parse: ${parsed.usage}`);
    }
    await dispatch({ client: asClient(fake), command: parsed.command });

    expect(fake.calls.map((c) => c.method)).toEqual(["illuminate"]);
    const [seed, opts] = fake.calls[0].args as [
      string,
      { community?: unknown; bfs?: unknown; ppr?: unknown },
    ];
    expect(seed).toBe("a1");
    // The positional (5) becomes the max_size UPPER BOUND; the α/ε knobs
    // default to 0 = "server default". Mirrors the Go CLI `case "community"`.
    // reduction defaults to UNSPECIFIED (no tree) and objective to the axis
    // default (max) — harmless without a reduction (#961).
    expect(opts.community).toEqual({
      maxSize: 5,
      restartProb: 0,
      epsilon: 0,
      reduction: SdkReduction.UNSPECIFIED,
      objective: SdkObjective.MAXIMIZE,
    });
    // The #942 regression guard: the seam must NOT degrade to a BFS/PPR walk.
    expect(opts.bfs).toBeUndefined();
    expect(opts.ppr).toBeUndefined();
  });

  test("community passes float32-normalized restart_prob / epsilon", async () => {
    const fake = new FakeLanternClient();
    fake.stub("illuminate", emptyGraph);
    const parsed = parse("community a1 5 restart_prob=0.25 epsilon=0.001");
    if (!parsed.ok) {
      throw new Error(`fixture did not parse: ${parsed.usage}`);
    }
    await dispatch({ client: asClient(fake), command: parsed.command });

    const [, opts] = fake.calls[0].args as [
      string,
      { community?: unknown; bfs?: unknown; ppr?: unknown },
    ];
    expect(opts.community).toEqual({
      maxSize: 5,
      restartProb: Math.fround(0.25),
      epsilon: Math.fround(0.001),
      reduction: SdkReduction.UNSPECIFIED,
      objective: SdkObjective.MAXIMIZE,
    });
    expect(opts.bfs).toBeUndefined();
    expect(opts.ppr).toBeUndefined();
  });

  // #961: the reduction axis rides on the community oneof and its direction
  // is steered by objective — an MST rooted at the seed, minimised.
  test("community + reduction=mst objective=min carries the tree knobs", async () => {
    const fake = new FakeLanternClient();
    fake.stub("illuminate", emptyGraph);
    const parsed = parse("community a1 5 reduction=mst objective=min");
    if (!parsed.ok) {
      throw new Error(`fixture did not parse: ${parsed.usage}`);
    }
    await dispatch({ client: asClient(fake), command: parsed.command });

    const [, opts] = fake.calls[0].args as [
      string,
      { community?: unknown; bfs?: unknown; ppr?: unknown },
    ];
    expect(opts.community).toEqual({
      maxSize: 5,
      restartProb: 0,
      epsilon: 0,
      reduction: SdkReduction.MINIMUM_SPANNING_TREE,
      objective: SdkObjective.MINIMIZE,
    });
  });

  test("pagerank builds the ppr oneof with top_n + α/ε", async () => {
    const fake = new FakeLanternClient();
    fake.stub("illuminate", emptyGraph);
    const parsed = parse("pagerank a1 15 restart_prob=0.25 epsilon=0.001");
    if (!parsed.ok) {
      throw new Error(`fixture did not parse: ${parsed.usage}`);
    }
    await dispatch({ client: asClient(fake), command: parsed.command });

    const [seed, opts] = fake.calls[0].args as [
      string,
      { community?: unknown; bfs?: unknown; ppr?: unknown },
    ];
    expect(seed).toBe("a1");
    // The positional (15) becomes top_n; pagerank carries no reduction /
    // objective (its relevance star is already a tree).
    expect(opts.ppr).toEqual({
      topN: 15,
      restartProb: Math.fround(0.25),
      epsilon: Math.fround(0.001),
    });
    expect(opts.bfs).toBeUndefined();
    expect(opts.community).toBeUndefined();
  });

  // #961: the reduction axis also feeds the bfs family.
  test("bfs + reduction=spt carries the tree knob", async () => {
    const fake = new FakeLanternClient();
    fake.stub("illuminate", emptyGraph);
    const parsed = parse("bfs a1 2 5 reduction=spt objective=min");
    if (!parsed.ok) {
      throw new Error(`fixture did not parse: ${parsed.usage}`);
    }
    await dispatch({ client: asClient(fake), command: parsed.command });

    const [, opts] = fake.calls[0].args as [
      string,
      {
        community?: unknown;
        ppr?: unknown;
        bfs?: {
          step: number;
          fanOut: number;
          reduction: number;
          objective: number;
        };
      },
    ];
    expect(opts.bfs).toEqual({
      step: 2,
      fanOut: 5,
      objective: SdkObjective.MINIMIZE,
      reduction: SdkReduction.SHORTEST_PATH_TREE,
    });
    expect(opts.community).toBeUndefined();
    expect(opts.ppr).toBeUndefined();
  });
});

describe("dispatch search (#1068 shared request semantics)", () => {
  test("maps every grammar option onto one Node SDK page request", async () => {
    const fake = new FakeLanternClient();
    fake.stub("searchVerticesPage", () => ({
      hits: [{ key: "利用者/one", score: 2.5, projectionStatus: "key-score" }],
      nextCursor: new Uint8Array([4, 5]),
      effectiveLimit: 17,
      truncated: true,
      continuationLimited: false,
    }));
    const parsed = parse(
      'search "静かな rolling update" limit=17 prefix=利用者/ mode=min-should min_should=2 fuzziness=1 prefix_terms=true cursor=AQID projection=key-score format=json',
    );
    if (!parsed.ok) throw new Error(parsed.usage);

    const result = await dispatch({
      client: asClient(fake),
      command: parsed.command,
    });

    expect(fake.calls).toHaveLength(1);
    const [query, opts] = fake.calls[0].args as [
      string,
      {
        limit: number;
        prefix: string;
        matchMode: string;
        minShouldMatch: number;
        fuzziness: number;
        prefixTerms: boolean;
        cursor: Uint8Array;
        projection: string;
      },
    ];
    expect(query).toBe("静かな rolling update");
    expect(opts).toMatchObject({
      limit: 17,
      prefix: "利用者/",
      matchMode: "min-should",
      minShouldMatch: 2,
      fuzziness: 1,
      prefixTerms: true,
      projection: "key-score",
    });
    expect(Array.from(opts.cursor)).toEqual([1, 2, 3]);
    expect(result).toMatchObject({
      hits: [{ key: "利用者/one", score: 2.5 }],
      nextCursor: "BAU",
      effectiveLimit: 17,
      truncated: true,
    });
  });

  test("all=true follows the bounded cursor chain", async () => {
    const fake = new FakeLanternClient();
    fake.stub("searchVerticesIter", async function* () {
      yield { key: "a", score: 2 };
      yield { key: "b", score: 1 };
    });
    const parsed = parse("search alpha limit=1 all=true");
    if (!parsed.ok) throw new Error(parsed.usage);
    const result = (await dispatch({
      client: asClient(fake),
      command: parsed.command,
    })) as { hits: Array<{ key: string }> };
    expect(result.hits.map((hit) => hit.key)).toEqual(["a", "b"]);
    expect(fake.calls).toHaveLength(1);
    expect(fake.calls[0].method).toBe("searchVerticesIter");
    const opts = fake.calls[0].args[1] as { cursor: Uint8Array };
    expect(Array.from(opts.cursor)).toEqual([]);
  });

  test("rejects a malformed cursor before the SDK call", async () => {
    const fake = new FakeLanternClient();
    const parsed = parse("search alpha cursor=not+base64");
    if (!parsed.ok) throw new Error(parsed.usage);
    await expect(
      dispatch({ client: asClient(fake), command: parsed.command }),
    ).rejects.toThrow("unpadded URL-safe base64");
    expect(fake.calls).toHaveLength(0);
  });
});
