import type { Message } from "@bufbuild/protobuf";
import {
  MutationAcceptanceKind,
  type MutationAcceptance as WireAcceptance,
} from "./gen/graph/v1/graph_pb.js";
import { MutationAcceptance, MutationProtocolError } from "./errors.js";

/** Validate acknowledgement exclusivity before interpreting any effect. */
export function checkMutationAcceptance(response: Message & { acceptance?: WireAcceptance }): void {
  const acceptance = response.acceptance;
  if (acceptance === undefined) return;
  if (
    acceptance.kind !== MutationAcceptanceKind.HANDLED_EFFECT_UNDISCLOSED ||
    acceptance.$unknown?.length ||
    response.$unknown?.length
  ) {
    throw new MutationProtocolError("server returned an unknown mutation acceptance");
  }
  const fields = Object.entries(response) as [string, unknown][];
  for (const [field, value] of fields) {
    if (field === "$typeName" || field === "$unknown" || field === "acceptance") continue;
    if (
      value === undefined ||
      value === false ||
      value === 0 ||
      value === 0n ||
      value === "" ||
      (Array.isArray(value) && value.length === 0) ||
      (value instanceof Uint8Array && value.length === 0)
    )
      continue;
    throw new MutationProtocolError("mutation acceptance carried detailed effects");
  }
  throw new MutationAcceptance();
}
