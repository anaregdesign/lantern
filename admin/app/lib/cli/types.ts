/**
 * REPL/CLI verb parser (#411).
 *
 * This is the TypeScript port of `cli/parser/` shared with the
 * lantern web admin `/cli` route. It accepts the same grammar the
 * Go REPL accepts post-#410, with the addition of the `scan` verb
 * shared by both surfaces.
 *
 * Drift-detection: `admin/test/cli-grammar/verbs.json` is loaded by
 * BOTH this parser's unit test (`grammar.test.ts`) and the upstream
 * Go test (`cli/parser/grammar_fixture_test.go`). If the two parsers
 * disagree on any fixture entry, both sides go red.
 *
 * Tokenisation matches the Go side: lower-cased, whitespace-split.
 */

export type AlgorithmName = "none" | "mst" | "spt" | "ppr";
export type ObjectiveName = "min" | "max";
export type WeightingName = "raw" | "tfidf" | "bm25";

export type Command =
  | { verb: "exit" }
  | { verb: "help" }
  | { verb: "get"; objective: "vertex"; key: string }
  | { verb: "get"; objective: "edge"; tail: string; head: string }
  | {
      verb: "put";
      objective: "vertex";
      key: string;
      value: string;
      ttlSeconds: number | null;
      valueType: string;
    }
  | {
      verb: "put";
      objective: "edge";
      tail: string;
      head: string;
      weight: number;
      ttlSeconds: number | null;
    }
  | { verb: "delete"; objective: "vertex"; keys: string[] }
  | {
      verb: "delete";
      objective: "edge";
      pairs: { tail: string; head: string }[];
    }
  | {
      verb: "add";
      objective: "edge";
      tail: string;
      head: string;
      weight: number;
      ttlSeconds: number | null;
    }
  | {
      verb: "scan";
      objective: "vertices";
      prefix: string;
      limit: number;
      all: boolean;
    }
  | {
      verb: "scan";
      objective: "edges";
      tailPrefix: string;
      headPrefix: string;
      limit: number;
      all: boolean;
    }
  | { verb: "keys"; prefix: string; limit: number }
  | { verb: "count"; objective: "vertices"; prefix: string }
  | {
      verb: "delete-prefix";
      objective: "vertices";
      prefix: string;
      limit: number;
      dryRun: boolean;
      confirm: boolean;
    }
  | {
      verb: "illuminate";
      seed: string;
      step: number;
      k: number;
      algorithm: AlgorithmName;
      objective: ObjectiveName;
      weighting: WeightingName;
      vertexPrefix: string;
      /**
       * Personalized PageRank knobs (#801), only meaningful when
       * `algorithm === "ppr"`. `restartProb` is the restart/teleport-to-seed
       * probability α in (0,1); `epsilon` is the forward-push residual
       * threshold ε > 0. Both default to 0, which the server resolves to its
       * own defaults (α=0.15 / ε=1e-4); they are ignored for other algorithms.
       */
      restartProb: number;
      epsilon: number;
    };

export type ParseResult =
  | { ok: true; command: Command }
  | { ok: false; usage: string };
