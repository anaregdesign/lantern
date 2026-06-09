/**
 * Tiny, dependency-free JSON tokenizer for the /cli scrollback (#512).
 *
 * The admin bundle deliberately carries no syntax-highlighting library,
 * so the CLI highlights its `OK` JSON payloads with this in-house pass
 * instead of pulling in Prism/Shiki/highlight.js. The tokenizer is total
 * — it never throws — and is split out from its presentational component
 * ({@link ../../../../components/cli/JsonView/JsonView}) so the lexing
 * rules can be unit-tested under `bun:test` without a DOM.
 *
 * Invariant relied upon by the renderer and the e2e suite: the
 * concatenation of every token's `text`, in order, reproduces the input
 * byte-for-byte. That keeps Playwright `toContainText` assertions green
 * (the visible text is unchanged; only per-token colouring is added) and
 * means a tokenizer bug can never silently drop characters.
 */

export type JsonTokenKind =
  | "key"
  | "string"
  | "number"
  | "boolean"
  | "null"
  | "punctuation"
  | "whitespace";

export interface JsonToken {
  kind: JsonTokenKind;
  text: string;
}

function isWhitespace(ch: string): boolean {
  return ch === " " || ch === "\n" || ch === "\t" || ch === "\r";
}

function isDigit(ch: string): boolean {
  return ch >= "0" && ch <= "9";
}

/** Characters that may legally appear in a JSON number body after the first. */
function isNumberPart(ch: string): boolean {
  return (
    isDigit(ch) ||
    ch === "." ||
    ch === "e" ||
    ch === "E" ||
    ch === "+" ||
    ch === "-"
  );
}

/**
 * Lex a JSON document (typically the pretty-printed output of
 * `JSON.stringify(value, replacer, 2)`) into colourable tokens.
 *
 * Object keys are distinguished from ordinary string values in a second
 * pass: a `string` token is reclassified as a `key` when the next
 * non-whitespace token is a `:` punctuation token. Malformed input
 * degrades gracefully — any character that does not start a recognised
 * token is emitted as a single-character `punctuation` token so the
 * concatenation invariant always holds.
 */
export function tokenizeJson(src: string): JsonToken[] {
  const tokens: JsonToken[] = [];
  const n = src.length;
  let i = 0;

  while (i < n) {
    const ch = src[i];

    if (isWhitespace(ch)) {
      let j = i + 1;
      while (j < n && isWhitespace(src[j])) j++;
      tokens.push({ kind: "whitespace", text: src.slice(i, j) });
      i = j;
      continue;
    }

    if (ch === '"') {
      let j = i + 1;
      while (j < n) {
        if (src[j] === "\\") {
          j += 2; // skip the escaped character
          continue;
        }
        if (src[j] === '"') {
          j += 1; // include the closing quote
          break;
        }
        j += 1;
      }
      tokens.push({ kind: "string", text: src.slice(i, Math.min(j, n)) });
      i = Math.min(j, n);
      continue;
    }

    if (
      ch === "{" ||
      ch === "}" ||
      ch === "[" ||
      ch === "]" ||
      ch === ":" ||
      ch === ","
    ) {
      tokens.push({ kind: "punctuation", text: ch });
      i += 1;
      continue;
    }

    if (ch === "-" || isDigit(ch)) {
      let j = i + 1;
      while (j < n && isNumberPart(src[j])) j++;
      tokens.push({ kind: "number", text: src.slice(i, j) });
      i = j;
      continue;
    }

    if (src.startsWith("true", i)) {
      tokens.push({ kind: "boolean", text: "true" });
      i += 4;
      continue;
    }
    if (src.startsWith("false", i)) {
      tokens.push({ kind: "boolean", text: "false" });
      i += 5;
      continue;
    }
    if (src.startsWith("null", i)) {
      tokens.push({ kind: "null", text: "null" });
      i += 4;
      continue;
    }

    // Unrecognised character — emit it verbatim so the concatenation
    // invariant holds even for non-JSON input.
    tokens.push({ kind: "punctuation", text: ch });
    i += 1;
  }

  markKeys(tokens);
  return tokens;
}

/**
 * Reclassify string tokens that name object members. A `string` becomes a
 * `key` when, skipping any whitespace, the following token is `:`.
 */
function markKeys(tokens: JsonToken[]): void {
  for (let k = 0; k < tokens.length; k++) {
    if (tokens[k].kind !== "string") continue;
    let m = k + 1;
    while (m < tokens.length && tokens[m].kind === "whitespace") m++;
    const next = tokens[m];
    if (next && next.kind === "punctuation" && next.text === ":") {
      tokens[k] = { kind: "key", text: tokens[k].text };
    }
  }
}
