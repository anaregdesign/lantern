# lantern-sdk (Node.js / TypeScript)

Official Node.js / TypeScript client for [Lantern](https://github.com/anaregdesign/lantern) —
an in-memory graph KVS with prefix scan, neighborhood traversal (`Illuminate`), and TTL.

- Transport: `@connectrpc/connect-node` v2 (Connect protocol over HTTP/2)
- Module formats: ESM + CJS, with full TypeScript `.d.ts`
- Node.js: 20+

## Install

```bash
npm install lantern-sdk
# or
bun add lantern-sdk
# or
pnpm add lantern-sdk
```

## Quick start

The Lantern server's primary `:6380` listener speaks Connect, gRPC,
and gRPC-Web on the same h2c socket, so this client points at the
server URL with an `http://` (or `https://` for TLS) scheme.

```ts
import { connect } from "lantern-sdk";

const client = connect("http://localhost:6380");
try {
  await client.putVertex({ key: "hello", value: "world", ttlSeconds: 60 });

  const v = await client.getVertex("hello");
  console.log(v.key, v.value); // "hello" "world"

  await client.addEdge({ tail: "hello", head: "world", weight: 1.0, ttlSeconds: 60 });

  const graph = await client.illuminate("hello", {
    bfs: { step: 2, fanOut: 16 },
  });
  console.log(`vertices=${graph.vertices.size}`);
} finally {
  client.close();
}
```

`illuminate()` requires exactly one options arm: `bfs`, `ppr`, or
`community`. BFS requires positive `step` and `fanOut`; missing/conflicting
families and zero BFS dimensions raise `InvalidArgumentError` before any RPC.
`ppr.topN = 0` and `community.maxSize = 0` remain their families' explicit
sentinels.

## Vertex values

Each method on `Lantern` accepts these JavaScript types and maps each
to a typed proto oneof field:

| JS / TS input                          | Proto field                        |
| -------------------------------------- | ---------------------------------- |
| `string`                               | `string`                           |
| `number` (integer, fits int64)         | `int64`                            |
| `number` (fractional or non-int range) | `float64`                          |
| `bigint`                               | `int64`/`uint64` (sign-dispatched) |
| `boolean`                              | `bool`                             |
| `Date`                                 | `timestamp`                        |
| `Uint8Array` / `Buffer`                | `bytes`                            |
| `null`                                 | `nil`                              |
| `Int32(n)` / `Uint32(n)` / `Uint64(n)` | `int32` / `uint32` / `uint64`      |
| `Float32(n)`                           | `float32`                          |
| `Duration(seconds, nanos)`             | `duration`                         |

Use the typed wrappers from `lantern-sdk` when you need a narrower numeric
type than `number` / `bigint` would infer.

## Batch APIs

`putVertices`, `deleteVertices`, `addEdges`, `putEdges`, and `deleteEdges`
split inputs into chunks (default 1000, override via
`ConnectOptions.batchChunkSize`). On a chunk failure the call throws
`BatchError`, which carries `.written` — the count of items successfully
committed before the error — and the underlying `cause`. Resume with
`inputs.slice(err.written)`. A full retry from index 0 is safe for the
idempotent batch ops (`putVertices`, `putEdges`, `deleteVertices`,
`deleteEdges`) but **not** for a plain `addEdges`, whose already-applied
prefix would be double-counted — attach contrib ids (see
[Idempotent additive edges](#idempotent-additive-edges)) to make `addEdges`
retries safe too.

```ts
import { BatchError } from "lantern-sdk";

try {
  await client.putEdges(edges);
} catch (err) {
  if (err instanceof BatchError) {
    console.warn(`committed ${err.written} before: ${err.cause}`);
  } else {
    throw err;
  }
}
```

## Conditional writes (SET NX)

`putVertexIfAbsent` / `putVerticesIfAbsent` apply a write only when **no live
vertex already exists** at the key — the Redis `SET NX` pattern (#896). They
close the check-then-act race of a `getVertex` → `putVertex` sequence: the
server performs the existence check and the store atomically, so two callers
racing to create the same marker can't both win.

`putVertexIfAbsent` resolves to a `boolean` (`true` when the write landed,
`false` when a live vertex was already there and left untouched).
`putVerticesIfAbsent` resolves to `{ written, skippedKeys }`, where `written`
counts the vertices actually stored and `skippedKeys` lists the keys skipped
because a live vertex already existed.

```ts
// Enqueue-dedup marker: only the first caller proceeds.
const first = await client.putVertexIfAbsent({
  key: "job:discover:user42",
  value: true,
  ttlSeconds: 300,
});
if (first) {
  // we own the marker — enqueue the background job
}

const { written, skippedKeys } = await client.putVerticesIfAbsent([
  { key: "settings:a", value: "default-a" },
  { key: "settings:b", value: "default-b" },
]);
```

"Live" follows the server's live-visibility rule, so an expired-but-uncollected
vertex does not block the write. Under leaderless replication two concurrent
`ifAbsent` writes on different nodes can both report success locally before
converging (the same caveat as Redis `SETNX` with async replicas).

## Idempotent additive edges

`addEdge` / `addEdges` are **additive** — the server sums each contribution
into the edge's weight — so a transport retry that re-sends the same edge
double-counts its weight. Attach a 24-byte **contrib ID** to make a
contribution idempotent: while that contribution is live, re-adding it with
the same id is a no-op instead of adding weight again.

Both resolve to the **post-accumulation effective weight**: `addEdge` returns
the edge's new live total and `addEdges` returns an index-aligned `number[]`,
so you can keep a running counter (an additive `+1` write, then read back the
total) without a follow-up `getEdge`. The value is the serving node's live
view; a replication peer holding un-streamed contributions may differ briefly.

```ts
const total = await client.addEdge({ tail: "a", head: "b", weight: 1 });
// total is the accumulated weight after this contribution landed.
```

Two ways to get an id onto the wire:

```ts
// 1. Opt-in automatic ids: the client mints one per contribution from a
//    per-client random nonce + a monotonic sequence, so a retried call
//    re-sends identical bytes.
const client = connect("http://localhost:6380", {
  options: { idempotentAdds: true },
});
await client.addEdge({ tail: "a", head: "b", weight: 1 });

// 2. Caller-supplied deterministic ids: control the dedup key yourself.
//    Must be exactly CONTRIB_ID_BYTES (24) bytes; a caller id always wins
//    over the automatic one.
import { CONTRIB_ID_BYTES } from "lantern-sdk";
const contribId = new Uint8Array(CONTRIB_ID_BYTES); // fill deterministically
await client.addEdge({ tail: "a", head: "b", weight: 1, contribId });
```

For `addEdges`, ids stay index-aligned with `edges` even when the batch is
split across chunks. **Dedup horizon:** dedup only holds while the
contribution is live — once the edge decays past its TTL (or is deleted) the
id is forgotten, so a later add with the same id contributes weight again.
Contrib IDs guard retries within a contribution's lifetime, not for all time.

`scanVerticesAll`, `scanEdgesAll`, and `scanVertexKeysAll` are async iterables
that page through results until the server returns an empty cursor.

```ts
for await (const page of client.scanVerticesAll("user:", 500)) {
  for (const v of page) console.log(v.key);
}
```

`scanVertexKeys` / `scanVertexKeysAll` are the keys-only, wire-efficient
counterparts to `scanVertices` — they return just the matching vertex keys
(no values), backing the Redis-familiar `keys` CLI verb. A non-empty prefix
is required.

All four scans walk a prefix ascending by default; pass `order: "desc"` in the
scan options to walk it descending (#898). The order is bound into the cursor,
so replaying a cursor under the opposite order is rejected with
`invalid_argument` — carry the same `order` through a paginated loop. The
`*All` async iterables take `order` as a trailing argument, e.g.
`client.scanVerticesAll("user:", 500, undefined, "desc")`.

### Prefix bulk-delete

`deleteVerticesByPrefix(prefix, opts?)` and `deleteEdgesByPrefix(opts?)` remove
a whole namespace in one call, returning the count deleted as a `bigint`. Both
accept `dryRun: true` to preview the count without mutating, and a `limit` that
caps a single call (loop until the returned count drops below `limit` to drain
a set larger than the server's per-call cap).

`deleteEdgesByPrefix` matches the tail∩head intersection of `tailPrefix` and
`headPrefix` (the edge-shaped sibling of the scan filter). At least one prefix
must be non-empty — a both-empty request rejects with `InvalidArgumentError`,
so a whole-graph edge wipe is always explicitly scoped.

```ts
// Preview, then delete every edge from a user into the session namespace.
const would = await client.deleteEdgesByPrefix({
  tailPrefix: "user:",
  headPrefix: "session:",
  dryRun: true,
});
if (would > 0n) {
  await client.deleteEdgesByPrefix({ tailPrefix: "user:", headPrefix: "session:" });
}
```

## Decaying edges

`addDecayingEdge(tail, head, opts)` accumulates a single edge whose **live
(summed) weight decays geometrically over time** — starting at
`opts.initialWeight`, multiplied by `opts.ratio` every `opts.intervalSeconds`,
reaching zero after `opts.steps` intervals. It expands the curve into a
telescoping set of additive contributions with staggered absolute expirations
and applies them in one `addEdges` batch (so it inherits contrib-id dedup when
`idempotentAdds` is on), returning the edge's effective weight right after the
add.

```ts
// weight ≈ 16 now, ~8 after 1h, ~4 after 2h, … gone after 5h.
const live = await client.addDecayingEdge("user:42", "topic:ml", {
  initialWeight: 16,
  ratio: 0.5,
  steps: 5,
  intervalSeconds: 3600,
});
```

Prefer a half-life to hand-tuned steps? `halfLifeDecay(initialWeight,
halfLifeSeconds, intervalSeconds, horizonSeconds)` builds the `DecayOptions` for
you (clamped to `MAX_DECAY_STEPS` = 16):

```ts
import { halfLifeDecay } from "lantern-sdk";
// Halve every 24h, sampled hourly, out to a 7-day horizon.
const opts = halfLifeDecay(10, 86_400, 3_600, 604_800);
await client.addDecayingEdge("a", "b", opts);
```

The whole curve is one `addEdges` request, so the server validates every
contribution's expiration before applying any: a horizon longer than the
server's `LANTERN_TOMBSTONE_TTL` rejects the entire add. A curve that underflows
float32 to zero, or ill-formed `opts`, rejects with `InvalidArgumentError`. The
pure `decayContributions(tail, head, opts, baseMs)` helper is exported too, if
you want the `EdgeInput[]` without sending it.

## Content search

`searchVertices` runs a relevance-ranked full-text query over vertex
_content_ (key + value) — unlike `scanVertices`, which is a lexicographic
key-prefix walk. It returns `{ key, score }` hits in stable `(score DESC, raw
key ASC)` order: the seed candidates to pick before an `illuminate` traversal,
where `score` doubles as the seed's initial weight. Hits carry only the key and
score, so hydrate value/TTL with a follow-up `getVertices`, preserving rank
order.

```ts
const hits = await client.searchVertices("quarterly revenue", { limit: 10, prefix: "doc/" });
const { found } = await client.getVertices(hits.map((h) => h.key));
```

An empty or unmatched query resolves to `[]` (not an error). When the
server's index is disabled (`LANTERN_SEARCH_ENABLED=false`) the call rejects
with `FailedPreconditionError` whose `reason` is
`SearchErrorReason.SEARCH_DISABLED`, so callers can render a calm "search not
enabled" state without parsing text. A phrase query sent to a server without
positional postings is distinct:
`SearchErrorReason.SEARCH_POSITIONS_DISABLED`. `getServerStatus().search`
lets clients discover positions, defaults, limits, implementation versions,
and the HA configuration fingerprint before issuing a query.

## Backup & restore

`backup(opts?)` streams a whole-graph, point-in-time dump as an **async
iterable** of `BackupRecord`s — every live vertex, then every folded live edge.
Each record is decoded kind-preservingly, so narrow numeric value types
(int32/uint32/uint64/float32/duration) round-trip through `restore` exactly.
`restore(source, opts?)` replays any (sync or async) iterable of records back
through the batch `putVertices` / `putEdges` surface and returns the counts
loaded.

```ts
// Dump → NDJSON file.
import { createWriteStream } from "node:fs";
const out = createWriteStream("graph.ndjson");
for await (const rec of client.backup()) {
  out.write(backupRecordToNdjson(rec) + "\n");
}

// NDJSON file → restore.
import { createReadStream } from "node:fs";
import { createInterface } from "node:readline";
async function* records() {
  const rl = createInterface({ input: createReadStream("graph.ndjson") });
  for await (const line of rl) if (line) yield backupRecordFromNdjson(line);
}
const stats = await client.restore(records()); // { vertices, edges }
```

Pass `{ prefix }` to scope a backup to a key namespace, and `{ chunkSize }`
(default 1000) to tune restore batch size. The `backupRecordToNdjson` /
`backupRecordFromNdjson` line codec is a **Node-native** format (a
`"kind"`-discriminated JSON object per line; int64/uint64 magnitudes as strings
so values above 2^53 survive) — it is safe and lossless for Node→Node dumps,
but is not interchangeable with the Go SDK's `FormatNDJSON`. Restore is not
transactional — a mid-stream failure leaves already-flushed batches in place;
re-running is safe because `Put` is idempotent.

## Ping / health check

`ping(signal?)` probes server readiness via the gRPC-Health-v1 `Health/Check`
that rides the primary listener. It resolves when the server reports SERVING,
throws `HealthStatusError` (with the reported `.status`) on any other status,
and a generic `LanternError` on transport / non-200 / decode failure.

```ts
try {
  await client.ping();
} catch (err) {
  if (err instanceof HealthStatusError) console.warn("not serving:", err.status);
  else throw err;
}
```

`ping` needs the server's base URL, so it works on clients built via `connect()`
/ `connectWeb()` (or a `withTransport` client given a `baseUrl`); a
transport-only client rejects with `InvalidArgumentError`. The client's
`defaultTimeoutMs`, if set, bounds the probe.

## Cancellation

Every method accepts an optional `AbortSignal` as the trailing arg.
Aborting the signal cancels the in-flight Connect call.

```ts
const ctrl = new AbortController();
setTimeout(() => ctrl.abort(), 500);
await client.getVertex("slow-key", ctrl.signal);
```

## Transport tuning

Override the Connect-Node transport options via `transportOptions`:

```ts
import { connect } from "lantern-sdk";

const client = connect("https://lantern.example.com:6380", {
  transportOptions: {
    useBinaryFormat: true, // flip from Connect/JSON to Connect/protobuf
    httpVersion: "2",
  },
  // Custom Connect interceptors run on every unary call.
  interceptors: [
    (next) => async (req) => {
      req.header.set("x-trace-id", crypto.randomUUID());
      return next(req);
    },
  ],
});
```

## Browser entrypoint (`lantern-sdk/web`)

The package also exports a browser-flavoured entrypoint that swaps the
Node `http2` transport for a `fetch`-based one from
`@connectrpc/connect-web`. Bundlers that follow the `package.json#exports`
map (Vite, Webpack 5+, Rollup, esbuild) will route
`import { connectWeb } from "lantern-sdk/web"` to a bundle that excludes
`@connectrpc/connect-node` entirely — verified by the `bundle-isolation`
test in `test/bundle-isolation.test.ts`.

```ts
import { connectWeb, Reduction } from "lantern-sdk/web";

const client = connectWeb("https://lantern.example.com:6380");
const graph = await client.illuminate("hello", {
  bfs: { step: 2, fanOut: 16, reduction: Reduction.SHORTEST_PATH_TREE },
});
console.log(`vertices=${graph.vertices.size}`);
```

The browser entrypoint exposes the same `Lantern` class, value enums,
error hierarchy, and option types as the Node entrypoint; only the
transport differs. CORS preflights must be allowed on the Lantern
server via `LANTERN_CORS_ALLOWED_ORIGINS`.

## High availability

Both `connect` and `connectWeb` dial a **single** endpoint. Unlike the Go
SDK — which since #592 offers opt-in `NewLanternFailover([]string{...})`
over a fixed endpoint set — this Node client has **no** failover
counterpart yet. Front a multi-replica deployment with a reverse proxy,
k8s Service / Ingress, or DNS round-robin and point the client at that
single URL; see [docs/ha-runbook.md](../../docs/ha-runbook.md).

## License

Apache-2.0 — see [LICENSE](./LICENSE).
