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
import { Lantern, Algorithm } from "lantern-sdk";

const client = Lantern.connect("http://localhost:6380");
try {
  await client.putVertex({ key: "hello", value: "world", ttlSeconds: 60 });

  const v = await client.getVertex("hello");
  console.log(v.key, v.value); // "hello" "world"

  await client.addEdge({ tail: "hello", head: "world", weight: 1.0, ttlSeconds: 60 });

  const graph = await client.illuminate("hello", {
    step: 2,
    k: 16,
    algorithm: Algorithm.UNSPECIFIED,
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

`scanVerticesAll` and `scanEdgesAll` are async iterables that page through
results until the server returns an empty cursor.

```ts
for await (const page of client.scanVerticesAll("user:", 500)) {
  for (const v of page) console.log(v.key);
}
```

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
import { Lantern } from "lantern-sdk";

const client = Lantern.connect("https://lantern.example.com:6380", {
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

## License

Apache-2.0 — see [LICENSE](./LICENSE).
