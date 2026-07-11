import { Objective, Reduction, Weighting, connect, type Graph } from "lantern-sdk";

function printGraph(name: string, graph: Graph): void {
  console.log(`${name}: vertices=${graph.vertices.size}`);
  for (const [tail, row] of graph.edges) {
    for (const [head, weight] of row) {
      console.log(`${name}: edge ${tail} -> ${head} = ${weight}`);
    }
  }
}

async function main(): Promise<void> {
  const client = connect("http://localhost:6380");

  try {
    // PutVertex — value can be string, number, bigint, boolean, Date,
    // Uint8Array, null, or a typed wrapper (Int32/Uint32/Uint64/Float32/Duration).
    await client.putVertex({ key: "string", value: "A", ttlSeconds: 60 });
    await client.putVertex({ key: "int", value: 1, ttlSeconds: 60 });
    await client.putVertex({ key: "float", value: 1.1, ttlSeconds: 60 });
    await client.putVertex({ key: "bool", value: true, ttlSeconds: 60 });
    await client.putVertex({ key: "time", value: new Date(), ttlSeconds: 60 });
    await client.putVertex({ key: "bytes", value: new Uint8Array([0x41]), ttlSeconds: 60 });
    await client.putVertex({ key: "nil", value: null, ttlSeconds: 60 });

    // GetVertex
    const v = await client.getVertex("string");
    console.log(`${v.key}: kind=${v.kind} value=${String(v.value)}`);

    // Edges
    await client.addEdge({ tail: "string", head: "int", weight: 1.0, ttlSeconds: 60 });
    await client.addEdge({ tail: "string", head: "float", weight: 1.0, ttlSeconds: 60 });
    await client.addEdge({ tail: "int", head: "bool", weight: 1.0, ttlSeconds: 60 });

    // Illuminate selects exactly one traversal family. BFS supports the
    // per-hop controls and optional tree rendering; the shared weighting is
    // applied before the family walk.
    const bfs = await client.illuminate("string", {
      bfs: {
        step: 2,
        fanOut: 16,
        reduction: Reduction.SHORTEST_PATH_TREE,
        objective: Objective.MAXIMIZE,
      },
      weighting: Weighting.UNSPECIFIED,
    });
    printGraph("bfs", bfs);

    // Personalized PageRank is a relevance star around the seed.
    const pagerank = await client.illuminate("string", {
      ppr: { topN: 16, restartProb: 0.15, epsilon: 0.0001 },
      weighting: Weighting.TFIDF,
    });
    printGraph("pagerank", pagerank);

    // Local community returns the induced subgraph selected by a
    // conductance sweep, with an optional tree view of those members.
    const community = await client.illuminate("string", {
      community: {
        maxSize: 16,
        restartProb: 0.15,
        epsilon: 0.0001,
        reduction: Reduction.MINIMUM_SPANNING_TREE,
        objective: Objective.MINIMIZE,
      },
      weighting: Weighting.BM25,
    });
    printGraph("community", community);

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
