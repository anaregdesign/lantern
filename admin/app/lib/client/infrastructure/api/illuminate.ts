import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkEdgeToFlat, sdkVertexToFlat } from "./to-flat";
import type { Graph, IlluminateResponse } from "./types";
import {
  Algorithm as SdkAlgorithm,
  Objective as SdkObjective,
  Weighting as SdkWeighting,
} from "lantern-sdk/web";

export type { Edge, Graph, IlluminateResponse, Vertex } from "./types";

// Per #410 the wire schema carries three orthogonal axes (algorithm
// × objective × weighting); the admin UI tracks each as a stable
// string so router serialisation and Playwright fixtures keep one
// human-readable vocabulary across surfaces.
export type Algorithm =
  | "ALGORITHM_UNSPECIFIED"
  | "ALGORITHM_MINIMUM_SPANNING_TREE"
  | "ALGORITHM_SHORTEST_PATH_TREE";

export type Objective =
  | "OBJECTIVE_UNSPECIFIED"
  | "OBJECTIVE_MINIMIZE"
  | "OBJECTIVE_MAXIMIZE";

export type Weighting =
  | "WEIGHTING_UNSPECIFIED"
  | "WEIGHTING_RAW"
  | "WEIGHTING_TFIDF"
  | "WEIGHTING_BM25";

export interface IlluminateRequest {
  seed: string;
  step?: number;
  k?: number;
  algorithm?: Algorithm;
  objective?: Objective;
  weighting?: Weighting;
  /**
   * Restrict the traversal frontier to vertices whose key has this prefix
   * (#606). The seed is always retained as the anchor even if it does not
   * match. Empty/omitted = no filter. Applied server-side BEFORE per-hop
   * top-k and before any MST/SPT reduction (induced-subgraph semantics).
   */
  vertexPrefix?: string;
}

const ALGORITHM_TO_SDK: Record<Algorithm, SdkAlgorithm> = {
  ALGORITHM_UNSPECIFIED: SdkAlgorithm.UNSPECIFIED,
  ALGORITHM_MINIMUM_SPANNING_TREE: SdkAlgorithm.MINIMUM_SPANNING_TREE,
  ALGORITHM_SHORTEST_PATH_TREE: SdkAlgorithm.SHORTEST_PATH_TREE,
};
const OBJECTIVE_TO_SDK: Record<Objective, SdkObjective> = {
  OBJECTIVE_UNSPECIFIED: SdkObjective.UNSPECIFIED,
  OBJECTIVE_MINIMIZE: SdkObjective.MINIMIZE,
  OBJECTIVE_MAXIMIZE: SdkObjective.MAXIMIZE,
};
const WEIGHTING_TO_SDK: Record<Weighting, SdkWeighting> = {
  WEIGHTING_UNSPECIFIED: SdkWeighting.UNSPECIFIED,
  WEIGHTING_RAW: SdkWeighting.RAW,
  WEIGHTING_TFIDF: SdkWeighting.TFIDF,
  WEIGHTING_BM25: SdkWeighting.BM25,
};

/**
 * Calls `LanternService.Illuminate` via `lantern-sdk/web`.
 *
 * Runs a k-bounded BFS from the supplied seed. Optional knobs (step
 * / k / algorithm / objective / weighting) default server-side when
 * omitted. The SDK returns a rich-shape Graph with `Map<>` values;
 * this adapter flattens it back to admin's array-of-flat-JSON shape
 * so the existing canvas + table consumers keep working unchanged
 * (#409, #410).
 */
export async function illuminate(
  client: LanternClient,
  request: IlluminateRequest,
  init?: { signal?: AbortSignal },
): Promise<IlluminateResponse> {
  if (request.seed === "") {
    throw new Error("illuminate: seed must be non-empty");
  }
  try {
    const sdkGraph = await client.illuminate(
      request.seed,
      {
        step: request.step ?? 0,
        k: request.k ?? 0,
        algorithm:
          request.algorithm !== undefined
            ? ALGORITHM_TO_SDK[request.algorithm]
            : undefined,
        objective:
          request.objective !== undefined
            ? OBJECTIVE_TO_SDK[request.objective]
            : undefined,
        weighting:
          request.weighting !== undefined
            ? WEIGHTING_TO_SDK[request.weighting]
            : undefined,
        vertexPrefix: request.vertexPrefix ?? "",
      },
      init?.signal,
    );
    const graph: Graph = {
      vertices: Array.from(sdkGraph.vertices.values()).map(sdkVertexToFlat),
      edges: [],
    };
    for (const [tail, heads] of sdkGraph.edges) {
      for (const [head, weight] of heads) {
        graph.edges!.push(
          sdkEdgeToFlat({ tail, head, weight, expiration: null }),
        );
      }
    }
    return { graph };
  } catch (err) {
    throw LanternApiError.fromUnknown("Illuminate", err);
  }
}
