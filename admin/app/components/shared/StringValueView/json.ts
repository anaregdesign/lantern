// Pure helpers for the JSON-aware rendering of `string` vertex values (#759).
// No React, no dependencies — the tokenizer is hand-rolled so syntax
// highlighting costs the bundle nothing.

/** The linted (pretty-printed) form of a JSON value. */
export interface FormattedJson {
  /** The value re-serialized with two-space indentation. */
  formatted: string;
}

/**
 * Returns the linted (pretty-printed) form of `value` when it parses as a JSON
 * object or array, otherwise `null`. Only `{`/`[` documents qualify, so plain
 * prose and bare scalars (a lone number, `true`, a quoted word) are never
 * reinterpreted as JSON. The pretty-print doubles as validation: malformed
 * JSON throws and yields `null`, falling back to the raw text view.
 */
export function formatJson(value: string): FormattedJson | null {
  const trimmed = value.trim();
  if (trimmed === "" || (trimmed[0] !== "{" && trimmed[0] !== "[")) {
    return null;
  }
  let parsed: unknown;
  try {
    parsed = JSON.parse(trimmed);
  } catch {
    return null;
  }
  // A leading `{`/`[` guarantees an object or array, but guard anyway so a
  // non-object can never slip through to the highlighter.
  if (parsed === null || typeof parsed !== "object") {
    return null;
  }
  return { formatted: JSON.stringify(parsed, null, 2) };
}

/** The lexical class of a JSON token, used to pick a highlight colour. */
export type JsonTokenKind =
  | "key"
  | "string"
  | "number"
  | "boolean"
  | "null"
  | "punctuation"
  | "whitespace";

/** A contiguous run of source text classified for syntax highlighting. */
export interface JsonToken {
  kind: JsonTokenKind;
  text: string;
}

function isWhitespace(c: string): boolean {
  return c === " " || c === "\n" || c === "\t" || c === "\r";
}

function isDigit(c: string): boolean {
  return c >= "0" && c <= "9";
}

// readString returns the index just past the closing quote of the JSON string
// that starts at `start` (where source[start] === '"'). Escaped characters are
// skipped so an embedded \" does not end the string early.
function readString(source: string, start: number): number {
  let i = start + 1;
  while (i < source.length) {
    const c = source[i];
    if (c === "\\") {
      i += 2;
      continue;
    }
    if (c === '"') {
      return i + 1;
    }
    i++;
  }
  return i; // unterminated — only reachable on malformed input
}

// readNumber returns the index just past the JSON number starting at `start`.
function readNumber(source: string, start: number): number {
  let i = start;
  if (source[i] === "-") i++;
  while (i < source.length && isDigit(source[i])) i++;
  if (source[i] === ".") {
    i++;
    while (i < source.length && isDigit(source[i])) i++;
  }
  if (source[i] === "e" || source[i] === "E") {
    i++;
    if (source[i] === "+" || source[i] === "-") i++;
    while (i < source.length && isDigit(source[i])) i++;
  }
  return i;
}

/**
 * Splits pretty-printed JSON into a flat list of highlight tokens that, when
 * concatenated, reproduce the input exactly (whitespace included), so a caller
 * can wrap each token in a coloured span inside a `<pre>`. A quoted string is
 * classified as a `key` when the next non-whitespace character is `:`, else as
 * a `string`. The tokenizer never throws; on unexpected input it falls back to
 * single-character punctuation tokens.
 */
export function tokenizeJson(source: string): JsonToken[] {
  const tokens: JsonToken[] = [];
  const n = source.length;
  let i = 0;
  while (i < n) {
    const c = source[i];
    if (isWhitespace(c)) {
      let end = i + 1;
      while (end < n && isWhitespace(source[end])) end++;
      tokens.push({ kind: "whitespace", text: source.slice(i, end) });
      i = end;
    } else if (c === '"') {
      const end = readString(source, i);
      let j = end;
      while (j < n && isWhitespace(source[j])) j++;
      const kind: JsonTokenKind = source[j] === ":" ? "key" : "string";
      tokens.push({ kind, text: source.slice(i, end) });
      i = end;
    } else if (c === "-" || isDigit(c)) {
      const end = readNumber(source, i);
      tokens.push({ kind: "number", text: source.slice(i, end) });
      i = end;
    } else if (source.startsWith("true", i) || source.startsWith("false", i)) {
      const word = source.startsWith("true", i) ? "true" : "false";
      tokens.push({ kind: "boolean", text: word });
      i += word.length;
    } else if (source.startsWith("null", i)) {
      tokens.push({ kind: "null", text: "null" });
      i += 4;
    } else {
      tokens.push({ kind: "punctuation", text: c });
      i++;
    }
  }
  return tokens;
}
