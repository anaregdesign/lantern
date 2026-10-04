import { expect, test } from "bun:test";
import { MutationAcceptance } from "lantern-sdk/web";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { addEdgeHandler, putEdgeHandler, deleteEdgeHandler } from "./handlers";
import type { EditEdgeAction } from "./reducer";

test("blind Edge acknowledgements never read back or publish an effect", async () => {
  let reads = 0;
  const accepted = async () => {
    throw new MutationAcceptance();
  };
  const client = {
    addEdge: accepted,
    putEdge: accepted,
    deleteEdge: accepted,
    getEdge: async () => {
      reads++;
      throw new Error("unauthorized readback");
    },
  } as unknown as LanternClient;
  for (const [handler, terminal] of [
    [addEdgeHandler, "WRITE_ACCEPTED_UNDISCLOSED"],
    [putEdgeHandler, "WRITE_ACCEPTED_UNDISCLOSED"],
    [deleteEdgeHandler, "DELETE_ACCEPTED_UNDISCLOSED"],
  ] as const) {
    const actions: EditEdgeAction[] = [];
    await handler(
      {
        client,
        tail: "tails:a",
        head: "heads:b",
        body: { edge: { weight: 7 } },
      },
      (action) => actions.push(action),
    );
    expect(actions.at(-1)?.type).toBe(terminal);
    expect(actions.at(-1)).not.toHaveProperty("edge");
    expect(actions).toHaveLength(2);
  }
  expect(reads).toBe(0);
});

test("wrapped and genuine mutation failures remain failures", async () => {
  for (const failure of [
    new Error("transport failure"),
    new Error("partial batch", { cause: new MutationAcceptance() }),
  ]) {
    let calls = 0;
    const client = {
      addEdge: async () => {
        calls++;
        throw failure;
      },
    } as unknown as LanternClient;
    const actions: EditEdgeAction[] = [];
    await addEdgeHandler({ client, tail: "a", head: "b", body: {} }, (action) =>
      actions.push(action),
    );
    expect(actions.at(-1)?.type).toBe("WRITE_FAILED");
    expect(calls).toBe(1);
  }
});
