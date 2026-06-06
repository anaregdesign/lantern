import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { IlluminateResponse } from "./types";
import {
  Algorithm as ProtoAlgorithm,
  Objective as ProtoObjective,
  Weighting as ProtoWeighting,
} from "~/lib/api/gen/graph/v1/graph_pb";

export type { Edge, Graph, IlluminateResponse, Vertex } from "./types";

/**
 * String enum forms exposed to the UI. Per #410 the wire schema carries
 * three orthogonal axes (algorithm × objective × weighting); the admin
 * UI tracks each as a stable string so router serialisation, persisted
 * URL state, and Playwright fixtures keep one human-readable vocabulary
 * across surfaces.
 *
 * The string literals match the protobuf enum value names verbatim so a
 * future binary-format transport only swaps one mapping table.
 */
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
  | "WEIGHTING_TFIDF";

export interface IlluminateRequest {
  seed: string;
  step?: number;
  k?: number;
  algorithm?: Algorithm;
  objective?: Objective;
  weighting?: Weighting;
}

// connect-es v1 strips the common <NAME>_ prefix from enum members;
// keep these tables as the single source of truth for the
// string-to-numeric mapping the UI relies on.
const ALGORITHM_TO_PROTO: Record<Algorithm, ProtoAlgorithm> = {
  ALGORITHM_UNSPECIFIED: ProtoAlgorithm.UNSPECIFIED,
  ALGORITHM_MINIMUM_SPANNING_TREE: ProtoAlgorithm.MINIMUM_SPANNING_TREE,
  ALGORITHM_SHORTEST_PATH_TREE: ProtoAlgorithm.SHORTEST_PATH_TREE,
};
const OBJECTIVE_TO_PROTO: Record<Objective, ProtoObjective> = {
  OBJECTIVE_UNSPECIFIED: ProtoObjective.UNSPECIFIED,
  OBJECTIVE_MINIMIZE: ProtoObjective.MINIMIZE,
  OBJECTIVE_MAXIMIZE: ProtoObjective.MAXIMIZE,
};
const WEIGHTING_TO_PROTO: Record<Weighting, ProtoWeighting> = {
  WEIGHTING_UNSPECIFIED: ProtoWeighting.UNSPECIFIED,
  WEIGHTING_RAW: ProtoWeighting.RAW,
  WEIGHTING_TFIDF: ProtoWeighting.TFIDF,
};

/**
 * Calls `LanternService.Illuminate` over Connect-Web.
 *
 * Runs a k-bounded BFS from the supplied seed. Optional knobs (step / k /
 * algorithm / objective / weighting) default server-side when omitted.
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
    const resp = await client.illuminate(
      {
        seed: request.seed,
        step: request.step ?? 0,
        k: request.k ?? 0,
        algorithm:
          request.algorithm === undefined
            ? ProtoAlgorithm.UNSPECIFIED
            : ALGORITHM_TO_PROTO[request.algorithm],
        objective:
          request.objective === undefined
            ? ProtoObjective.UNSPECIFIED
            : OBJECTIVE_TO_PROTO[request.objective],
        weighting:
          request.weighting === undefined
            ? ProtoWeighting.UNSPECIFIED
            : WEIGHTING_TO_PROTO[request.weighting],
      },
      { signal: init?.signal },
    );
    return resp.toJson() as IlluminateResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("Illuminate", err);
  }
}
