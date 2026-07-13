import { describe, expect, it } from "bun:test";
import type { LanternClient } from "./lantern-client";
import { searchVertices } from "./search-vertices";

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
});
