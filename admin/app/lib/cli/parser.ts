/**
 * REPL/CLI verb parser entry. See `types.ts` for the public Command
 * shape and `tokenise.ts` for the quoting-aware tokeniser.
 *
 * The dispatch layout matches `cli/parser/validate.go`'s switch
 * statement so the error messages line up: the Go test under
 * `cli/parser/grammar_fixture_test.go` and this side's bun test
 * (`grammar.test.ts`) both consume `admin/test/cli-grammar/verbs.json`.
 *
 * Verb names are matched case-insensitively (mirrors the Go REPL's
 * `strings.ToLower(verb)` dispatch). Arguments are passed through
 * untouched — see #437.
 */

import type { ParseResult } from "./types";
import { tokenise } from "./tokenise";
import {
  parseAdd,
  parseCount,
  parseDelete,
  parseDeletePrefix,
  parseGet,
  parseHelp,
  parseIlluminate,
  parseKeys,
  parsePut,
  parseScan,
} from "./verbs";

const VERB_LIST_USAGE =
  "usage: { get | put | delete | delete-prefix | add | scan | count | keys | illuminate | help | exit } ...";

const VERBS = new Set([
  "exit",
  "help",
  "get",
  "put",
  "delete",
  "delete-prefix",
  "add",
  "scan",
  "count",
  "keys",
  "illuminate",
]);

export function parse(input: string): ParseResult {
  const r = tokenise(input);
  if (!r.ok) {
    return { ok: false, usage: r.error };
  }
  const tokens = r.tokens;
  if (tokens.length === 0) {
    return { ok: false, usage: VERB_LIST_USAGE };
  }
  const verb = tokens[0].toLowerCase();
  if (!VERBS.has(verb)) {
    return { ok: false, usage: VERB_LIST_USAGE };
  }
  const rest = tokens.slice(1);
  switch (verb) {
    case "exit":
      if (rest.length !== 0) {
        return { ok: false, usage: "usage: exit" };
      }
      return { ok: true, command: { verb: "exit" } };
    case "help":
      return parseHelp(rest);
    case "get":
      return parseGet(rest);
    case "put":
      return parsePut(rest);
    case "delete":
      return parseDelete(rest);
    case "add":
      return parseAdd(rest);
    case "scan":
      return parseScan(rest);
    case "count":
      return parseCount(rest);
    case "delete-prefix":
      return parseDeletePrefix(rest);
    case "keys":
      return parseKeys(rest);
    case "illuminate":
      return parseIlluminate(rest);
  }
  return { ok: false, usage: VERB_LIST_USAGE };
}

export type { Command, ParseResult } from "./types";
