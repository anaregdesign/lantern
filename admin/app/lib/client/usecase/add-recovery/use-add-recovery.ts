import { useCallback, useMemo, useSyncExternalStore } from "react";
import { useAuth } from "~/lib/client/usecase/auth/use-auth";
import { useConnection } from "~/lib/client/usecase/connection/connection-context";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import {
  createAddRecoveryGateway,
  createDecayingAddRecoveryGateway,
} from "~/lib/client/infrastructure/api/add-recovery";
import type { EdgeInput } from "lantern-sdk/web";
import type { AddDecayingEdgeBody } from "~/lib/client/infrastructure/api/types";

export function useAddRecovery() {
  const { state, adds } = useAuth();
  const { connection } = useConnection();
  const client = useLanternClient();
  const gateway = useMemo(() => createAddRecoveryGateway(client), [client]);
  const actor = JSON.stringify([
    connection.baseUrl,
    state.kind,
    ...(state.kind === "ready"
      ? [state.principal.identity!.issuer, state.principal.identity!.subject]
      : []),
  ]);
  const snapshot = useSyncExternalStore(
    adds.subscribe,
    adds.getSnapshot,
    adds.getSnapshot,
  );
  const signal =
    state.kind === "ready" || state.kind === "off" ? state.signal : undefined;
  const add = useCallback(
    (input: EdgeInput, caller?: AbortSignal) =>
      adds.add(
        actor,
        input,
        gateway,
        signal && caller
          ? AbortSignal.any([signal, caller])
          : (signal ?? caller),
      ),
    [actor, adds, gateway, signal],
  );
  const addDecaying = useCallback(
    (
      tail: string,
      head: string,
      body: AddDecayingEdgeBody,
      caller?: AbortSignal,
    ) =>
      adds.add(
        actor,
        { tail, head, weight: body.initialWeight },
        createDecayingAddRecoveryGateway(client, body),
        signal && caller
          ? AbortSignal.any([signal, caller])
          : (signal ?? caller),
      ),
    [actor, adds, client, signal],
  );
  return {
    attempts: snapshot.filter((a) => a.actor === actor),
    pending: (tail: string, head: string) => adds.pending(actor, tail, head),
    add,
    addDecaying,
    check: (id: string) => adds.check(id, actor, gateway, signal),
  };
}
