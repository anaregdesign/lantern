/**
 * Tokenisation for the admin CLI grammar (#411).
 *
 * Mirrors `cli/parser/source.go` `NewSource`. The fixture under
 * `admin/test/cli-grammar/verbs.json` is loaded by both this side
 * and the Go side, so any drift in tokenisation surfaces on the
 * next CI run.
 *
 * Grammar (#437 + #438):
 *
 *   token = bareword | double-quoted | single-quoted
 *   bareword       = first char ∉ {whitespace, '"', "'"} ; consume until next whitespace.
 *                    Quotes inside a bareword are kept verbatim (so `foo"bar` is one token).
 *   double-quoted  = `"` … `"` with C-style escapes  `\"`, `\\`, `\n`, `\r`, `\t`.
 *                    Any other backslash sequence is a parse error.
 *   single-quoted  = `'` … `'`  with NO escapes (verbatim payload).
 *
 * Quoting is only special at the **start** of a token — once a bareword
 * has begun, every subsequent non-whitespace character (including
 * quotes) is part of that bareword. This keeps `key=foo"bar` working
 * without escaping while still letting `put vertex k "hello world"`
 * carry whitespace.
 *
 * The tokeniser does NOT lowercase. Case-folding for the verb and
 * objective slots happens at the comparison site (parser.ts / verbs.ts),
 * which matches the Go REPL: `service.Run` only ToLowers the dispatch
 * tokens, never the arguments. This preserves user-supplied casing in
 * vertex keys, edge endpoints, and illuminate seeds — see #437.
 *
 * Unterminated quotes return an error; the caller surfaces it as the
 * parse-time usage line.
 */

export type TokeniseResult =
  | { ok: true; tokens: string[] }
  | { ok: false; error: string };

const WHITESPACE = /\s/;

export function tokenise(input: string): TokeniseResult {
  const tokens: string[] = [];
  let i = 0;
  const n = input.length;
  while (i < n) {
    const ch = input[i];
    if (WHITESPACE.test(ch)) {
      i++;
      continue;
    }
    if (ch === '"') {
      const r = scanDoubleQuoted(input, i);
      if (!r.ok) return r;
      tokens.push(r.value);
      i = r.next;
      continue;
    }
    if (ch === "'") {
      const r = scanSingleQuoted(input, i);
      if (!r.ok) return r;
      tokens.push(r.value);
      i = r.next;
      continue;
    }
    // bareword: consume up to next whitespace (quotes embedded mid-token
    // stay verbatim, matching the issue's "only special at token
    // boundaries" rule).
    let j = i + 1;
    while (j < n && !WHITESPACE.test(input[j])) j++;
    tokens.push(input.slice(i, j));
    i = j;
  }
  return { ok: true, tokens };
}

type ScanResult =
  | { ok: true; value: string; next: number }
  | { ok: false; error: string };

function scanDoubleQuoted(input: string, start: number): ScanResult {
  // input[start] === '"'
  const out: string[] = [];
  let i = start + 1;
  const n = input.length;
  while (i < n) {
    const ch = input[i];
    if (ch === '"') {
      return { ok: true, value: out.join(""), next: i + 1 };
    }
    if (ch === "\\") {
      if (i + 1 >= n) {
        return { ok: false, error: "error: unterminated string literal" };
      }
      const esc = input[i + 1];
      switch (esc) {
        case '"':
          out.push('"');
          break;
        case "\\":
          out.push("\\");
          break;
        case "n":
          out.push("\n");
          break;
        case "r":
          out.push("\r");
          break;
        case "t":
          out.push("\t");
          break;
        default:
          return {
            ok: false,
            error: `error: invalid escape sequence \\${esc}`,
          };
      }
      i += 2;
      continue;
    }
    out.push(ch);
    i++;
  }
  return { ok: false, error: "error: unterminated string literal" };
}

function scanSingleQuoted(input: string, start: number): ScanResult {
  // input[start] === "'"
  let i = start + 1;
  const n = input.length;
  while (i < n) {
    if (input[i] === "'") {
      return { ok: true, value: input.slice(start + 1, i), next: i + 1 };
    }
    i++;
  }
  return { ok: false, error: "error: unterminated string literal" };
}
