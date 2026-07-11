import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { sdkEdgeToFlat, sdkVertexToFlat } from "./to-flat";
import type { Graph, IlluminateResponse } from "./types";
import {
  Objective as SdkObjective,
  Reduction as SdkReduction,
  Weighting as SdkWeighting,
  type IlluminateOptions as SdkIlluminateOptions,
} from "lantern-sdk/web";

export type { Edge, Graph, IlluminateResponse, Vertex } from "./types";

export type Reduction =
  | "REDUCTION_UNSPECIFIED"
  | "REDUCTION_MINIMUM_SPANNING_TREE"
  | "REDUCTION_SHORTEST_PATH_TREE";

export type Objective =
  | "OBJECTIVE_UNSPECIFIED"
  | "OBJECTIVE_MINIMIZE"
  | "OBJECTIVE_MAXIMIZE";

export type Weighting =
  | "WEIGHTING_UNSPECIFIED"
  | "WEIGHTING_RAW"
  | "WEIGHTING_TFIDF"
  | "WEIGHTING_BM25";

interface BaseIlluminateRequest {
  seed: string;
  weighting: Weighting;
  /** Empty means no frontier-prefix filter. */
  vertexPrefix: string;
}

/**
 * The API boundary follows the wire oneof. A caller has to select exactly one
 * family and cannot attach a BFS-only or tree-only knob to PageRank.
 */
export type IlluminateRequest =
  | (BaseIlluminateRequest & {
      family: "bfs";
      step: number;
      fanOut: number;
      reduction: Reduction;
      objective: Objective;
    })
  | (BaseIlluminateRequest & {
      family: "pagerank";
      topN: number;
      restartProb: number;
      epsilon: number;
    })
  | (BaseIlluminateRequest & {
      family: "community";
      maxSize: number;
      restartProb: number;
      epsilon: number;
      reduction: Reduction;
      objective: Objective;
    });

const REDUCTION_TO_SDK: Record<Reduction, SdkReduction> = {
  REDUCTION_UNSPECIFIED: SdkReduction.UNSPECIFIED,
  REDUCTION_MINIMUM_SPANNING_TREE: SdkReduction.MINIMUM_SPANNING_TREE,
  REDUCTION_SHORTEST_PATH_TREE: SdkReduction.SHORTEST_PATH_TREE,
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
 * The discriminated request selects the SDK's matching typed family options.
 * The SDK returns a rich-shape Graph with `Map<>` values; this adapter
 * flattens it back to admin's array-of-flat-JSON shape for the canvas and
 * table consumers.
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
    const opts: SdkIlluminateOptions = (() => {
      const shared = {
        weighting: WEIGHTING_TO_SDK[request.weighting],
        vertexPrefix: request.vertexPrefix,
      };
      switch (request.family) {
        case "bfs":
          return {
            ...shared,
            bfs: {
              step: request.step,
              fanOut: request.fanOut,
              objective: OBJECTIVE_TO_SDK[request.objective],
              reduction: REDUCTION_TO_SDK[request.reduction],
            },
          };
        case "pagerank":
          return {
            ...shared,
            ppr: {
              topN: request.topN,
              restartProb: request.restartProb,
              epsilon: request.epsilon,
            },
          };
        case "community":
          return {
            ...shared,
            community: {
              maxSize: request.maxSize,
              restartProb: request.restartProb,
              epsilon: request.epsilon,
              reduction: REDUCTION_TO_SDK[request.reduction],
              objective: OBJECTIVE_TO_SDK[request.objective],
            },
          };
      }
    })();
    const sdkGraph = await client.illuminate(request.seed, opts, init?.signal);
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
