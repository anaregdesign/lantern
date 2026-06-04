# lantern-client (Node.js / TypeScript)

Official Node.js / TypeScript client for [Lantern](https://github.com/anaregdesign/lantern) —
an in-memory graph KVS with prefix scan, neighborhood traversal (`Illuminate`), and TTL.

- Transport: gRPC over `@grpc/grpc-js`
- Module formats: ESM + CJS, with full TypeScript `.d.ts`
- Node.js: 20+

## Install

```bash
npm install lantern-client
# or
bun add lantern-client
# or
pnpm add lantern-client
```

## Quick Start

```ts
import { Lantern, Optimization } from "lantern-client";

const client = Lantern.connect("localhost:6380");
try {
  await client.putVertex("hello", "world", { ttlSeconds: 60 });

  const v = await client.getVertex("hello");
  console.log(v.key, v.value); // "hello" "world"

  await client.addEdge("hello", "world", 1.0, { ttlSeconds: 60 });

  const graph = await client.illuminate("hello", {
    step: 2,
    k: 16,
    optimization: Optimization.UNSPECIFIED,
  });
  console.log(`vertices=${graph.vertices.size}`);
} finally {
  client.close();
}
```

## Multiple endpoints (client-side round-robin)

```ts
const client = Lantern.connectEndpoints(["node-a:6380", "node-b:6380"]);
```

Endpoints are wrapped behind grpc-js's `ipv4:` resolver with a `round_robin`
load-balancing policy.

## Vertex values

`putVertex` accepts these JavaScript types and maps each to a typed proto
oneof field:

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
| `Duration({seconds, nanos})`           | `duration`                         |

Use the typed wrappers from `lantern-client` when you need a narrower numeric
type than `number` / `bigint` would infer.

## Batch APIs

`putVertices`, `deleteVertices`, `addEdges`, `putEdges`, and `deleteEdges`
split inputs into chunks (default 1000, override via `chunkSize`). On a
chunk failure the call throws `BatchError`, which carries `.written`
(or `.processed`) — the count of items successfully committed before the
error — and the underlying `cause`.

```ts
import { BatchError } from "lantern-client";

try {
  await client.putEdges(edges, { chunkSize: 500 });
} catch (err) {
  if (err instanceof BatchError) {
    console.warn(`committed ${err.written} before: ${err.cause}`);
  } else {
    throw err;
  }
}
```

## Streaming-like pagination

`scanVerticesAll` and `scanEdgesAll` are async iterables that page through
results until the server returns an empty cursor.

```ts
for await (const page of client.scanVerticesAll("user:", { limit: 500 })) {
  for (const v of page) console.log(v.key);
}
```

## Cancellation

Every method accepts an optional `AbortSignal` (passed as `{ signal }` on
batch/prefix methods, or as a positional arg on single-key methods). Aborting
the signal sets a gRPC deadline and cancels the in-flight call.

```ts
const ctrl = new AbortController();
setTimeout(() => ctrl.abort(), 500);
await client.getVertex("slow-key", ctrl.signal);
```

## Retry policy

The client ships a default gRPC service config enabling `round_robin` LB and
retry-on-`UNAVAILABLE`/`RESOURCE_EXHAUSTED` for all RPCs **except** `AddEdge`
and `AddEdges`, which mutate accumulating state and must not be retried.

Override with `Lantern.connect(target, { serviceConfig })`.

## License

Apache-2.0 — see [LICENSE](./LICENSE).
