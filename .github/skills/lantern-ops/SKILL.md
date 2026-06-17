---
name: lantern-ops
description: "Operate Lantern data through the `lantern` CLI — read, write, delete, scan, count, bulk-load, and graph-walk vertices and edges from the command line. Use whenever the request involves manipulating data in a running Lantern server (get/put/delete a vertex or edge, prefix scan or count, bulk NDJSON load, prefix delete, or an illuminate graph walk), or driving Lantern from an interactive/agentic system. Do NOT use this skill for editing Lantern's own Go source, regenerating protobuf/wire code, server deployment/IaC, or the MCP decaying-memory tools."
---

# Lantern Ops — CLI command reference

This skill is the canonical reference for operating **data** in a running
Lantern server through the official `lantern` CLI. Read it before issuing any
data command so the arguments, flags, output shape, and exit codes are correct
without reading server source.

Lantern is an in-memory graph KVS ("key-vertex-store"). Every **vertex** (a
keyed, typed value) and every **edge** (a directed, weighted `tail -> head`
pair) carries a **TTL** and decays on its own. The CLI wraps every server RPC
as a scriptable subcommand plus an interactive REPL.

## Invoking the CLI

From inside this repo (no install needed):

```shell
go run ./cli <args>                 # recompiles each call; fine for one-offs
```

Build once, then reuse the binary (preferred for repeated calls):

```shell
go build -o lantern ./cli
./lantern <args>
```

