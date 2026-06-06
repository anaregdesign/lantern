import type { Edge } from "~/lib/client/infrastructure/api/get-edge";
import type { PutEdgeBody } from "~/lib/client/infrastructure/api/put-edge";
import type { AddEdgeBody } from "~/lib/client/infrastructure/api/add-edge";
import { parseGoDuration, ttlToExpiration, type TtlInput, INITIAL_TTL_INPUT } from "../edit-vertex/value-codec";

export type EdgeWriteMode = "add" | "put";

export interface EdgeWriteInputs {
  weight: string;
  ttl: TtlInput;
}

export const INITIAL_EDGE_WRITE_INPUTS: EdgeWriteInputs = {
  weight: "1",
  ttl: INITIAL_TTL_INPUT,
};

export interface BuildEdgeBodyResult<TBody> {
  body: TBody | null;
  error: string | null;
}

function buildWeightAndExpiration(
  inputs: EdgeWriteInputs,
  now: number,
): { weight: number; expiration: string | undefined; error: string | null } {
  const raw = inputs.weight.trim();
  if (raw === "") {
    return { weight: 0, expiration: undefined, error: "Weight is required." };
  }
  const weight = Number(raw);
  if (!Number.isFinite(weight)) {
    return {
      weight: 0,
      expiration: undefined,
      error: "Weight must be a finite number.",
    };
  }
  const { iso, error } = ttlToExpiration(inputs.ttl, now);
  if (error) {
    return { weight: 0, expiration: undefined, error };
  }
  return { weight, expiration: iso, error: null };
}

export function buildAddEdgeBody(
  inputs: EdgeWriteInputs,
  now: number = Date.now(),
): BuildEdgeBodyResult<AddEdgeBody> {
  const { weight, expiration, error } = buildWeightAndExpiration(inputs, now);
  if (error) return { body: null, error };
  const edge: AddEdgeBody["edge"] = { weight };
  if (expiration) edge.expiration = expiration;
  return { body: { edge }, error: null };
}

export function buildPutEdgeBody(
  inputs: EdgeWriteInputs,
  now: number = Date.now(),
): BuildEdgeBodyResult<PutEdgeBody> {
  const { weight, expiration, error } = buildWeightAndExpiration(inputs, now);
  if (error) return { body: null, error };
  const edge: PutEdgeBody["edge"] = { weight };
  if (expiration) edge.expiration = expiration;
  return { body: { edge }, error: null };
}

/** Re-export so consumers can pull duration helpers from one module. */
export { parseGoDuration };

/** Seeds editor inputs from a loaded edge. */
export function inputsFromEdge(edge: Edge | null): EdgeWriteInputs {
  return {
    weight: edge?.weight !== undefined ? String(edge.weight) : "1",
    ttl: INITIAL_TTL_INPUT,
  };
}
