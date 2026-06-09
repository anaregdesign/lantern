import { describe, expect, test } from "bun:test";
import { tokenizeJson, type JsonToken } from "./json-syntax";

/** Re-join tokens; the renderer + e2e suite rely on this being lossless. */
function rejoin(tokens: JsonToken[]): string {
  return tokens.map((t) => t.text).join("");
}

/** Collect the text of every token of a given kind, in order. */
function textsOf(tokens: JsonToken[], kind: JsonToken["kind"]): string[] {
  return tokens.filter((t) => t.kind === kind).map((t) => t.text);
}

describe("tokenizeJson concatenation invariant", () => {
  test("round-trips a pretty-printed object byte-for-byte", () => {
    const src = JSON.stringify(
      { key: "cli:alpha", string: "first", weight: -2.5, live: true },
      null,
      2,
    );
    expect(rejoin(tokenizeJson(src))).toBe(src);
  });

  test("round-trips nested arrays and objects", () => {
    const src = JSON.stringify(
      { nodes: [{ id: 1 }, { id: 2 }], edges: [], meta: null },
      null,
      2,
    );
    expect(rejoin(tokenizeJson(src))).toBe(src);
  });

  test("round-trips an empty string and whitespace-only input", () => {
    expect(rejoin(tokenizeJson(""))).toBe("");
    expect(rejoin(tokenizeJson("   \n\t "))).toBe("   \n\t ");
  });

  test("round-trips non-JSON input without dropping characters", () => {
    const src = "not json @#$ <key>";
    expect(rejoin(tokenizeJson(src))).toBe(src);
  });
});

describe("tokenizeJson classification", () => {
  test("distinguishes keys from string values", () => {
    const src = '{\n  "name": "lantern"\n}';
    const tokens = tokenizeJson(src);
    expect(textsOf(tokens, "key")).toEqual(['"name"']);
    expect(textsOf(tokens, "string")).toEqual(['"lantern"']);
  });

  test("a string used as an array element is not a key", () => {
    const tokens = tokenizeJson('["a", "b"]');
    expect(textsOf(tokens, "key")).toEqual([]);
    expect(textsOf(tokens, "string")).toEqual(['"a"', '"b"']);
  });

  test("handles escaped quotes inside strings", () => {
    const src = '{ "msg": "say \\"hi\\"" }';
    const tokens = tokenizeJson(src);
    expect(rejoin(tokens)).toBe(src);
    expect(textsOf(tokens, "string")).toEqual(['"say \\"hi\\""']);
    expect(textsOf(tokens, "key")).toEqual(['"msg"']);
  });

  test("classifies numbers including negative, decimal, and exponent", () => {
    const tokens = tokenizeJson("[-2.5, 1e10, 0, 42]");
    expect(textsOf(tokens, "number")).toEqual(["-2.5", "1e10", "0", "42"]);
  });

  test("classifies booleans and null", () => {
    const tokens = tokenizeJson("[true, false, null]");
    expect(textsOf(tokens, "boolean")).toEqual(["true", "false"]);
    expect(textsOf(tokens, "null")).toEqual(["null"]);
  });

  test("emits structural punctuation tokens", () => {
    const tokens = tokenizeJson('{"a":[1]}');
    expect(textsOf(tokens, "punctuation")).toEqual(["{", ":", "[", "]", "}"]);
  });

  test("preserves indentation as whitespace tokens", () => {
    const src = '{\n  "a": 1\n}';
    const tokens = tokenizeJson(src);
    // Two-space indent before the key is a whitespace token.
    expect(textsOf(tokens, "whitespace")).toContain("\n  ");
  });
});
