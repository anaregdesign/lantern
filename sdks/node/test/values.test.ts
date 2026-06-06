import { describe, expect, test } from "bun:test";

import {
  Algorithm,
  Duration,
  Float32,
  Int32,
  Objective,
  OverflowError,
  Uint32,
  Uint64,
  Weighting,
  fromEdgeJson,
  fromVertexJson,
  toVertexJson,
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

describe("Duration.toString", () => {
  test("renders integer seconds", () => {
    expect(new Duration(5n, 0).toString()).toBe("5s");
    expect(new Duration(0n, 0).toString()).toBe("0s");
  });

  test("renders fractional seconds with trimmed zeros", () => {
    expect(Duration.fromMillis(1500).toString()).toBe("1.5s");
    expect(Duration.fromMillis(250).toString()).toBe("0.25s");
  });

  test("renders negatives", () => {
    expect(Duration.fromMillis(-1500).toString()).toBe("-1.5s");
  });
});

describe("toVertexJson dispatch", () => {
  test("null → nil tombstone", () => {
    const j = toVertexJson({ key: "k", value: null });
    expect(j.nil).toBe(true);
  });

  test("boolean dispatched before number", () => {
    const j = toVertexJson({ key: "k", value: true });
    expect(j.bool).toBe(true);
    expect(j.int64).toBeUndefined();
  });

  test("integer Number → int64 (as string)", () => {
    const j = toVertexJson({ key: "k", value: 42 });
    expect(j.int64).toBe("42");
    expect(j.float64).toBeUndefined();
  });

  test("fractional Number → float64", () => {
    const j = toVertexJson({ key: "k", value: 3.14 });
    expect(j.float64).toBe(3.14);
  });

  test("negative bigint → int64", () => {
    const j = toVertexJson({ key: "k", value: -10n });
    expect(j.int64).toBe("-10");
  });

  test("bigint ≥ 2^63 → uint64", () => {
    const big = 1n << 63n;
    const j = toVertexJson({ key: "k", value: big });
    expect(j.uint64).toBe(big.toString());
  });

  test("bigint overflowing uint64 throws", () => {
    expect(() => toVertexJson({ key: "k", value: 1n << 64n })).toThrow(OverflowError);
  });

  test("bigint underflowing int64 throws", () => {
    expect(() => toVertexJson({ key: "k", value: -(1n << 63n) - 1n })).toThrow(OverflowError);
  });

  test("string, bytes, Date", () => {
    expect(toVertexJson({ key: "k", value: "hello" }).string).toBe("hello");
    // bytes encode to base64 on the JSON wire.
    expect(toVertexJson({ key: "k", value: new Uint8Array([1, 2, 3]) }).bytes).toBe(
      Buffer.from([1, 2, 3]).toString("base64"),
    );
    const d = new Date(1700000000000);
    expect(toVertexJson({ key: "k", value: d }).timestamp).toBe(d.toISOString());
  });

  test("Duration carrier", () => {
    const j = toVertexJson({ key: "k", value: Duration.fromMillis(1500) });
    expect(j.duration).toBe("1.5s");
  });

  test("markers route to pinned variants", () => {
    expect(toVertexJson({ key: "k", value: new Int32(7) }).int32).toBe(7);
    expect(toVertexJson({ key: "k", value: new Uint32(7) }).uint32).toBe(7);
    expect(toVertexJson({ key: "k", value: new Uint64(7n) }).uint64).toBe("7");
    expect(toVertexJson({ key: "k", value: new Float32(1.5) }).float32).toBe(1.5);
  });

  test("ttl and expiration are mutually exclusive", () => {
    expect(() =>
      toVertexJson({ key: "k", value: 1, ttlSeconds: 1, expiration: new Date() }),
    ).toThrow(TypeError);
  });

  test("ttl materialises to absolute ISO string", () => {
    const j = toVertexJson({ key: "k", value: 1, ttlSeconds: 60 });
    expect(typeof j.expiration).toBe("string");
    const skew = Math.abs(Date.parse(String(j.expiration)) - (Date.now() + 60_000));
    expect(skew).toBeLessThan(1_000);
  });
});

describe("fromVertexJson", () => {
  test("round-trips primitives", () => {
    const cases: Array<[string, unknown]> = [
      ["string", "hello"],
      ["bool", true],
      ["int64", 42],
      ["float64", 3.14],
    ];
    for (const [_label, value] of cases) {
      const j = toVertexJson({
        key: "k",
        value: value as Parameters<typeof toVertexJson>[0]["value"],
      });
      const sv = fromVertexJson(j);
      expect(sv.value).toEqual(value);
    }
  });

  test("nil round-trip yields null", () => {
    const sv = fromVertexJson(toVertexJson({ key: "k", value: null }));
    expect(sv.value).toBeNull();
    expect(sv.kind).toBe("nil");
  });

  test("zero-epoch expiration normalises to null", () => {
    const sv = fromVertexJson({ key: "k", expiration: new Date(0).toISOString(), string: "x" });
    expect(sv.expiration).toBeNull();
  });

  test("non-zero expiration preserved", () => {
    const exp = new Date(Date.now() + 60_000);
    const sv = fromVertexJson({ key: "k", expiration: exp.toISOString(), string: "x" });
    expect(sv.expiration?.getTime()).toBe(exp.getTime());
  });

  test("int64 out of safe range promotes to bigint", () => {
    const big = (BigInt(Number.MAX_SAFE_INTEGER) + 10n).toString();
    const sv = fromVertexJson({ key: "k", int64: big });
    expect(typeof sv.value).toBe("bigint");
    expect(sv.value).toBe(BigInt(big));
  });
});

describe("fromEdgeJson", () => {
  test("zero-epoch expiration normalises to null", () => {
    const se = fromEdgeJson({
      tail: "t",
      head: "h",
      weight: 1,
      expiration: new Date(0).toISOString(),
    });
    expect(se.expiration).toBeNull();
    expect(se.tail).toBe("t");
    expect(se.head).toBe("h");
    expect(se.weight).toBe(1);
  });
});

describe("Illuminate axis enums (#410)", () => {
  test("Algorithm matches proto codes", () => {
    expect(Algorithm.UNSPECIFIED).toBe(0);
    expect(Algorithm.MINIMUM_SPANNING_TREE).toBe(1);
    expect(Algorithm.SHORTEST_PATH_TREE).toBe(2);
  });
  test("Objective matches proto codes", () => {
    expect(Objective.UNSPECIFIED).toBe(0);
    expect(Objective.MINIMIZE).toBe(1);
    expect(Objective.MAXIMIZE).toBe(2);
  });
  test("Weighting matches proto codes", () => {
    expect(Weighting.UNSPECIFIED).toBe(0);
    expect(Weighting.RAW).toBe(1);
    expect(Weighting.TFIDF).toBe(2);
  });
});