A released binary is named `lantern-cli`; the in-repo build above is named
`lantern`. **All examples below use `lantern <args>`** — substitute
`go run ./cli <args>` or `./lantern <args>` as appropriate.

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
lantern --help              # top-level: command layout, global flags, exit codes
lantern <cmd> --help        # a command group, e.g. lantern vertex --help
lantern <cmd> <sub> --help  # a specific subcommand, e.g. lantern vertex put --help
lantern help [cmd]          # same content via cobra's `help` subcommand
lantern <cmd> -h            # `-h` is the short alias for `--help`
```

Examples worth reading before first use:

```shell
lantern vertex put --help        # value typing (--value-type) + TTL semantics
lantern edge add --help          # additive vs idempotent write semantics
lantern illuminate --help        # the algorithm × objective × weighting axes
lantern vertex delete-prefix --help  # the destructive-delete safety gate
```

Inside the interactive prompt, type `help` to print the per-verb grammar into
the scrollback; `lantern repl --help` documents the REPL grammar, quoting, and
case rules. Shell completion install instructions: `lantern completion --help`.

## Global connection flags

These apply to every subcommand and go **before** the subcommand (e.g.
`lantern --tls -H host vertex get k`):

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

- **Value typing.** `vertex put` parses the raw arg per `--value-type`
  (default `auto`): `auto` tries int → float → bool → RFC3339 datetime → else
  string. Force the type when the value is ambiguous (e.g. keep a leading zero
  with `--value-type=string`). Supported types: `auto|string|int|float|bool|
  datetime|duration|json`. JSON **objects and arrays are re-encoded as a
  compact JSON string** on the wire (Lantern has no nested value variant);
  scalars pass through as their natural type.
- **TTL semantics.** `--ttl` is a Go duration relative to the server's "now"
  at receipt (`30s`, `5m`, `1h`, `24h`, `168h`). Omit `--ttl` or pass `0` to
  store **permanently** (no decay). Expired entries are reaped lazily.
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

### `vertex get <key>`
Fetch one vertex. Prints a JSON object `{key, type, value, expiration}`.
`NotFound` (exit 2) when the key is absent or expired.
```shell
lantern vertex get alice
lantern vertex get alice | jq .value
```

### `vertex put <key> <value>`
Upsert one vertex. Flags: `--ttl <dur>` (default permanent),
`--value-type <type>` (default `auto`). Prints `OK`.
```shell
lantern vertex put alice "Alice Smith"                    # string (auto)
lantern vertex put count 42                                # int (auto)
lantern vertex put price 19.99                             # float (auto)
lantern vertex put alice '{"age":30}' --value-type=json --ttl 1h
lantern vertex put zipcode "01234" --value-type=string     # keep leading zero
```

### `vertex delete <key> [<key>...]`
Delete one or more vertices. One key → `DeleteVertex`; multiple keys →
batch `DeleteVertices` (chunked at `--chunk-size`). Idempotent.
- Single: prints `OK existed=true|false`.
- Batch: prints `OK <n>` (number that actually existed and were removed).
```shell
lantern vertex delete alice                 # single
lantern vertex delete alice bob carol       # batch
cat keys.txt | xargs lantern vertex delete  # batch from file
```

### `vertex scan <prefix>`
Enumerate live vertices whose key begins with `<prefix>`, one NDJSON object per
line on stdout. Flags: `--limit <n>` (per-page size), `--cursor <token>`
(resume), `--all` (iterate every page through the SDK helper).
Without `--all`, a `next-cursor: <token>` line is printed to **stderr** when
more pages exist; re-run with `--cursor <token>`. Cursors are opaque — do not
hand-craft them.
```shell
lantern vertex scan users/
lantern vertex scan users/ --all > snapshot.ndjson
lantern vertex scan users/ --limit 50            # then read stderr for cursor
lantern vertex scan users/ --limit 50 --cursor "<token>"
```

### `vertex count <prefix>`
Print the number of keys in the prefix index as a single integer.
**Caveat:** counted from the radix index, not cross-checked for liveness, so it
may include expired-but-not-yet-reaped keys. For a strictly-live count use
`lantern vertex scan <prefix> --all | wc -l`.
```shell
lantern vertex count users/
```

### `keys <prefix> [limit]`
List vertex **keys** under `<prefix>`, one per line on stdout — the
Redis-familiar, keys-only counterpart to `vertex scan` (no values, pipe-friendly).
Lantern is prefix-indexed, so the argument is a key **prefix**, not a glob (no
trailing `*`). A prefix is **required** (the server rejects an empty prefix); the
optional `<limit>` caps the page (mirrors `scan vertices`). This is a verb-first
one-liner — the same grammar as the `lantern repl` prompt, backed by the
wire-efficient `ScanVertexKeys` RPC.
```shell
lantern keys users/
lantern keys users/ 100
lantern keys users/ | xargs -n1 lantern vertex get   # hydrate values
```

### `vertex delete-prefix <prefix>` (DESTRUCTIVE)
Bulk-delete every live vertex under `<prefix>`, up to `--limit` per call.
**Safety gate:** running without `--dry-run` or `--yes` is **refused** — it
prints the current match count + suggested next steps to stderr and exits
non-zero. Flags: `--dry-run` (count only, mutates nothing), `--yes` (perform
delete), `--limit <n>` (cap per call). Prints `would delete <n>` / `deleted <n>`.
Incident edges are not eagerly removed (lazy GC).
```shell
lantern vertex delete-prefix tmp/             # refused → prints suggestion
lantern vertex delete-prefix tmp/ --dry-run
lantern vertex delete-prefix tmp/ --yes
lantern vertex delete-prefix tmp/ --yes --limit 500
```

## Edge commands

### `edge get <tail> <head>`
Fetch one edge. Prints `{tail, head, weight, expiration}`. `NotFound` (exit 2)
when the edge never existed, was deleted, or expired.
```shell
lantern edge get alice bob
lantern edge get alice bob | jq .weight
```

### `edge add <tail> <head> <weight>`  (additive)
Sum `<weight>` onto `(tail, head)`. Flag `--ttl <dur>` resets the edge's
expiration to `now+ttl` each call (default permanent). Weight is `float32`;
`NaN`/`±Inf` are rejected. Prints `OK`.
```shell
lantern edge add alice bob 1.5            # weight 1.5
lantern edge add alice bob 0.5            # weight now 2.0
lantern edge add alice bob 0.1 --ttl 30m  # weight 2.1, TTL reset to 30m
```

### `edge put <tail> <head> <weight>`  (idempotent)
Replace the `(tail, head)` weight. Flag `--ttl <dur>` (default permanent).
Prints `OK`.
```shell
lantern edge put alice bob 1.5            # weight 1.5
lantern edge put alice bob 0.5            # weight 0.5 (overwritten)
```

### `edge delete <tail> <head> | <tail>:<head> [...]`
Delete edges. Two input shapes:
1. **Positional pair** — exactly two args, neither containing the separator:
   `lantern edge delete alice bob`.
2. **Separated pairs** — any arg contains the separator (`:` by default) →
   batch `DeleteEdges` (chunked at `--chunk-size`). Override the delimiter with
   `--separator '|'` when an endpoint itself contains a colon.

Idempotent. Single → `OK existed=true|false`; batch → `OK <n>`.
```shell
lantern edge delete alice bob                     # positional single
lantern edge delete alice:bob bob:carol carol:dave # batch
lantern edge delete --separator '|' 'a:b|c:d'      # custom delimiter
jq -r '"\(.tail):\(.head)"' edges.json | xargs lantern edge delete
```

### `edge scan`
Enumerate live edges in ascending `(tail, head)` order as NDJSON. Flags:
`--tail-prefix <p>`, `--head-prefix <p>` (either may be omitted; omitting both
scans all edges), `--limit <n>`, `--cursor <token>`, `--all`. Cursor handling
matches `vertex scan` (token to stderr without `--all`). Edge cursors are
**not** interchangeable with vertex-scan cursors. A head-only scan still
iterates every tail (no global reverse index), so combining both prefixes is
the most efficient shape.
```shell
lantern edge scan --tail-prefix user:
lantern edge scan --tail-prefix user: --head-prefix post: --limit 100
lantern edge scan --head-prefix post: --all > posts.ndjson
```

## `illuminate <seed>` — graph walk

Run a bounded breadth-first walk from `<seed>` and emit the visited subgraph as
indented JSON `{vertices: {...}, edges: {tail: {head: weight}}}`. Read-only and
idempotent. Flags:

| Flag | Default | Meaning |
|---|---|---|
| `--step <uint32>` | `1` | max walk depth from the seed |
| `--k <uint32>` | `10` | max neighbours visited per node (top-k) |
| `--algorithm <mode>` | `none` | post-traversal reduction: `none` (raw subgraph), `mst` (spanning tree), `spt` (shortest-path tree rooted at seed) |
| `--objective <dir>` | `max` | optimisation direction; governs **both** per-hop top-k pruning and the reduction: `max` (largest-weight wins; weight = relevance) or `min` (smallest-weight wins; weight = cost) |
| `--weighting <mode>` | `raw` | edge-weight transform before the walk: `raw` or `tfidf` (re-score by TF-IDF over per-vertex out-edge distribution) |
| `--prefix <string>` | — | restrict the walk **frontier** to vertices with this key prefix (case-sensitive); the seed is always kept as anchor. Applied server-side before top-k and any reduction, so `--prefix` + `mst`/`spt` yields a tree over the prefix-induced subgraph, not a path in the full graph |

```shell
lantern illuminate alice                                   # raw 1-hop, top-10 by weight
lantern illuminate alice --step 2 --k 5 --weighting tfidf  # 2-hop, TF-IDF re-rank
lantern illuminate alice --step 3 --k 20 --algorithm mst --objective min  # min-weight spanning tree
lantern illuminate alice --step 3 --k 20 --algorithm spt --objective max  # relevance-weighted SPT
lantern illuminate alice --step 2 --k 5 --prefix users/    # frontier restricted to users/
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
lantern bulk vertices vertices.ndjson

