/**
 * Verb dispatcher (#411). Each parsed `Command` from
 * `lib/cli/parser.ts` is routed to the matching SDK call (or pair
 * of calls) on a pre-built `LanternClient`. The dispatcher returns
 * a JSON-serialisable result so the scrollback panel can pretty-print
 * any verb's output via `JSON.stringify`.
 *
 * Errors propagate as `LanternApiError` (the same shape every other
 * usecase consumes) so the scrollback can render `err.code`,
 * `err.grpcMessage`, or fall back to `err.message` consistently.
 */

import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { addEdge } from "~/lib/client/infrastructure/api/add-edge";
import { countVerticesByPrefix } from "~/lib/client/infrastructure/api/count-vertices-by-prefix";
import { deleteEdge } from "~/lib/client/infrastructure/api/delete-edge";
import { deleteVertex } from "~/lib/client/infrastructure/api/delete-vertex";
import { getEdge } from "~/lib/client/infrastructure/api/get-edge";
import { getVertex } from "~/lib/client/infrastructure/api/get-vertex";
import {
  illuminate,
  type Algorithm as ApiAlgorithm,
  type Objective as ApiObjective,
  type Weighting as ApiWeighting,
} from "~/lib/client/infrastructure/api/illuminate";
import { putEdge } from "~/lib/client/infrastructure/api/put-edge";
import { putVertex } from "~/lib/client/infrastructure/api/put-vertex";
import { scanEdges } from "~/lib/client/infrastructure/api/scan-edges";
import { scanVertices } from "~/lib/client/infrastructure/api/scan-vertices";
import type { Command } from "./types";

export interface DispatchInput {
  client: LanternClient;
  command: Command;
  signal?: AbortSignal;
}

const ALGORITHM_TO_API: Record<string, ApiAlgorithm> = {
  none: "ALGORITHM_UNSPECIFIED",
  mst: "ALGORITHM_MINIMUM_SPANNING_TREE",
  spt: "ALGORITHM_SHORTEST_PATH_TREE",
};
const OBJECTIVE_TO_API: Record<string, ApiObjective> = {
  min: "OBJECTIVE_MINIMIZE",
  max: "OBJECTIVE_MAXIMIZE",
};
const WEIGHTING_TO_API: Record<string, ApiWeighting> = {
  raw: "WEIGHTING_RAW",
  tfidf: "WEIGHTING_TFIDF",
};

/**
 * Dispatch one parsed REPL command. Returns whatever JSON-serialisable
 * shape the underlying RPC produced; the caller pretty-prints it
 * into the scrollback panel.
 *
 * `command.verb === "exit"` is handled by the caller (the React
 * component closes the prompt rather than dispatching).
 */
export async function dispatch(input: DispatchInput): Promise<unknown> {
  const { client, command, signal } = input;
  switch (command.verb) {
    case "exit":
      return null;
    case "get":
      if (command.objective === "vertex") {
        return getVertex(client, command.key, { signal });
      }
      return getEdge(client, command.tail, command.head, { signal });
    case "put":
      if (command.objective === "vertex") {
        await putVertex(
          client,
          command.key,
          { vertex: { key: command.key, string: command.value } },
          { signal },
        );
        return { ok: true };
      }
      await putEdge(
        client,
        command.tail,
        command.head,
        {
          edge: {
            tail: command.tail,
            head: command.head,
            weight: command.weight,
          },
        },
        { signal },
      );
      return { ok: true };
    case "delete":
      if (command.objective === "vertex") {
        return deleteVertex(client, command.key, { signal });
      }
      return deleteEdge(client, command.tail, command.head, { signal });
    case "add":
      await addEdge(
        client,
        command.tail,
        command.head,
        {
          edge: {
            tail: command.tail,
            head: command.head,
            weight: command.weight,
          },
        },
        { signal },
      );
      return { ok: true };
    case "scan":
      if (command.objective === "vertices") {
        const page = await scanVertices(
          client,
          { prefix: command.prefix, limit: command.limit },
          { signal },
        );
        const count = await countVerticesByPrefix(client, command.prefix, {
          signal,
        });
        return { ...page, count };
      }
      return scanEdges(
        client,
        { tailPrefix: command.tailPrefix, limit: command.limit },
        { signal },
      );
    case "illuminate":
      return illuminate(
        client,
        {
          seed: command.seed,
          step: command.step,
          k: command.k,
          algorithm: ALGORITHM_TO_API[command.algorithm],
          objective: OBJECTIVE_TO_API[command.objective],
          weighting: WEIGHTING_TO_API[command.weighting],
        },
        { signal },
      );
  }
}

/**
 * Returns true when the command would mutate server state. Used by
 * the React panel to gate destructive verbs behind a confirmation
 * chip (#411).
 */
export function isDestructive(command: Command): boolean {
  return (
    command.verb === "delete" ||
    command.verb === "put" ||
    command.verb === "add"
  );
}
