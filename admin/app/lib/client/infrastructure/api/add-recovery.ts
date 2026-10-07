import {
  mintReceiptOperationContext,
  mutationReply,
  parseReceiptOperationContext,
  ReceiptMutationUncertainError,
  ReceiptReconciliationError,
  type ReceiptOperationContext,
  type ReceiptStatus,
  type EdgeInput,
  type MutationReply,
} from "lantern-sdk/web";
import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import type { AddDecayingEdgeBody } from "./types";

export interface PreparedAdd {
  input: EdgeInput;
  receipt?: ReceiptOperationContext;
}

/** Decay remains one legacy batch; its original result cannot use one-item receipts. */
export function createDecayingAddRecoveryGateway(
  client: LanternClient,
  body: AddDecayingEdgeBody,
): AddRecoveryGateway {
  return {
    ...createAddRecoveryGateway(client),
    prepare: async (input) => ({ input }),
    send: (prepared, signal) =>
      mutationReply(() =>
        client.addDecayingEdge(
          prepared.input.tail,
          prepared.input.head,
          body,
          signal,
        ),
      ),
  };
}
export interface AddRecoveryGateway {
  prepare(input: EdgeInput, signal?: AbortSignal): Promise<PreparedAdd>;
  send(
    prepared: PreparedAdd,
    signal?: AbortSignal,
  ): Promise<MutationReply<number>>;
  status(
    receipt: ReceiptOperationContext,
    signal?: AbortSignal,
  ): Promise<ReceiptStatus>;
  definiteRefusal(error: unknown): boolean;
}

/** Existing receipt APIs only. Status never sends a mutation. */
export function createAddRecoveryGateway(
  client: LanternClient,
): AddRecoveryGateway {
  return {
    async prepare(input, signal) {
      const capability = await client.getReceiptCapability(signal);
      if (
        !capability.enabled ||
        !capability.supportedMutations.includes("addEdge")
      )
        return { input: { ...input } };
      const receipt = mintReceiptOperationContext(capability, 1);
      const contribId = crypto.getRandomValues(new Uint8Array(24));
      if (!contribId.some((byte) => byte !== 0))
        throw new Error("Could not create an Add contribution identity.");
      return { input: { ...input, contribId }, receipt };
    },
    send(prepared, signal) {
      return mutationReply(async () => {
        if (prepared.receipt) {
          const result = await client.addEdgeWithReceipt(
            { ...prepared.input, contribId: prepared.input.contribId! },
            prepared.receipt,
            signal,
          );
          return result.effectiveWeight;
        }
        return (await client.addEdges([prepared.input], signal))[0]!;
      });
    },
    async status(context, signal) {
      const receipt = parseReceiptOperationContext(context);
      if (receipt.operationIds.length !== 1)
        throw new Error("Invalid retained Add identity.");
      return client.getReceiptStatus(receipt.operationIds[0]!, signal);
    },
    definiteRefusal(error) {
      // A sent receipt mutation with an unobserved response stays uncertain,
      // including cancellation and later continuity/status failures.
      if (error instanceof ReceiptMutationUncertainError) return false;
      if (error instanceof ReceiptReconciliationError) return false;
      const mapped = LanternApiError.fromUnknown("AddEdges", error);
      return (
        mapped instanceof LanternApiError &&
        ["invalid_argument", "permission_denied", "unauthenticated"].includes(
          mapped.code,
        )
      );
    },
  };
}
