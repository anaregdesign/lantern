import { useCallback, useEffect, useRef, useState } from "react";
import { LanternApiError } from "~/lib/client/infrastructure/api/error";
import {
  scanEdges,
  type Edge,
} from "~/lib/client/infrastructure/api/scan-edges";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { filterInboundEdges } from "./selectors";

/**
 * How many inbound edges the node-detail Drawer pulls in one shot
 * (#461). The Drawer is a quick-look inspector, not a paginated browser
 * (that's `/edges`), so a single bounded page is plenty; the user can
 * jump to the full edge browser for exhaustive traversal.
 */
const INBOUND_EDGE_LIMIT = 200;

export type InboundEdgesStatus = "idle" | "loading" | "loaded" | "error";

export interface UseInboundEdgesResult {
  status: InboundEdgesStatus;
  /** Edges that terminate at the bound vertex (head === vertexKey). */
  edges: Edge[];
  error: string | null;
  /** Fetch inbound edges for the bound vertex. No-op when none is bound. */
  load: () => void;
}

/**
 * Panel-scoped, on-demand fetch of the edges that TERMINATE at
 * `vertexKey` (#461 "Show inbound edges").
 *
 * Deliberately kept OUT of the Illuminate accumulator: per #466 the
 * canvas owns the graph and the Drawer is a read-only inspector, so
 * inbound edges surfaced here never mutate the canvas view. Re-binding
 * `vertexKey` (inspecting a different vertex, or closing the Drawer with
 * `null`) resets back to `idle` and aborts any in-flight request so a
 * late response can't bleed into the next vertex's panel.
 *
 * No unit-test sibling by repo convention — hooks are covered by the
 * Illuminate Playwright suite; the testable seam (`filterInboundEdges`)
 * lives in `selectors.ts`.
 */
export function useInboundEdges(
  vertexKey: string | null,
): UseInboundEdgesResult {
  const client = useLanternClient();
  const [status, setStatus] = useState<InboundEdgesStatus>("idle");
  const [edges, setEdges] = useState<Edge[]>([]);
  const [error, setError] = useState<string | null>(null);
  const controllerRef = useRef<AbortController | null>(null);

  // Reset whenever the inspected vertex changes (or the Drawer closes).
  // Aborting the in-flight scan keeps a stale response from landing in
  // the next vertex's panel.
  useEffect(() => {
    controllerRef.current?.abort();
    controllerRef.current = null;
    setStatus("idle");
    setEdges([]);
    setError(null);
    return () => {
      controllerRef.current?.abort();
      controllerRef.current = null;
    };
  }, [vertexKey]);

  const load = useCallback(() => {
    if (vertexKey === null || vertexKey === "") return;
    controllerRef.current?.abort();
    const controller = new AbortController();
    controllerRef.current = controller;
    setStatus("loading");
    setError(null);
    void scanEdges(
      client,
      { headPrefix: vertexKey, limit: INBOUND_EDGE_LIMIT },
      { signal: controller.signal },
    )
      .then((response) => {
        if (controller.signal.aborted) return;
        setEdges(filterInboundEdges(response.edges ?? [], vertexKey));
        setStatus("loaded");
      })
      .catch((err: unknown) => {
        if (controller.signal.aborted) return;
        if (err instanceof DOMException && err.name === "AbortError") return;
        setError(messageOf(err));
        setStatus("error");
      });
  }, [client, vertexKey]);

  return { status, edges, error, load };
}

function messageOf(err: unknown): string {
  if (err instanceof LanternApiError) {
    return err.grpcMessage ?? err.message;
  }
  if (err instanceof Error) return err.message;
  return String(err);
}
