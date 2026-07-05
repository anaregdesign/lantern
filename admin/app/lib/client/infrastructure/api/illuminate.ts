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

// The admin UI tracks each axis as a stable string so router
// serialisation and Playwright fixtures keep one human-readable
// vocabulary across surfaces. Since #846 the wire request is a params
// ONEOF (bfs | ppr | community); this adapter owns the translation from
// admin's flat axis vocabulary to the typed per-family SDK options —
// mst/spt select the BFS family's Reduction, ppr selects the PPR family
// (step has no PPR meaning and is dropped; k becomes topN), and
// community selects the local-community family (#845; step dropped, k
// becomes the max_size upper bound).
export type Algorithm =
  | "ALGORITHM_UNSPECIFIED"
  | "ALGORITHM_MINIMUM_SPANNING_TREE"
  | "ALGORITHM_SHORTEST_PATH_TREE"
  | "ALGORITHM_PERSONALIZED_PAGERANK"
  | "ALGORITHM_LOCAL_COMMUNITY";

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
  /**
   * Personalized PageRank / local-community knobs, consulted when
   * `algorithm === "ALGORITHM_PERSONALIZED_PAGERANK"` (#801) or
   * `algorithm === "ALGORITHM_LOCAL_COMMUNITY"` (#845) — both families
   * share the same α/ε locality knobs. `restartProb` is the
   * restart/teleport-to-seed probability α in (0,1); `epsilon` is the
   * forward-push residual threshold ε > 0. 0/omitted lets the server apply
   * its defaults (α=0.15 / ε=1e-4).
   */
  restartProb?: number;
  epsilon?: number;
}

const ALGORITHM_TO_REDUCTION: Record<Algorithm, SdkReduction> = {
  ALGORITHM_UNSPECIFIED: SdkReduction.UNSPECIFIED,
  ALGORITHM_MINIMUM_SPANNING_TREE: SdkReduction.MINIMUM_SPANNING_TREE,
  ALGORITHM_SHORTEST_PATH_TREE: SdkReduction.SHORTEST_PATH_TREE,
  // PPR is handled as its own oneof family, never as a reduction.
  ALGORITHM_PERSONALIZED_PAGERANK: SdkReduction.UNSPECIFIED,
  // Local community (#845) is likewise its own oneof family, never a
  // BFS reduction.
  ALGORITHM_LOCAL_COMMUNITY: SdkReduction.UNSPECIFIED,
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
 * / k / algorithm / objective / weighting / prefix) default server-side
 * when omitted; `algorithm=ALGORITHM_PERSONALIZED_PAGERANK` switches to a
 * seed-anchored PPR walk tuned by restartProb / epsilon (#801), and
 * `algorithm=ALGORITHM_LOCAL_COMMUNITY` switches to PageRank-Nibble local
 * community extraction (#845; k is the max_size upper bound, α/ε tune the
 * push). The SDK returns a rich-shape Graph with `Map<>` values; this
 * adapter flattens it back to admin's array-of-flat-JSON shape so the
 * existing canvas + table consumers keep working unchanged (#409, #410).
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
    const opts: SdkIlluminateOptions = {
      weighting:
        request.weighting !== undefined
          ? WEIGHTING_TO_SDK[request.weighting]
          : undefined,
      vertexPrefix: request.vertexPrefix ?? "",
    };
    if (request.algorithm === "ALGORITHM_PERSONALIZED_PAGERANK") {
      opts.ppr = {
        topN: request.k ?? 0,
        restartProb: request.restartProb ?? 0,
        epsilon: request.epsilon ?? 0,
      };
    } else if (request.algorithm === "ALGORITHM_LOCAL_COMMUNITY") {
      // Local community extraction (#845): PageRank-Nibble. Mirrors the Go
      // CLI translation (`cli/service/service.go` `IlluminateFamilyOption`,
      // `case "community"`): k is the max_size UPPER BOUND (the conductance
      // sweep may stop earlier), step has no meaning here and is dropped,
      // and α/ε pass through (0 = server default, α=0.15 / ε=1e-4).
      opts.community = {
        maxSize: request.k ?? 0,
        restartProb: request.restartProb ?? 0,
        epsilon: request.epsilon ?? 0,
      };
    } else {
      opts.bfs = {
        step: request.step ?? 0,
        fanOut: request.k ?? 0,
        objective:
          request.objective !== undefined
            ? OBJECTIVE_TO_SDK[request.objective]
            : undefined,
        reduction:
          ALGORITHM_TO_REDUCTION[request.algorithm ?? "ALGORITHM_UNSPECIFIED"],
      };
    }
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
