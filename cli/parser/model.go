package parser

import "time"

// Help is the parsed `help [topic]` command. Topic is empty for the complete
// grammar overview, or one of the known traversal-family topics.
type Help struct {
	Topic string
}

type GetVertex struct {
	Key string
}

type GetEdge struct {
	Tail string
	Head string
}

// PutVertex backs `put vertex <key> <value> [ttl_seconds] [type=…]`. Type is
// the optional value-type override migrated from `vertex put --value-type`:
// "" / "auto" auto-detects (int→float→bool→RFC3339→string); otherwise one of
// string|int|float|bool|datetime|duration|json.
type PutVertex struct {
	Key   string
	Value any
	TTL   time.Duration
	Type  string
}

type PutEdge struct {
	Tail   string
	Head   string
	Weight float32
	TTL    time.Duration
}

type AddEdge struct {
	Tail   string
	Head   string
	Weight float32
	TTL    time.Duration
}

// AddDecayingEdge backs
// `add decaying-edge <tail> <head> <initial_weight> <ratio> <steps> <interval_seconds>`.
// The fields map 1:1 onto client.DecayOpts: the edge's contributed live weight
// starts at InitialWeight and is multiplied by Ratio every Interval, reaching
// zero after Steps intervals. The dispatcher expands it client-side into an
// AddEdges batch of staggered-TTL contributions — no server support required
// (#952). Numeric-range validation (Ratio in (0,1), Steps in
// [1, client.MaxDecaySteps], Interval > 0) is deferred to the SDK, which owns
// the DecayOpts contract.
type AddDecayingEdge struct {
	Tail          string
	Head          string
	InitialWeight float32
	Ratio         float32
	Steps         int
	Interval      time.Duration
}

// DeleteVertex backs `delete vertex <key> [<key> …]`. Keys holds one or
// more keys; the dispatcher forwards a one-element batch to DeleteVertex
// and a multi-element batch to DeleteVertices.
type DeleteVertex struct {
	Keys []string
}

// EdgePair is one (tail, head) pair in a batch edge delete.
type EdgePair struct {
	Tail string
	Head string
}

// DeleteEdge backs `delete edge <tail> <head> [<tail> <head> …]`. Pairs
// holds one or more (tail, head) pairs (the token count must be even).
type DeleteEdge struct {
	Pairs []EdgePair
}

// Bfs backs the bfs family verb (#975):
// `bfs <seed> [step] [fan_out] [reduction=…] [objective=…] [weighting=…] [prefix=…]`.
// Step/FanOut are optional positional ints (defaults 5/3) and may also be given
// as step=/fan_out= kwargs. bfs is the only family with step and reduction knobs.
type Bfs struct {
	Seed      string
	Step      uint32 // walk depth (default 5)
	FanOut    uint32 // per-hop top-k prune (default 3)
	Reduction string // "none" | "mst" | "spt" (default: "none")
	Objective string // "min" | "max"          (default: "max")
	Weighting string // "raw" | "tfidf" | "bm25" (default: "raw")
	Prefix    string // vertex-key prefix filter; "" (default) = no filter (#604)
}

// Pagerank backs the pagerank family verb (#975):
// `pagerank <seed> [top_n] [restart_prob=…] [epsilon=…] [weighting=…] [prefix=…]`.
// TopN is an optional positional int (default 10; 0 = every positive-mass
// vertex). RestartProb/Epsilon default to 0, which the server resolves to
// α=0.15 / ε=1e-4. Personalized PageRank returns a relevance star, so it has no
// reduction/objective knob.
type Pagerank struct {
	Seed        string
	TopN        uint32  // cap the star to the top-N by mass (default 10; 0 = all)
	RestartProb float32 // restart prob α; 0 (default) = server default 0.15 (#801)
	Epsilon     float32 // residual threshold ε; 0 (default) = server default 1e-4 (#801)
	Weighting   string  // "raw" | "tfidf" | "bm25" (default: "raw")
	Prefix      string  // vertex-key prefix filter; "" (default) = no filter (#604)
}

// Community backs the community family verb (#975):
// `community <seed> [max_size] [restart_prob=…] [epsilon=…] [reduction=…] [objective=…] [weighting=…] [prefix=…]`.
// MaxSize is an optional positional int (default 0 = the conductance sweep alone
// decides the size). RestartProb/Epsilon share PPR's semantics/defaults;
// reduction/objective render an optional tree view of the community (#845).
type Community struct {
	Seed        string
	MaxSize     uint32  // size upper bound (default 0 = sweep decides)
	RestartProb float32 // restart prob α; 0 (default) = server default 0.15 (#845)
	Epsilon     float32 // residual threshold ε; 0 (default) = server default 1e-4 (#845)
	Reduction   string  // "none" | "mst" | "spt" (default: "none")
	Objective   string  // "min" | "max"          (default: "max")
	Weighting   string  // "raw" | "tfidf" | "bm25" (default: "raw")
	Prefix      string  // vertex-key prefix filter; "" (default) = no filter (#604)
}

// ScanVertices backs `scan vertices <prefix> [limit] [all=true]` (#411,
// extended in #679). Limit is optional (0 = server default); All iterates
// every page and renders the full result.
type ScanVertices struct {
	Prefix string
	Limit  int
	All    bool
}

// ScanEdges backs `scan edges <tail-prefix> [limit] [head=<prefix>]
// [all=true]`. HeadPrefix narrows the head dimension as a server-side index
// lookup; All iterates every page.
type ScanEdges struct {
	TailPrefix string
	HeadPrefix string
	Limit      int
	All        bool
}

// Keys backs the `keys <prefix> [limit]` verb — the Redis-familiar key lister
// that lists vertex keys under a prefix (keys-only output). Lantern is a
// prefix-indexed store, so Prefix is a key PREFIX, not a glob. Limit is
// optional (0 = server default), mirroring ScanVertices.
type Keys struct {
	Prefix string
	Limit  int
}

// Search backs the shared content-search grammar used by the Go REPL,
// verb-first Cobra command, and Admin /cli (#1068):
//
//	search <query> [limit=<uint32>] [prefix=<string>] [mode=server|any|all|min-should]
//	       [min_should=<uint32>] [phrase=<bool>] [fuzziness=0|1|2]
//	       [prefix_terms=<bool>] [cursor=<base64url>] [all=<bool>]
//	       [projection=key-score|full-vertex] [format=json|ndjson|tsv]
//
// Cursor remains opaque at the grammar boundary and is decoded only by the
// service that builds SDK options. Format is empty when omitted so the output
// layer can resolve the contextual default (json for one page, ndjson for
// all=true) without losing whether the operator selected a format explicitly.
type Search struct {
	Query       string
	Limit       uint32
	Prefix      string
	Mode        string
	MinShould   uint32
	Phrase      bool
	Fuzziness   uint32
	PrefixTerms bool
	Cursor      string
	All         bool
	Projection  string
	Format      string
}

// CountVertices backs `count vertices <prefix>` — the prefix-cardinality
// verb migrated from the former `vertex count` subcommand.
type CountVertices struct {
	Prefix string
}

// DeletePrefixVertices backs `delete-prefix vertices <prefix> [limit=<n>]
// [confirm=yes|dry_run=true]` — the destructive prefix delete migrated from
// `vertex delete-prefix`. Exactly one of Confirm / DryRun must be set (the
// safety gate); a bare `delete-prefix vertices p` is a usage error.
type DeletePrefixVertices struct {
	Prefix  string
	Limit   int
	DryRun  bool
	Confirm bool
}
