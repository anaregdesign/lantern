import { MutationAcceptance } from "./errors.js";

export type MutationReply<T> =
  | { readonly kind: "knownEffect"; readonly effect: T }
  | { readonly kind: "acceptedUndisclosed" };

/**
 * Adapt an SDK mutation facade without inventing a weight, bool or count.
 * A partial batch or transport failure remains a rejection requiring
 * reconciliation; only the dedicated complete-call acknowledgement is accepted.
 */
export async function mutationReply<T>(call: () => Promise<T>): Promise<MutationReply<T>> {
  try {
    return Object.freeze({ kind: "knownEffect", effect: await call() });
  } catch (error) {
    if (error instanceof MutationAcceptance) {
      return Object.freeze({ kind: "acceptedUndisclosed" });
    }
    throw error;
  }
}
