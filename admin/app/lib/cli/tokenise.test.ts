/**
 * Focused unit coverage for `tokenise` (#437 + #438).
 *
 * The shared fixture under `admin/test/cli-grammar/verbs.json` covers
 * end-to-end parser agreement with the Go REPL. This file pins the
 * tokeniser's own behaviour — case preservation, the quote-as-token-
 * boundary rule, the C-style escape table, and the unterminated-
 * literal error — without coupling to the verb-dispatch surface.
 */

import { describe, expect, test } from "bun:test";

import { tokenise } from "~/lib/cli/tokenise";

function ok(input: string): string[] {
  const r = tokenise(input);
  if (!r.ok) {
    throw new Error(`tokenise(${JSON.stringify(input)}) failed: ${r.error}`);
  }
  return r.tokens;
}

function err(input: string): string {
  const r = tokenise(input);
  if (r.ok) {
    throw new Error(
      `tokenise(${JSON.stringify(input)}) unexpectedly succeeded with ${JSON.stringify(
        r.tokens,
      )}`,
    );
  }
  return r.error;
}

describe("tokenise — happy path (#437 case preservation)", () => {
  test("empty input yields no tokens", () => {
    expect(ok("")).toEqual([]);
  });

  test("whitespace-only input yields no tokens", () => {
    expect(ok("   \t\n  ")).toEqual([]);
  });

  test("preserves case on every argument", () => {
    expect(ok("Get VERTEX CamelKey")).toEqual(["Get", "VERTEX", "CamelKey"]);
  });

  test("does not lowercase family-verb keyword args", () => {
    expect(ok("bfs Alice 2 5 Reduction=SPT")).toEqual([
      "bfs",
      "Alice",
      "2",
      "5",
      "Reduction=SPT",
    ]);
  });

  test("collapses any whitespace run between tokens", () => {
    expect(ok("a   b\tc\nd")).toEqual(["a", "b", "c", "d"]);
  });

  test("trims leading and trailing whitespace", () => {
    expect(ok("  put vertex k v  ")).toEqual(["put", "vertex", "k", "v"]);
  });
});

describe("tokenise — bareword quoting policy (#438 boundary rule)", () => {
  test("quotes embedded mid-bareword stay verbatim", () => {
    expect(ok(`key=foo"bar`)).toEqual([`key=foo"bar`]);
  });

  test("single-quote mid-bareword stays verbatim", () => {
    expect(ok("key=foo'bar")).toEqual(["key=foo'bar"]);
  });

  test("bareword may contain '=' for kwarg syntax", () => {
    expect(ok("a=b c=d")).toEqual(["a=b", "c=d"]);
  });
});

describe("tokenise — double-quoted strings (#438)", () => {
  test("simple quoted string with whitespace", () => {
    expect(ok('put vertex greeting "hello world"')).toEqual([
      "put",
      "vertex",
      "greeting",
      "hello world",
    ]);
  });

  test("empty double-quoted string", () => {
    expect(ok('a "" b')).toEqual(["a", "", "b"]);
  });

  test('escape: \\" → "', () => {
    expect(ok('"say \\"hi\\""')).toEqual(['say "hi"']);
  });

  test("escape: \\\\ → \\", () => {
    expect(ok('"a\\\\b"')).toEqual(["a\\b"]);
  });

  test("escape: \\n → newline, \\t → tab, \\r → CR", () => {
    expect(ok('"a\\nb\\tc\\rd"')).toEqual(["a\nb\tc\rd"]);
  });

  test("multiple quoted tokens separated by whitespace", () => {
    expect(ok('"foo" "bar baz"')).toEqual(["foo", "bar baz"]);
  });

  test("quoted token alongside bareword", () => {
    expect(ok('cmd "with spaces" tail')).toEqual([
      "cmd",
      "with spaces",
      "tail",
    ]);
  });
});

describe("tokenise — single-quoted strings (#438)", () => {
  test("single-quoted carries embedded double-quote verbatim", () => {
    expect(ok(`put vertex code 'console.log("hi")'`)).toEqual([
      "put",
      "vertex",
      "code",
      `console.log("hi")`,
    ]);
  });

  test("single-quoted carries embedded backslash verbatim (no escapes)", () => {
    expect(ok(`'C:\\Users\\hiroki'`)).toEqual([`C:\\Users\\hiroki`]);
  });

  test("empty single-quoted string", () => {
    expect(ok("a '' b")).toEqual(["a", "", "b"]);
  });
});

describe("tokenise — errors (#438)", () => {
  test("unterminated double-quote", () => {
    expect(err('"hello world')).toBe("error: unterminated string literal");
  });

  test("unterminated single-quote", () => {
    expect(err("'hello world")).toBe("error: unterminated string literal");
  });

  test("trailing backslash inside double-quote is unterminated", () => {
    expect(err('"foo\\')).toBe("error: unterminated string literal");
  });

  test("unknown backslash escape inside double-quote", () => {
    expect(err('"bad \\q escape"')).toBe("error: invalid escape sequence \\q");
  });
});
