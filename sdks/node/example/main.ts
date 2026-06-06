import { Lantern, Algorithm, Objective, Weighting } from "lantern-sdk";

async function main(): Promise<void> {
  const client = Lantern.connect("localhost:6380");

  try {
    // PutVertex — value can be string, number, bigint, boolean, Date,
    // Uint8Array, null, or a typed wrapper (Int32/Uint32/Uint64/Float32/Duration).
    await client.putVertex("string", "A", { ttlSeconds: 60 });
    await client.putVertex("int", 1, { ttlSeconds: 60 });
    await client.putVertex("float", 1.1, { ttlSeconds: 60 });
    await client.putVertex("bool", true, { ttlSeconds: 60 });
    await client.putVertex("time", new Date(), { ttlSeconds: 60 });
    await client.putVertex("bytes", new Uint8Array([0x41]), { ttlSeconds: 60 });
    await client.putVertex("nil", null, { ttlSeconds: 60 });

    // GetVertex
    const v = await client.getVertex("string");
    console.log(`${v.key}: kind=${v.kind} value=${String(v.value)}`);

    // Edges
    await client.addEdge("string", "int", 1.0, { ttlSeconds: 60 });
    await client.addEdge("string", "float", 1.0, { ttlSeconds: 60 });
    await client.addEdge("int", "bool", 1.0, { ttlSeconds: 60 });

    // Illuminate — neighborhood graph traversal. The three orthogonal
    // axes (algorithm × objective × weighting) replaced the legacy
    // `tfidf` flag + `optimization` enum in #410. UNSPECIFIED on every
    // axis defers to the server's defaults (raw subgraph, minimise,
    // raw weighting).
    const graph = await client.illuminate("string", {
      step: 2,
      k: 16,
      algorithm: Algorithm.UNSPECIFIED,
      objective: Objective.UNSPECIFIED,
      weighting: Weighting.UNSPECIFIED,
    });
    console.log(`vertices: ${graph.vertices.size}`);
    for (const [tail, row] of graph.edges) {
      for (const [head, weight] of row) {
        console.log(`edge ${tail} -> ${head} = ${weight}`);
      }
    }

    // Prefix count
    const count = await client.countVerticesByPrefix("");
    console.log(`total vertices: ${count}`);
  } finally {
    client.close();
  }
}

main().catch((err) => {
  console.error(err);
  process.exit(1);
});
