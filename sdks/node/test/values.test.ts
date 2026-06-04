import { describe, expect, test } from "bun:test";

import {
  Duration,
  Float32,
  Int32,
  Optimization,
  OverflowError,
  Uint32,
  Uint64,
  fromPbEdge,
  fromPbVertex,
  toPbVertex,
} from "../src/index.js";

describe("narrowing markers", () => {
  test("Int32 accepts and rejects values", () => {
    expect(new Int32(0).value).toBe(0);
    expect(new Int32(Int32.MIN).value).toBe(Int32.MIN);
    expect(new Int32(Int32.MAX).value).toBe(Int32.MAX);
    expect(() => new Int32(Int32.MAX + 1)).toThrow(OverflowError);
    expect(() => new Int32(Int32.MIN - 1)).toThrow(OverflowError);
    expect(() => new Int32(1.5)).toThrow(TypeError);
  });

  test("Uint32 enforces non-negative", () => {
    expect(new Uint32(Uint32.MAX).value).toBe(Uint32.MAX);
    expect(() => new Uint32(-1)).toThrow(OverflowError);
    expect(() => new Uint32(Uint32.MAX + 1)).toThrow(OverflowError);
  });

  test("Uint64 accepts number and bigint", () => {
    expect(new Uint64(123).value).toBe(123n);
    expect(new Uint64(1n << 63n).value).toBe(1n << 63n);
    expect(() => new Uint64(-1n)).toThrow(OverflowError);
    expect(() => new Uint64(1n << 64n)).toThrow(OverflowError);
  });

  test("Float32 wraps numbers", () => {
    expect(new Float32(1.5).value).toBe(1.5);
  });
});

describe("toPbVertex dispatch", () => {
  test("null → nil tombstone", () => {
    const pv = toPbVertex("k", null);
    expect(pv.nil).toBe(true);
  });

  test("boolean dispatched before number", () => {
    const pv = toPbVertex("k", true);
    expect(pv.bool).toBe(true);
    expect(pv.int64).toBeUndefined();
  });

  test("integer Number → int64", () => {
    const pv = toPbVertex("k", 42);
    expect(pv.int64?.toString()).toBe("42");
    expect(pv.float64).toBeUndefined();
  });

  test("fractional Number → float64", () => {
    const pv = toPbVertex("k", 3.14);
    expect(pv.float64).toBe(3.14);
  });

  test("negative bigint → int64", () => {
    const pv = toPbVertex("k", -10n);
    expect(pv.int64?.toString()).toBe("-10");
  });

  test("bigint ≥ 2^63 → uint64", () => {
    const pv = toPbVertex("k", 1n << 63n);
    expect(pv.uint64?.toString()).toBe((1n << 63n).toString());
  });

  test("bigint overflowing uint64 throws", () => {
    expect(() => toPbVertex("k", 1n << 64n)).toThrow(OverflowError);
  });

  test("bigint underflowing int64 throws", () => {
    expect(() => toPbVertex("k", -(1n << 63n) - 1n)).toThrow(OverflowError);
  });

  test("string, bytes, Date", () => {
    expect(toPbVertex("k", "hello").string).toBe("hello");
    expect(toPbVertex("k", new Uint8Array([1, 2, 3])).bytes).toEqual(Buffer.from([1, 2, 3]));
    const d = new Date(1700000000000);
    expect(toPbVertex("k", d).timestamp).toBe(d);
  });

  test("Duration carrier", () => {
    const pv = toPbVertex("k", Duration.fromMillis(1500));
    expect(pv.duration?.seconds.toString()).toBe("1");
    expect(pv.duration?.nanos).toBe(500_000_000);
  });

  test("markers route to pinned variants", () => {
    expect(toPbVertex("k", new Int32(7)).int32).toBe(7);
    expect(toPbVertex("k", new Uint32(7)).uint32).toBe(7);
    expect(toPbVertex("k", new Uint64(7n)).uint64?.toString()).toBe("7");
    expect(toPbVertex("k", new Float32(1.5)).float32).toBe(1.5);
  });

  test("ttl and expiration are mutually exclusive", () => {
    expect(() => toPbVertex("k", 1, 1, new Date())).toThrow(TypeError);
  });

  test("ttl materialises to absolute Date", () => {
    const pv = toPbVertex("k", 1, 60);
    expect(pv.expiration).toBeInstanceOf(Date);
    const skew = Math.abs((pv.expiration as Date).getTime() - (Date.now() + 60_000));
    expect(skew).toBeLessThan(1_000);
  });
});

describe("fromPbVertex", () => {
  test("round-trips primitives", () => {
    const cases: Array<[string, unknown]> = [
      ["string", "hello"],
      ["bool", true],
      ["int64", 42],
      ["float64", 3.14],
    ];
    for (const [_label, value] of cases) {
      const sv = fromPbVertex(toPbVertex("k", value));
      expect(sv.value).toEqual(value);
    }
  });

  test("nil round-trip yields null", () => {
    const sv = fromPbVertex(toPbVertex("k", null));
    expect(sv.value).toBeNull();
    expect(sv.kind).toBe("nil");
  });

  test("zero-epoch expiration normalises to null", () => {
    const sv = fromPbVertex({ key: "k", expiration: new Date(0), string: "x" });
    expect(sv.expiration).toBeNull();
  });

  test("non-zero expiration preserved", () => {
    const exp = new Date(Date.now() + 60_000);
    const sv = fromPbVertex({ key: "k", expiration: exp, string: "x" });
    expect(sv.expiration?.getTime()).toBe(exp.getTime());
  });
});

describe("fromPbEdge", () => {
  test("zero-epoch expiration normalises to null", () => {
    const se = fromPbEdge({ tail: "t", head: "h", weight: 1, expiration: new Date(0) });
    expect(se.expiration).toBeNull();
    expect(se.tail).toBe("t");
    expect(se.head).toBe("h");
    expect(se.weight).toBe(1);
  });
});

describe("Optimization enum", () => {
  test("matches proto codes", () => {
    expect(Optimization.UNSPECIFIED).toBe(0);
    expect(Optimization.MINIMUM_SPANNING_TREE).toBe(1);
    expect(Optimization.MAXIMUM_SPANNING_TREE).toBe(2);
    expect(Optimization.SHORTEST_PATH_TREE).toBe(3);
    expect(Optimization.SHORTEST_PATH_TREE_INVERSE).toBe(4);
  });
});
