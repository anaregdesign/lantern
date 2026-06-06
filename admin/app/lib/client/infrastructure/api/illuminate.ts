import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { IlluminateResponse } from "./types";
import { Optimization as ProtoOptimization } from "~/lib/api/gen/graph/v1/graph_pb";

export type { Edge, Graph, IlluminateResponse, Vertex } from "./types";

/**
 * The set of optimization values the legacy OpenAPI surface exposed
 * as string enum members. Mirrored here so the usecase layer keeps
 * the same import. The proto enum is numeric; we translate at the
 * adapter boundary so the rest of the UI never touches the enum
 * numbers. The string form intentionally matches the protobuf
 * full enum value names so swapping in a different transport later
 * (e.g. binary protobuf) only changes one mapping table.
 */
export type Optimization =
  | "OPTIMIZATION_UNSPECIFIED"
  | "OPTIMIZATION_MINIMUM_SPANNING_TREE"
  | "OPTIMIZATION_MAXIMUM_SPANNING_TREE"
  | "OPTIMIZATION_SHORTEST_PATH_TREE"
  | "OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE";

export interface IlluminateRequest {
  seed: string;
  step?: number;
  k?: number;
  tfidf?: boolean;
  optimization?: Optimization;
}

// connect-es v1 strips the common OPTIMIZATION_ prefix from enum
// members; keep this table as the single source of truth for the
// string-to-numeric mapping the UI relies on.
const OPTIMIZATION_TO_PROTO: Record<Optimization, ProtoOptimization> = {
  OPTIMIZATION_UNSPECIFIED: ProtoOptimization.UNSPECIFIED,
  OPTIMIZATION_MINIMUM_SPANNING_TREE: ProtoOptimization.MINIMUM_SPANNING_TREE,
  OPTIMIZATION_MAXIMUM_SPANNING_TREE: ProtoOptimization.MAXIMUM_SPANNING_TREE,
  OPTIMIZATION_SHORTEST_PATH_TREE: ProtoOptimization.SHORTEST_PATH_TREE,
  OPTIMIZATION_SHORTEST_PATH_TREE_INVERSE:
    ProtoOptimization.SHORTEST_PATH_TREE_INVERSE,
};

/**
 * Calls `LanternService.Illuminate` over Connect-Web.
 *
 * Runs a k-bounded BFS from the supplied seed. Optional knobs
 * (step / k / tfidf / optimization) default server-side when omitted.
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
        tfidf: request.tfidf ?? false,
        optimization:
          request.optimization === undefined
            ? ProtoOptimization.UNSPECIFIED
            : OPTIMIZATION_TO_PROTO[request.optimization],
      },
      { signal: init?.signal },
    );
    return resp.toJson() as IlluminateResponse;
  } catch (err) {
    throw LanternApiError.fromUnknown("Illuminate", err);
  }
}
