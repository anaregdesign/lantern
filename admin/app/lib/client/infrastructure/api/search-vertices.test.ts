import { describe, expect, it } from "bun:test";
import type { LanternClient } from "./lantern-client";
import { searchAllVertices, searchVertices } from "./search-vertices";

describe("searchVertices adapter", () => {
  it("uses FULL_VERTEX paging and maps the exact SDK snapshot", async () => {
    let captured:
      | {
          query: string;
          options: Record<string, unknown>;
          signal?: AbortSignal;
        }
      | undefined;
    const cursor = new Uint8Array([1, 2]);
    const nextCursor = new Uint8Array([3, 4]);
    const client = {
      async searchVerticesPage(
        query: string,
        options: Record<string, unknown>,
        signal?: AbortSignal,
      ) {
        captured = { query, options, signal };
        return {
          hits: [
            {
              key: "doc/1",
              score: 3.5,
              vertex: {
                key: "doc/1",
                value: "alpha",
                kind: "string",
                expiration: null,
              },
              projectionStatus: "snapshot",
            },
          ],
          nextCursor,
          effectiveLimit: 1,
          truncated: true,
          continuationLimited: false,
        };
      },
    } as unknown as LanternClient;

    const response = await searchVertices(client, {
      query: "alpha",
      limit: 1,
      cursor,
      projection: "full-vertex",
    });

    expect(captured?.query).toBe("alpha");
    expect(captured?.options).toMatchObject({
      limit: 1,
      cursor,
      projection: "full-vertex",
    });
    expect(response.hits).toEqual([
      {
        key: "doc/1",
        score: 3.5,
        vertex: { key: "doc/1", string: "alpha" },
        projectionStatus: "snapshot",
      },
    ]);
    expect(response.nextCursor).toBe(nextCursor);
    expect(response.truncated).toBe(true);
  });

  it("uses the SDK iterator for bounded all-page search", async () => {
    let captured: Record<string, unknown> | undefined;
    const client = {
      async *searchVerticesIter(
        _query: string,
        options: Record<string, unknown>,
      ) {
        captured = options;
        yield { key: "doc/1", score: 2, projectionStatus: "key-score" };
        yield { key: "doc/2", score: 1, projectionStatus: "key-score" };
      },
    } as unknown as LanternClient;

    const hits = await searchAllVertices(client, {
      query: "alpha",
      limit: 1,
      cursor: new Uint8Array([1]),
      projection: "key-score",
    });

    expect(captured).toMatchObject({
      limit: 1,
      cursor: new Uint8Array([1]),
      projection: "key-score",
    });
    expect(hits.map((hit) => hit.key)).toEqual(["doc/1", "doc/2"]);
  });
});
