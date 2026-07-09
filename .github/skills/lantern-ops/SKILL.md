---
name: lantern-ops
description: "Operate Lantern data through the `lantern-cli` CLI — read, write, delete, scan, count, bulk-load, back up/restore, and graph-walk vertices and edges from the command line. Use whenever the request involves manipulating data in a running Lantern server (get/put/delete a vertex or edge, prefix scan or count, bulk NDJSON load, whole-graph dump/restore backup, prefix delete, or a bfs/pagerank/community graph walk), or driving Lantern from an interactive/agentic system. Do NOT use this skill for editing Lantern's own Go source, regenerating protobuf/wire code, server deployment/IaC, or the MCP working-context tools."
---

# Lantern Ops — CLI command reference

This skill is the canonical reference for operating **data** in a running
Lantern server through the official `lantern-cli` CLI. Read it before issuing any
data command so the arguments, flags, output shape, and exit codes are correct
without reading server source.

Lantern is an in-memory graph KVS ("key-vertex-store"). Every **vertex** (a
keyed, typed value) and every **edge** (a directed, weighted `tail -> head`
pair) carries a **TTL** and decays on its own. The CLI wraps every server RPC
as a scriptable subcommand plus an interactive REPL.

## Invoking the CLI

On **macOS** the lowest-friction path is the Homebrew cask — it installs a
released, prebuilt `lantern-cli` binary with no Go toolchain or repo checkout
required (first choice):

```shell
brew tap anaregdesign/tap
brew install --cask lantern-cli    # installs the `lantern-cli` binary
```

On Linux/Windows, grab the `lantern-cli` binary attached to any GitHub Release.
The Go-toolchain / in-repo routes below are the build-from-source fallback.

From inside this repo (no install, but needs the Go toolchain):

```shell
go run ./cli <args>                 # recompiles each call; fine for one-offs
```

Build once, then reuse the binary (preferred for repeated calls from a checkout):

```shell
go build -o lantern-cli ./cli
./lantern-cli <args>
```

The Homebrew/release binary and the in-repo build above are all named
`lantern-cli`. **All examples below use `lantern-cli <args>`** — substitute
`go run ./cli <args>` or `./lantern-cli <args>` as appropriate.

Default target is **`http://localhost:6380`** over plaintext h2c, which matches
the primary replica of the `deploy/compose` stack (replicas are exposed on host
ports `6380`, `6381`, `6382`). Override with the global flags below.

## Discovering help

The CLI's built-in help is intentionally long-form and example-rich because it
is meant to be driven by both humans and agents. **Treat `--help` as the
authoritative, version-accurate source of truth** — if this skill and the
CLI's own help ever disagree, the help output wins (it is generated from the
running binary). When unsure about a flag, default, or output shape, ask the
binary before guessing.

```shell
lantern-cli --help              # top-level: command layout, global flags, exit codes
lantern-cli <verb> --help       # a verb's grammar + examples, e.g. lantern-cli put --help
lantern help [verb]         # same content via cobra's `help` subcommand
lantern-cli <verb> -h           # `-h` is the short alias for `--help`
```

Examples worth reading before first use:

```shell
lantern-cli put --help               # vertex/edge writes: value typing (type=) + ttl_seconds
lantern-cli add --help               # additive vs idempotent edge write semantics
lantern-cli bfs --help               # breadth-first family knobs: step/fan-out + reduction/objective/weighting
lantern-cli pagerank --help          # Personalized PageRank knobs: top-n + restart-prob/epsilon
lantern-cli community --help         # local-community knobs: max-size + reduction/objective/weighting
lantern-cli delete-prefix --help     # the destructive-delete safety gate
```

Inside the interactive prompt, type `help` to print the per-verb grammar into
the scrollback; `lantern-cli repl --help` documents the REPL grammar, quoting, and
case rules. Shell completion install instructions: `lantern-cli completion --help`.

## Global connection flags

These apply to every subcommand and go **before** the subcommand (e.g.
`lantern-cli --tls -H host get vertex k`):

| Flag | Default | Meaning |
|---|---|---|
| `-H, --host` | `localhost` | server hostname |
| `-p, --port` | `6380` | server port |
| `--address` | — | full `host:port`; overrides `--host`/`--port` |
| `--timeout` | `5s` | per-RPC deadline; `0` disables |
| `--tls` | off | dial with TLS instead of plaintext (flips `http`→`https`) |
| `--tls-ca` | — | PEM bundle to verify the server cert |
| `--tls-cert` / `--tls-key` | — | client cert + key for mTLS (must be paired) |
| `--tls-server-name` | — | override the SNI / verification hostname |
| `--insecure-skip-verify` | off | skip server cert verification (**dev only**) |
| `--compression` | `none` | per-call compressor: `none` or `gzip` |
| `--chunk-size` | `1000` | batch chunk size for multi-key write/delete |

