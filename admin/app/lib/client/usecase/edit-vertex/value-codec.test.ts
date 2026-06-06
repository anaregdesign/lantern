import { describe, expect, test } from "bun:test";
import {
  buildPutVertexBody,
  inputsFromVertex,
  isoToLocalInput,
  kindOfVertex,
  parseBytesInput,
  parseGoDuration,
  INITIAL_TTL_INPUT,
  INITIAL_VERTEX_INPUTS,
  type TtlInput,
  type VertexInputs,
} from "./value-codec";

const FIXED_NOW = Date.parse("2026-01-02T03:04:05Z");

describe("kindOfVertex", () => {
  test("picks the populated variant", () => {
    expect(kindOfVertex({ key: "k", int32: 5 })).toBe("int32");
    expect(kindOfVertex({ key: "k", string: "x" })).toBe("string");
    expect(kindOfVertex({ key: "k", bool: false })).toBe("bool");
    expect(kindOfVertex({ key: "k", nil: true })).toBe("nil");
  });
  test("falls back to nil when nothing is set", () => {
    expect(kindOfVertex({ key: "k" })).toBe("nil");
  });
  test("prefers float64 over later variants when both somehow set", () => {
    expect(kindOfVertex({ key: "k", float64: 1, string: "x" })).toBe("float64");
  });
});

describe("inputsFromVertex", () => {
  test("seeds matching field for int32", () => {
    const inputs = inputsFromVertex({ key: "k", int32: 7 });
    expect(inputs.int32).toBe("7");
  });
  test("encodes bytes as hex with 0x prefix", () => {
    // 0x010203 in base64 is "AQID"
    const inputs = inputsFromVertex({ key: "k", bytes: "AQID" });
    expect(inputs.bytesInput).toBe("0x010203");
  });
});

describe("parseGoDuration", () => {
  test("accepts compound durations", () => {
    expect(parseGoDuration("1h30m").ms).toBe(90 * 60 * 1000);
    expect(parseGoDuration("750ms").ms).toBe(750);
    expect(parseGoDuration("2s500ms").ms).toBe(2500);
  });
  test("accepts fractional magnitudes", () => {
    expect(parseGoDuration("1.5s").ms).toBe(1500);
  });
  test("rejects missing unit", () => {
    expect(parseGoDuration("5").error).not.toBeNull();
  });
  test("rejects unknown units", () => {
    expect(parseGoDuration("5x").error).not.toBeNull();
  });
  test("accepts negative durations", () => {
    expect(parseGoDuration("-5m").ms).toBe(-5 * 60_000);
  });
});

describe("parseBytesInput", () => {
  test("hex with 0x prefix", () => {
    expect(parseBytesInput("0x010203", "hex")).toEqual({
      b64: "AQID",
      error: null,
    });
  });
  test("hex without prefix and whitespace", () => {
    expect(parseBytesInput("01 02 03", "hex").b64).toBe("AQID");
  });
  test("rejects odd-length hex", () => {
    expect(parseBytesInput("0x1", "hex").error).not.toBeNull();
  });
  test("rejects non-hex chars", () => {
    expect(parseBytesInput("0xZZ", "hex").error).not.toBeNull();
  });
  test("base64 passthrough", () => {
    expect(parseBytesInput("AQID", "base64").b64).toBe("AQID");
  });
});

function makeInputs(over: Partial<VertexInputs>): VertexInputs {
  return { ...INITIAL_VERTEX_INPUTS, ...over };
}

function ttl(over: Partial<TtlInput> = {}): TtlInput {
  return { ...INITIAL_TTL_INPUT, ...over };
}

describe("buildPutVertexBody", () => {
  test("float64 happy path with preset TTL", () => {
    const out = buildPutVertexBody(
      "float64",
      makeInputs({ float64: "3.14" }),
      ttl({ mode: "preset5m" }),
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.vertex?.float64).toBe(3.14);
    expect(out.body?.vertex?.expiration).toBe(
      new Date(FIXED_NOW + 5 * 60_000).toISOString(),
    );
  });

  test("int32 out-of-range fails", () => {
    const out = buildPutVertexBody(
      "int32",
      makeInputs({ int32: "99999999999" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.body).toBeNull();
    expect(out.error).toMatch(/int32/);
  });

  test("int64 emitted as string", () => {
    const out = buildPutVertexBody(
      "int64",
      makeInputs({ int64: "9223372036854775807" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.vertex?.int64).toBe("9223372036854775807");
  });

  test("uint64 rejects negatives", () => {
    const out = buildPutVertexBody(
      "uint64",
      makeInputs({ uint64: "-1" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.body).toBeNull();
  });

  test("bytes converts hex → base64", () => {
    const out = buildPutVertexBody(
      "bytes",
      makeInputs({ bytesEncoding: "hex", bytesInput: "0xdeadbeef" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.vertex?.bytes).toBe("3q2+7w==");
  });

  test("timestamp normalised to ISO", () => {
    const out = buildPutVertexBody(
      "timestamp",
      makeInputs({ timestamp: "2026-01-02T03:04" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.vertex?.timestamp).toMatch(/^2026-01-02T/);
  });

  test("duration parsed but stored as raw Go string", () => {
    const out = buildPutVertexBody(
      "duration",
      makeInputs({ duration: "1h30m" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.vertex?.duration).toBe("1h30m");
  });

  test("nil sets the flag true", () => {
    const out = buildPutVertexBody(
      "nil",
      makeInputs({}),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.body?.vertex?.nil).toBe(true);
  });

  test("custom TTL parses Go duration", () => {
    const out = buildPutVertexBody(
      "string",
      makeInputs({ string: "x" }),
      ttl({ mode: "custom", custom: "10m" }),
      FIXED_NOW,
    );
    expect(out.body?.vertex?.expiration).toBe(
      new Date(FIXED_NOW + 10 * 60_000).toISOString(),
    );
  });

  test("none TTL omits expiration", () => {
    const out = buildPutVertexBody(
      "string",
      makeInputs({ string: "x" }),
      ttl({ mode: "none" }),
      FIXED_NOW,
    );
    expect(out.body?.vertex?.expiration).toBeUndefined();
  });
});

describe("isoToLocalInput", () => {
  test("returns YYYY-MM-DDTHH:mm local form", () => {
    const out = isoToLocalInput("2026-01-02T03:04:05Z");
    expect(out).toMatch(/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/);
  });
  test("invalid passes through", () => {
    expect(isoToLocalInput("not-a-date")).toBe("not-a-date");
  });
});
