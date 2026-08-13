import { describe, expect, test } from "bun:test";

import type { LanternClient } from "./lantern-client";
import { LanternApiError } from "./error";
import { putEdge } from "./put-edge";
import { putEdges } from "./put-edges";
import { putVertex } from "./put-vertex";
import { putVertices } from "./put-vertices";

function clientWith(stubs: Partial<LanternClient>): LanternClient {
  return stubs as LanternClient;
}

async function expectOutcomeError(
  call: Promise<unknown>,
  code: string,
): Promise<void> {
  try {
    await call;
    throw new Error("expected Put adapter to reject a non-applied outcome");
  } catch (error) {
    expect(error).toBeInstanceOf(LanternApiError);
    expect((error as LanternApiError).code).toBe(code);
  }
}

describe("Put adapters", () => {
  test("surface applied outcomes instead of empty success objects", async () => {
    const client = clientWith({
      putVertex: async () => "appliedAndLive",
      putVertices: async () => [{ key: "v", outcome: "appliedAndLive" }],
      putEdge: async () => "appliedAndLive",
      putEdges: async () => [
        { tail: "a", head: "b", outcome: "appliedAndLive" },
      ],
    });

    await expect(
      putVertex(client, "v", { vertex: { string: "value" } }),
    ).resolves.toEqual({
      outcome: "appliedAndLive",
    });
    await expect(
      putVertices(client, { vertices: [{ key: "v", string: "value" }] }),
    ).resolves.toEqual({
      results: [{ key: "v", outcome: "appliedAndLive" }],
    });
    await expect(
      putEdge(client, "a", "b", { edge: { weight: 1 } }),
    ).resolves.toEqual({
      outcome: "appliedAndLive",
    });
    await expect(
      putEdges(client, { edges: [{ tail: "a", head: "b", weight: 1 }] }),
    ).resolves.toEqual({
      results: [{ tail: "a", head: "b", outcome: "appliedAndLive" }],
    });
  });

  test("reject every non-applied outcome before UI success", async () => {
    await expectOutcomeError(
      putVertex(clientWith({ putVertex: async () => "expired" }), "v", {}),
      "put_expired",
    );
    await expectOutcomeError(
      putVertices(
        clientWith({
          putVertices: async () => [{ key: "v", outcome: "superseded" }],
        }),
        { vertices: [{ key: "v" }] },
      ),
      "put_superseded",
    );
    await expectOutcomeError(
      putEdge(
        clientWith({ putEdge: async () => "conditionNotMet" }),
        "a",
        "b",
        {},
      ),
      "put_condition_not_met",
    );
    await expectOutcomeError(
      putEdges(
        clientWith({
          putEdges: async () => [{ tail: "a", head: "b", outcome: "expired" }],
        }),
        { edges: [{ tail: "a", head: "b", weight: 1 }] },
      ),
      "put_expired",
    );
  });
});
