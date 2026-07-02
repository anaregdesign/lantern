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
committed before the error — and the underlying `cause`.

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

## Streaming-like pagination

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

## Content search

`searchVertices` runs a relevance-ranked full-text query over vertex
_content_ (key + value) — unlike `scanVertices`, which is a lexicographic
key-prefix walk. It returns `{ key, score }` hits in descending BM25 order:
the seed candidates to pick before an `illuminate` traversal, where `score`
doubles as the seed's initial weight. Hits carry only the key and score, so
hydrate value/TTL with a follow-up `getVertices`, preserving rank order.

```ts
const hits = await client.searchVertices("quarterly revenue", { limit: 10, prefix: "doc/" });
const { found } = await client.getVertices(hits.map((h) => h.key));
```

An empty or unmatched query resolves to `[]` (not an error). When the
server's index is disabled (`LANTERN_SEARCH_ENABLED=false`) the call rejects
with `FailedPreconditionError`, so callers can render a calm "search not
enabled" state instead of treating it as a hard failure.

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
  bfs: { reduction: Reduction.SHORTEST_PATH_TREE },
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
