import type { EditVertexState } from "./state";
import { buildPutVertexBody, type BuildBodyResult } from "./value-codec";

/**
 * Validates the active edit form and either yields a wire-ready
 * `PutVertexBody` or the first error message. `now` is injected so tests
 * can pin time and reducers stay pure.
 */
export function selectPutVertexBody(
  state: EditVertexState,
  now: number = Date.now(),
): BuildBodyResult {
  return buildPutVertexBody(state.kind, state.inputs, state.ttl, now);
}

/** True when the form would produce a valid PUT body right now. */
export function selectFormValid(
  state: EditVertexState,
  now: number = Date.now(),
): boolean {
  return selectPutVertexBody(state, now).body !== null;
}

/** Convenience: true while the editor is open. */
export function selectEditing(state: EditVertexState): boolean {
  return state.mode === "edit";
}

/** Convenience: true after a successful delete (route should redirect). */
export function selectDeleted(state: EditVertexState): boolean {
  return state.deleteStatus === "deleted";
}
