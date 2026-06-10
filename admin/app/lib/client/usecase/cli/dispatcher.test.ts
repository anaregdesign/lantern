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
import { InvalidArgumentError, NotFoundError } from "lantern-sdk/web";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { coerceValue, dispatch, ttlSecondsToExpiration } from "./dispatcher";
import type { Command } from "~/lib/cli/types";

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