Supplying any of `--tls-ca`, `--tls-cert` also implies TLS (`https`).

## Core concepts (read before writing)

- **Value typing.** `put vertex` parses the raw arg and, by default, auto-types
  it (`auto`): `auto` tries int → float → bool → RFC3339 datetime → else
  string. Force the type when the value is ambiguous by appending a `type=`
  token (e.g. keep a leading zero with `type=string`). Supported types:
  `auto|string|int|float|bool|datetime|duration|json`. JSON **objects and
  arrays are re-encoded as a compact JSON string** on the wire (Lantern has no
  nested value variant); scalars pass through as their natural type.
- **TTL semantics.** The optional trailing `ttl_seconds` is an integer number
  of seconds, relative to the server's "now" at receipt (`30`, `300`, `3600`,
  `86400`). Omit it or pass `0` to store **permanently** (no decay). Expired
  entries are reaped lazily.
- **Edge `add` vs `put` are NOT equivalent.**
  - `add` (AddEdge) is **additive** — repeated calls **sum** the weight on the
    same `(tail, head)`. Not idempotent; excluded from client retry (a retry
    would double-count). Use for streaming counts/scores/interaction signal.
  - `put` (PutEdge) **replaces** the weight wholesale. Idempotent; retried on
    transient errors. Use when weight is a measured property (similarity,
    distance, capacity).
- **Implicit endpoint upsert.** Both edge write verbs materialise the `tail`
  and `head` vertices (with empty values) at the same TTL if they don't exist.
- **Lazy edge cleanup.** Deleting a vertex does **not** eagerly delete incident
  edges — they decay with their own TTL. A `GetEdge` right after a vertex
  delete may still briefly return the edge.
- **Output & exit codes.** Read commands print **JSON** on stdout; write
  commands print a single `OK` line. Errors go to stderr. Exit codes: `0`
  success, `1` local error (bad args, parse, file I/O), `2` RPC error returned
  by the server (`NotFound`, `InvalidArgument`, …).

## Vertex commands

### `get vertex <key>`
Fetch one vertex. Prints a JSON object `{key, type, value, expiration}`.
`NotFound` (exit 2) when the key is absent or expired.
```shell
lantern-cli get vertex alice
lantern-cli get vertex alice | jq .value
```

### `put vertex <key> <value> [ttl_seconds] [type=...]`
Upsert one vertex. Optional trailing `ttl_seconds` (integer; default permanent)
and a `type=<type>` token (default `auto`); the two may appear in either order.
Prints `OK`.
```shell
lantern-cli put vertex alice "Alice Smith"                    # string (auto)
lantern-cli put vertex count 42                                # int (auto)
lantern-cli put vertex price 19.99                             # float (auto)
lantern-cli put vertex alice '{"age":30}' 3600 type=json       # JSON value, 1h TTL
lantern-cli put vertex zipcode "01234" type=string             # keep leading zero
```

### `delete vertex <key> [<key>...]`
Delete one or more vertices. One key → `DeleteVertex`; multiple keys →
batch `DeleteVertices` (chunked at `--chunk-size`). Idempotent.
- Single: prints `OK existed=true|false`.
- Batch: prints `OK <n>` (number that actually existed and were removed).
```shell
lantern-cli delete vertex alice                 # single
lantern-cli delete vertex alice bob carol       # batch
cat keys.txt | xargs lantern-cli delete vertex  # batch from file
```

### `scan vertices <prefix> [limit] [all=true]`
Enumerate live vertices whose key begins with `<prefix>` and print them as an
indented **JSON array** on stdout. The optional positional `<limit>` caps the
page size; `all=true` iterates every page through the SDK helper and
concatenates the result into one array. Without `all=true` a single bounded
page is returned (paging is handled internally — pass `all=true` for a full
snapshot rather than resuming by hand).
```shell
lantern-cli scan vertices users/
lantern-cli scan vertices users/ all=true > snapshot.json
lantern-cli scan vertices users/ 50                  # first page, up to 50
```

### `count vertices <prefix>`
Print the number of keys in the prefix index as a single integer.
**Caveat:** counted from the radix index, not cross-checked for liveness, so it
may include expired-but-not-yet-reaped keys. For a strictly-live count use
`lantern-cli scan vertices <prefix> all=true | jq 'length'`.
```shell
lantern-cli count vertices users/
```

