import { CreateEdgeOutcome as PbCreateEdgeOutcome } from "./gen/graph/v1/graph_pb.js";
import { InvalidArgumentError, LanternError } from "./errors.js";
import type { EdgeInput } from "./values.js";

/** Conditional outcome; never contains an existing Edge's value or TTL. */
export type CreateEdgeOutcome = "createdAndLive" | "edgeExists" | "endpointNotLive" | "expired";
export function createEdgeOutcomeFromWire(outcome: PbCreateEdgeOutcome): CreateEdgeOutcome {
  switch (outcome) {
    case PbCreateEdgeOutcome.CREATED_AND_LIVE:
      return "createdAndLive";
    case PbCreateEdgeOutcome.EDGE_EXISTS:
      return "edgeExists";
    case PbCreateEdgeOutcome.ENDPOINT_NOT_LIVE:
      return "endpointNotLive";
    case PbCreateEdgeOutcome.EXPIRED:
      return "expired";
    default:
      throw new LanternError(`server returned invalid Create outcome ${outcome}`);
  }
}
export function validateCreateEdgeInput(input: EdgeInput): void {
  if (
    !input ||
    typeof input.weight !== "number" ||
    !Number.isFinite(input.weight) ||
    !Number.isFinite(Math.fround(input.weight)) ||
    Math.fround(input.weight) === 0 ||
    input.contribId !== undefined
  ) {
    throw new InvalidArgumentError(
      "Create requires a finite nonzero float32 source and no contribId",
    );
  }
}
