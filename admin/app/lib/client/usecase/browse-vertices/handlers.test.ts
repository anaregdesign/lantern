import { describe, expect, it } from "bun:test";
import { LanternError } from "lantern-sdk/web";
import type { LanternClient } from "~/lib/client/infrastructure/api/lantern-client";
import { fetchCount, fetchPage } from "./handlers";
import { browseVerticesReducer, type BrowseVerticesAction } from "./reducer";
import { INITIAL_BROWSE_VERTICES_STATE } from "./state";

function clientWithFailure(code?: number): LanternClient {
  const fail = async () => {
    throw new LanternError("opaque diagnostic", {
      cause: code === undefined ? undefined : { code },
    });
  };
  return {
    countVerticesByPrefix: fail,
    scanVertices: fail,
  } as unknown as LanternClient;
}

describe("browse vertex requests", () => {
  const input = { prefix: "tenant:", epoch: 0, requestId: 1 };

  it("separates successful empty and nonempty counts from loading", async () => {
    for (const count of [0n, 42n]) {
      const actions: BrowseVerticesAction[] = [];
      const client = {
        countVerticesByPrefix: async () => count,
      } as unknown as LanternClient;
      await fetchCount({ ...input, client }, (action) => actions.push(action));
      expect(actions).toEqual([
        { type: "COUNT_REQUESTED", epoch: 0, requestId: 1 },
        {
          type: "COUNT_RECEIVED",
          epoch: 0,
          requestId: 1,
          count: Number(count),
        },
      ]);
    }
  });

  it("classifies denied scan and count using the preserved SDK cause", async () => {
    const client = clientWithFailure(7);
    const actions: BrowseVerticesAction[] = [];
    await fetchPage({ ...input, client, cursor: "", pageSize: 50 }, (action) =>
      actions.push(action),
    );
    await fetchCount({ ...input, client }, (action) => actions.push(action));
    expect(actions.filter((action) => action.type.endsWith("FAILED"))).toEqual([
      {
        type: "PAGE_FAILED",
        epoch: 0,
        requestId: 1,
        error: { kind: "denied", message: "opaque diagnostic" },
      },
      {
        type: "COUNT_FAILED",
        epoch: 0,
        requestId: 1,
        error: { kind: "denied", message: "opaque diagnostic" },
      },
    ]);
    expect(actions.some((action) => action.type === "COUNT_RECEIVED")).toBe(
      false,
    );
  });

  it("preserves a successful scanned page when only count is unavailable", async () => {
    const client = clientWithFailure(14);
    client.scanVertices = async () => ({
      vertices: [
        { key: "tenant:a", kind: "string", value: "visible", expiration: null },
      ],
      nextCursor: new Uint8Array(),
    });
    let state = INITIAL_BROWSE_VERTICES_STATE;
    const dispatch = (action: BrowseVerticesAction) => {
      state = browseVerticesReducer(state, action);
    };
    await fetchPage({ ...input, client, cursor: "", pageSize: 50 }, dispatch);
    const pages = state.pages;
    await fetchCount({ ...input, client }, dispatch);
    expect(state.status).toBe("ready");
    expect(state.pages).toBe(pages);
    expect(state.pages[0].vertices[0].key).toBe("tenant:a");
    expect(state.error).toBeNull();
    expect(state.count).toEqual({
      status: "unavailable",
      error: "opaque diagnostic",
    });
  });

  it("does not classify an unstructured permission-looking message as denial", async () => {
    const client = {
      countVerticesByPrefix: async () => {
        throw new Error("permission_denied");
      },
    } as unknown as LanternClient;
    const actions: BrowseVerticesAction[] = [];
    await fetchCount({ ...input, client }, (action) => actions.push(action));
    expect(actions.at(-1)).toEqual({
      type: "COUNT_FAILED",
      epoch: 0,
      requestId: 1,
      error: { kind: "unavailable", message: "permission_denied" },
    });
  });

  it("does not dispatch a failure or numeric count after cancellation", async () => {
    const abort = async () => {
      throw new DOMException("canceled", "AbortError");
    };
    const client = {
      countVerticesByPrefix: abort,
      scanVertices: abort,
    } as unknown as LanternClient;
    const actions: BrowseVerticesAction[] = [];
    await fetchPage({ ...input, client, cursor: "", pageSize: 50 }, (action) =>
      actions.push(action),
    );
    await fetchCount({ ...input, client }, (action) => actions.push(action));
    expect(actions.map((action) => action.type)).toEqual([
      "PAGE_REQUESTED",
      "COUNT_REQUESTED",
    ]);
  });
});