### `keys <prefix> [limit]`
List vertex **keys** under `<prefix>`, one per line on stdout — the
Redis-familiar, keys-only counterpart to `scan vertices` (no values, pipe-friendly).
Lantern is prefix-indexed, so the argument is a key **prefix**, not a glob (no
trailing `*`). A prefix is **required** (the server rejects an empty prefix); the
optional `<limit>` caps the page (mirrors `scan vertices`). This is a verb-first
one-liner — the same grammar as the `lantern-cli repl` prompt, backed by the
wire-efficient `ScanVertexKeys` RPC.
```shell
lantern-cli keys users/
lantern-cli keys users/ 100
lantern-cli keys users/ | xargs -n1 lantern-cli get vertex   # hydrate values
```

### `search <query>` — content relevance (BM25)
Full-text search over vertex **content** (key + value), ranked by BM25 — the
content counterpart to `scan vertices`' lexicographic key walk. Hits print one
per line as `<key>` and `<score>` (tab-separated), most relevant first. Requires
the server's search index (`LANTERN_SEARCH_ENABLED`, on by default; off ⇒
`FAILED_PRECONDITION`).

The default is an OR-union over the query words. Flags tune relevance (#892):

| flag | meaning |
|---|---|
| `--mode any\|all\|min-should` | term combination: OR (default), AND, or minimum-should-match |
| `--min-should <n>` | with `--mode min-should`, require at least `n` distinct query words |
| `--phrase` | require the words adjacent, in order (highest precision) |
| `--fuzziness 1\|2` | also match terms within that edit distance (typo tolerance) |
| `--prefix-terms` | also match terms that extend a query word (`lan` → `lantern`) |
| `--prefix <p>` | scope hits to a key namespace |
| `--limit <n>` | cap the ranked-hit count (`0` = server default) |

```shell
lantern-cli search "calm concise"
lantern-cli search "rolling update" --mode all
lantern-cli search "rolling update" --phrase
lantern-cli search serach --fuzziness 1          # finds "search"
lantern-cli search lan --prefix-terms            # finds "lantern"
lantern-cli search espresso --prefix user. --limit 20
```

Unlike `get` / `scan` / `keys`, `search` is a flag-based command (not part of the
shared REPL grammar), because its many relevance controls read better as flags.

### `delete-prefix vertices <prefix>` (DESTRUCTIVE)
Bulk-delete every live vertex under `<prefix>`, up to `limit=<n>` per call.
**Safety gate:** running without `dry_run=true` or `confirm=yes` is **refused**
— it prints the current match count + suggested next steps to stderr and exits
non-zero. Kwargs: `dry_run=true` (count only, mutates nothing), `confirm=yes`
(perform delete), `limit=<n>` (cap per call). Prints `would delete <n>` /
`deleted <n>`. Incident edges are not eagerly removed (lazy GC).
```shell
lantern-cli delete-prefix vertices tmp/                  # refused → prints suggestion
lantern-cli delete-prefix vertices tmp/ dry_run=true
lantern-cli delete-prefix vertices tmp/ confirm=yes
lantern-cli delete-prefix vertices tmp/ confirm=yes limit=500
```

## Edge commands

### `get edge <tail> <head>`
Fetch one edge. Prints `{tail, head, weight, expiration}`. `NotFound` (exit 2)
when the edge never existed, was deleted, or expired.
```shell
lantern-cli get edge alice bob
lantern-cli get edge alice bob | jq .weight
```

### `add edge <tail> <head> <weight> [ttl_seconds]`  (additive)
Sum `<weight>` onto `(tail, head)`. Optional trailing `ttl_seconds` resets the
edge's expiration to `now+ttl_seconds` each call (default permanent). Weight is
`float32`; `NaN`/`±Inf` are rejected. Prints `OK`.
```shell
lantern-cli add edge alice bob 1.5            # weight 1.5
lantern-cli add edge alice bob 0.5            # weight now 2.0
lantern-cli add edge alice bob 0.1 1800       # weight 2.1, TTL reset to 30m
```

### `add decaying-edge <tail> <head> <initial_weight> <ratio> <steps> <interval_seconds>`
Client-side sugar (no dedicated RPC — it fans out into one additive `AddEdges`
batch) that lays down a **geometrically decaying** weight curve on `(tail, head)`.
The live weight starts at `<initial_weight>` and is multiplied by `<ratio>` (open
interval `0 < ratio < 1`) every `<interval_seconds>`, reaching zero after
`<steps>` intervals. All six arguments are **required**; `<steps>` is capped at
16. The contributions telescope, so they sum to `<initial_weight>` (a
`16 → 8 → 4 → 2 → 1 → 0` staircase is written as `{8,4,2,1,1}`, not `{16,…}`).
The echo reports the full decay horizon (`steps × interval`) as the TTL.
```shell
# weight 16 now, halving every 60s: 16 → 8 → 4 → 2 → 1 → 0 over 5 minutes
lantern-cli add decaying-edge alice bob 16 0.5 5 60
```

### `put edge <tail> <head> <weight> [ttl_seconds]`  (idempotent)
Replace the `(tail, head)` weight. Optional trailing `ttl_seconds`
(default permanent). Prints `OK`.
```shell
lantern-cli put edge alice bob 1.5            # weight 1.5
lantern-cli put edge alice bob 0.5            # weight 0.5 (overwritten)
```

### `delete edge <tail> <head> [<tail> <head>...]`
Delete edges as a flat, whitespace-delimited sequence of `<tail> <head>` pairs.
Exactly one pair → `DeleteEdge`; two or more pairs → batch `DeleteEdges`
(chunked at `--chunk-size`). The token count must be even (each edge is two
tokens); there is no separator character to configure. Idempotent. Single →
`OK existed=true|false`; batch → `OK <n>`.
```shell
lantern-cli delete edge alice bob                      # single pair
lantern-cli delete edge alice bob bob carol carol dave # batch: 3 pairs
jq -r '.tail, .head' edges.json | xargs lantern-cli delete edge
```

### `scan edges <tail-prefix> [limit] [head=<prefix>] [all=true]`
Enumerate live edges in ascending `(tail, head)` order, printed as an indented
**JSON array** on stdout. The required positional `<tail-prefix>` may be the
empty string `""` to scan every tail; `head=<prefix>` narrows by head endpoint;
the optional positional `<limit>` caps the page; `all=true` iterates every page
into one array. A head-only scan (empty tail-prefix) still iterates every tail
(no global reverse index), so supplying both a tail-prefix and `head=` is the
most efficient shape.
```shell
lantern-cli scan edges user:
lantern-cli scan edges user: 100 head=post:
lantern-cli scan edges "" head=post: all=true > posts.json
```

## Graph walks: `bfs`, `pagerank`, `community`

Run a read-only graph walk from `<seed>` and emit the visited subgraph as
indented JSON `{vertices: {...}, edges: {tail: {head: weight}}}`. The CLI has
one verb per traversal family; each verb also accepts the REPL positional
grammar when more than the seed is supplied.

| Verb | Main knobs | Meaning |
|---|---|---|
| `bfs <seed>` | `--step`, `--fan-out`, `--reduction`, `--objective`, `--weighting`, `--prefix` | Greedy per-hop top-k breadth-first walk. `reduction=mst|spt` renders a tree view; `objective=min|max` controls both pruning and reduction direction. |
| `pagerank <seed>` | `--top-n`, `--restart-prob`, `--epsilon`, `--weighting`, `--prefix` | Personalized PageRank relevance star. No `reduction` or `objective` knob; `restart_prob` must be in `(0,1)` when supplied and `epsilon` must be positive when supplied. |
| `community <seed>` | `--max-size`, `--restart-prob`, `--epsilon`, `--reduction`, `--objective`, `--weighting`, `--prefix` | Conductance-cut local community. `max_size=0` lets the sweep decide; optional tree reduction renders a backbone view. |

```shell
lantern-cli bfs alice                                      # depth-5, per-hop top-3 defaults
lantern-cli bfs alice --step 2 --fan-out 5 --weighting tfidf
lantern-cli bfs alice 3 20 reduction=mst objective=min     # same grammar as the REPL
lantern-cli bfs alice --step 2 --fan-out 5 --prefix users/ # frontier restricted to users/
lantern-cli pagerank alice --top-n 8                       # PPR neighbourhood, server-default locality
lantern-cli pagerank alice 8 restart_prob=0.25 epsilon=1e-4
lantern-cli community alice --max-size 30                  # conductance-cut community
lantern-cli community alice 30 reduction=mst objective=min # community as a spanning-tree backbone
```

## `bulk` — NDJSON bulk load

Stream NDJSON (one JSON object per line) into Lantern via batch RPCs. Input is
a file path or `-` for stdin. Lines are accumulated into batches of
`--chunk-size` (default 1000). Progress prints to stderr (`... <n>`); `OK
<total>` prints to stdout at end. A malformed line aborts the stream (exit 1);
**already-sent batches are not rolled back** (Lantern has no transactions). Per
line, `"ttl"` is a Go duration string and is **permanent when omitted**;
`"value"` may be any JSON value.

```shell
# vertices: {"key":"alice","value":{"name":"Alice"},"ttl":"1h"}
lantern-cli bulk vertices vertices.ndjson

# edges add (additive): {"tail":"alice","head":"bob","weight":1.5,"ttl":"1h"}
cat edges.ndjson | lantern-cli bulk edges add -

# edges put (idempotent)
lantern-cli bulk edges put edges.ndjson --chunk-size 5000
```

## `dump` / `restore` — whole-graph backup

Back up the whole graph and reload it. Unlike `bulk` (per-type NDJSON load),
`dump` writes a **single consistent snapshot** of every live vertex **and**
edge — taken under one server-side lock — and `restore` replays it. Like
`bulk`, these are file-based commands (not part of the shared grammar).

- **`dump <file|->`** — `-`/omitted = stdout. `--format proto` (default,
  length-delimited protobuf, lossless) or `--format ndjson` (human-readable,
  one `{"kind":"vertex|edge",…}` object per line). `--prefix <p>` restricts to
  the induced subgraph over vertices with that key prefix (an edge is kept only
  when both endpoints match). The summary (`dumped <V> vertices, <E> edges`)
  prints to **stderr** so stdout can carry binary protobuf.
- **`restore <file|->`** — `-`/omitted = stdin. `--format` must match the
  dump. Records replay via `PutVertices`/`PutEdges` in `--chunk-size` batches;
  Put is idempotent, so re-running a restore is safe. A malformed record aborts
  (exit 1); **already-sent batches are not rolled back**. `OK <total>` prints to
  stdout. Absolute expirations round-trip; an expiration beyond the target
  server's `LANTERN_TOMBSTONE_TTL` cap is rejected (whole batch).

```shell
lantern-cli dump graph.lbk                  # whole graph -> file (protobuf)
lantern-cli dump - --format ndjson > g.ndjson
lantern-cli dump users.lbk --prefix user:   # partial backup
lantern-cli restore graph.lbk               # reload
cat g.ndjson | lantern-cli restore - --format ndjson
```

## Other commands

- **`repl`** — legacy interactive prompt. Same grammar as the admin `/cli`
  page; type `help` inside for the per-verb reference, `exit` to quit. Use for
  quick manual poking; prefer the one-shot subcommands above for scripting,
  batches, typed values, scans, bulk loads, TLS, and compression.
- **`version`** — print the client version.
- **`completion <bash|zsh|fish|powershell>`** — generate shell completions.

## Common recipes

```shell
# Snapshot a keyspace to a JSON array, then stream it elsewhere as NDJSON
lantern-cli scan vertices users/ all=true > users.json
jq -c '.[]' users.json | lantern-cli --address other-host:6380 bulk vertices -

# Count then safely purge a temporary keyspace
lantern-cli count vertices tmp/
lantern-cli delete-prefix vertices tmp/ dry_run=true
lantern-cli delete-prefix vertices tmp/ confirm=yes

# Accumulate an interaction signal with decay, then inspect the live sum
lantern-cli add edge userA itemX 1 86400
lantern-cli add edge userA itemX 1 86400
lantern-cli get edge userA itemX | jq .weight     # → 2

# Walk a relevance graph against a non-default replica over TLS
lantern-cli --tls --tls-ca ./ca.pem -H lantern.example.com -p 443 \
  bfs userA --step 2 --fan-out 10 --weighting tfidf
```

## Gotchas checklist

- Choosing `add edge` vs `put edge` wrong silently corrupts weights — `add`
  accumulates, `put` overwrites.
- `count vertices` can over-report vs. live reality; use `scan vertices <prefix>
  all=true | jq 'length'` for an exact live count.
- `delete-prefix vertices` without `dry_run=true`/`confirm=yes` is intentionally
  refused — pass one explicitly.
- Vertex deletes leave incident edges to decay on their own TTL.
- `scan` returns a single bounded page unless you pass `all=true`; there is no
  hand-managed cursor to resume — `all=true` walks every page for you.
- JSON object/array vertex values round-trip as compact JSON **strings**, not
  nested structures.
- `NotFound` surfaces as **exit code 2**, distinct from local errors (exit 1) —
  use it to tell "missing" from "broken".
