import { PutOutcome as PbPutOutcome } from "./gen/graph/v1/graph_pb.js";
import { LanternError } from "./errors.js";

/** Server-authoritative result for one idempotent Put. */
export type PutOutcome = "appliedAndLive" | "expired" | "conditionNotMet" | "superseded";

export function putOutcomeFromWire(outcome: PbPutOutcome): PutOutcome {
  switch (outcome) {
    case PbPutOutcome.APPLIED_AND_LIVE:
      return "appliedAndLive";
    case PbPutOutcome.EXPIRED:
      return "expired";
    case PbPutOutcome.CONDITION_NOT_MET:
      return "conditionNotMet";
    case PbPutOutcome.SUPERSEDED:
      return "superseded";
    case PbPutOutcome.UNSPECIFIED:
    default:
      throw new LanternError(`server returned invalid Put outcome ${outcome}`);
  }
}
