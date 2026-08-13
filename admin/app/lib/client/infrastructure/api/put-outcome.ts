import { LanternApiError } from "./error";
import type { PutOutcome } from "./types";

/** Fail closed before a non-applied SDK Put can become a success-shaped UI response. */
export function requireAppliedPutOutcome(
  rpc: string,
  identity: string,
  outcome: PutOutcome,
): void {
  if (outcome !== "appliedAndLive") {
    throw LanternApiError.putNotApplied(rpc, identity, outcome);
  }
}
