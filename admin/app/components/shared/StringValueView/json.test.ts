import { describe, expect, it } from "bun:test";

import { formatJson, tokenizeJson, type JsonToken } from "./json";

describe("formatJson", () => {
  it("pretty-prints a JSON object with two-space indent", () => {
    const out = formatJson('{"b":2,"a":"x"}');
    expect(out).not.toBeNull();
    expect(out?.formatted).toBe('{\n  "b": 2,\n  "a": "x"\n}');
  });

  it("pretty-prints a JSON array", () => {
    expect(formatJson('["a",1]')?.formatted).toBe('[\n  "a",\n  1\n]');
  });

  it("tolerates surrounding whitespace", () => {
    expect(formatJson('  {"a":1}  ')?.formatted).toBe('{\n  "a": 1\n}');
  });

  it("returns null for plain prose", () => {
    expect(formatJson("calm and concise")).toBeNull();
  });

  it("returns null for bare scalars even if valid JSON", () => {
    expect(formatJson("42")).toBeNull();
    expect(formatJson("true")).toBeNull();
    expect(formatJson('"quoted"')).toBeNull();
  });

  it("returns null for the empty string", () => {
    expect(formatJson("")).toBeNull();
  });

  it("returns null for malformed JSON that looks like an object", () => {
    expect(formatJson("{not valid")).toBeNull();
  });
});

function kinds(tokens: JsonToken[]): string[] {
  return tokens.filter((t) => t.kind !== "whitespace").map((t) => t.kind);
}

function roundTrip(tokens: JsonToken[]): string {
  return tokens.map((t) => t.text).join("");
}

describe("tokenizeJson", () => {
  it("reproduces the input exactly when concatenated", () => {
    const src = '{\n  "name": "Alice",\n  "score": 9,\n  "active": true\n}';
    expect(roundTrip(tokenizeJson(src))).toBe(src);
  });

  it("classifies object keys distinctly from string values", () => {
    const tokens = tokenizeJson('{"name":"Alice"}');
    expect(kinds(tokens)).toEqual([
      "punctuation",
      "key",
      "punctuation",
      "string",
      "punctuation",
    ]);
  });

  it("classifies numbers, booleans, and null", () => {
    expect(kinds(tokenizeJson('{"a":9}'))).toEqual([
      "punctuation",
      "key",
      "punctuation",
      "number",
      "punctuation",
    ]);
    expect(kinds(tokenizeJson('{"a":true}')).at(-2)).toBe("boolean");
    expect(kinds(tokenizeJson('{"a":null}')).at(-2)).toBe("null");
  });

  it("handles negative and exponent numbers", () => {
    const tokens = tokenizeJson("[-1.5e10]");
    const num = tokens.find((t) => t.kind === "number");
    expect(num?.text).toBe("-1.5e10");
  });

  it("does not end a string early on an escaped quote", () => {
    const tokens = tokenizeJson('{"a":"he said \\"hi\\""}');
    const value = tokens.filter((t) => t.kind === "string");
    expect(value).toHaveLength(1);
    expect(value[0]?.text).toBe('"he said \\"hi\\""');
  });

  it("treats a key literally named like a keyword as a key", () => {
    const tokens = tokenizeJson('{"null":1}');
    expect(tokens.find((t) => t.text === '"null"')?.kind).toBe("key");
  });
});
