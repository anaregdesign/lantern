package parser

import "time"

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

type Illuminate struct {
	Seed      string
	Step      int
	K         int
	Algorithm string // "none" | "mst" | "spt" (default: "none")
	Objective string // "min" | "max"           (default: "max")
	Weighting string // "raw" | "tfidf" | "bm25" (default: "raw")
	Prefix    string // vertex-key prefix filter; "" (default) = no filter (#604)
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
