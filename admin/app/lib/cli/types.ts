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

// AlgorithmName is the traversal FAMILY selector used by the click-to-illuminate
// axis picker (#464, #975). Since #975 the family is a first-class verb, so these
// are the verb names (`ppr` was renamed to `pagerank` per the user's preference).
export type AlgorithmName = "bfs" | "pagerank" | "community";
export type ReductionName = "none" | "mst" | "spt";
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
      /**
       * `add decaying-edge <tail> <head> <initial_weight> <ratio> <steps>
       * <interval_seconds>` (#953). Client-side geometric decay: the SDK
       * (`addDecayingEdge`) expands it into one staggered-TTL `AddEdges`
       * batch. The parser validates operand *types* only — numeric-range
       * checks (`ratio` in (0,1), `steps` in [1, MAX_DECAY_STEPS],
       * `intervalSeconds` > 0) are deferred to the SDK's `DecayOptions`
       * contract, mirroring the Go parser which defers to `client.DecayOpts`.
       */
      verb: "add";
      objective: "decaying-edge";
      tail: string;
      head: string;
      initialWeight: number;
      ratio: number;
      steps: number;
      intervalSeconds: number;
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
      /**
       * bfs family verb (#975): a greedy per-hop top-k breadth-first walk.
       * Only `seed` is required; `step` (walk depth, default 5) and `fanOut`
       * (per-hop top-k prune, default 3) are optional positional ints or
       * `step=`/`fan_out=` kwargs. `reduction` renders the result as an
       * MST/SPT tree rooted at the seed (default none); `objective` steers
       * BOTH the per-hop pruning and the reduction direction (default max).
       */
      verb: "bfs";
      seed: string;
      step: number;
      fanOut: number;
      reduction: ReductionName;
      objective: ObjectiveName;
      weighting: WeightingName;
      vertexPrefix: string;
    }
  | {
      /**
       * pagerank family verb (#975): Personalized PageRank from the seed,
       * returning a relevance star (which is already a tree, so pagerank has
       * no reduction/objective). `topN` caps the star (default 10; 0 = every
       * positive-mass vertex). `restartProb` (α) and `epsilon` (ε) default to
       * 0, which the server resolves to α=0.15 / ε=1e-4.
       */
      verb: "pagerank";
      seed: string;
      topN: number;
      restartProb: number;
      epsilon: number;
      weighting: WeightingName;
      vertexPrefix: string;
    }
  | {
      /**
       * community family verb (#975): conductance-optimal local community
       * extraction (#845). `maxSize` is an UPPER BOUND (default 0 = the sweep
       * decides). `restartProb`/`epsilon` share PPR's semantics/defaults;
       * `reduction`/`objective` render an optional tree view of the community.
       */
      verb: "community";
      seed: string;
      maxSize: number;
      restartProb: number;
      epsilon: number;
      reduction: ReductionName;
      objective: ObjectiveName;
      weighting: WeightingName;
      vertexPrefix: string;
    };

export type ParseResult =
  | { ok: true; command: Command }
  | { ok: false; usage: string };