# edges add (additive): {"tail":"alice","head":"bob","weight":1.5,"ttl":"1h"}
cat edges.ndjson | lantern bulk edges add -

# edges put (idempotent)
lantern bulk edges put edges.ndjson --chunk-size 5000
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
# Snapshot a keyspace to NDJSON, then re-load it elsewhere
lantern vertex scan users/ --all > users.ndjson
lantern --address other-host:6380 bulk vertices users.ndjson

# Count then safely purge a temporary keyspace
lantern vertex count tmp/
lantern vertex delete-prefix tmp/ --dry-run
lantern vertex delete-prefix tmp/ --yes

# Accumulate an interaction signal with decay, then inspect the live sum
lantern edge add userA itemX 1 --ttl 24h
lantern edge add userA itemX 1 --ttl 24h
lantern edge get userA itemX | jq .weight     # → 2

# Walk a relevance graph against a non-default replica over TLS
lantern --tls --tls-ca ./ca.pem -H lantern.example.com -p 443 \
  illuminate userA --step 2 --k 10 --weighting tfidf
```

## Gotchas checklist

- Choosing `edge add` vs `edge put` wrong silently corrupts weights — `add`
  accumulates, `put` overwrites.
- `vertex count` can over-report vs. live reality; use `scan --all | wc -l` for
  an exact live count.
- `delete-prefix` without `--dry-run`/`--yes` is intentionally refused — pass
  one explicitly.
- Vertex deletes leave incident edges to decay on their own TTL.
- Scan cursors are opaque and per-(prefix, server version); vertex and edge
  cursors are not interchangeable.
- JSON object/array vertex values round-trip as compact JSON **strings**, not
  nested structures.
- `NotFound` surfaces as **exit code 2**, distinct from local errors (exit 1) —
  use it to tell "missing" from "broken".
