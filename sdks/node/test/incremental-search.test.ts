import { describe, expect, test } from "bun:test";

import {
  createIncrementalSearch,
  DEFAULT_DEBOUNCE_MS,
  DEFAULT_MIN_QUERY_LENGTH,
  type IncrementalSearch,
  type SearchFn,
  type SearchUpdate,
} from "../src/incremental-search.js";

// nextUpdate resolves with the first update the driver emits.
function nextUpdate(is: IncrementalSearch): Promise<SearchUpdate> {
  return new Promise((resolve) => {
    const unsubscribe = is.subscribe((u) => {
      unsubscribe();
      resolve(u);
    });
  });
}

const tick = (ms: number) => new Promise((r) => setTimeout(r, ms));

describe("createIncrementalSearch", () => {
  test("exposes the documented defaults", () => {
    expect(DEFAULT_DEBOUNCE_MS).toBe(150);
    expect(DEFAULT_MIN_QUERY_LENGTH).toBe(1);
  });

  test("forwards limit/prefix and delivers ranked hits", async () => {
    const calls: { query: string; opts: { limit?: number; prefix?: string } }[] = [];
    const searchFn: SearchFn = async (query, opts) => {
      calls.push({ query, opts });
      return [{ key: "user.hi", score: 1.5 }];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 5, limit: 7, prefix: "user." });

    is.search("hello");
    const u = await nextUpdate(is);
    is.close();

    expect(u.error).toBeUndefined();
    expect(u.query).toBe("hello");
    expect(u.hits).toEqual([{ key: "user.hi", score: 1.5 }]);
    expect(calls).toHaveLength(1);
    expect(calls[0]).toEqual({ query: "hello", opts: { limit: 7, prefix: "user." } });
  });

  test("debounce coalesces a burst into one search of the final query", async () => {
    const queries: string[] = [];
    const searchFn: SearchFn = async (query) => {
      queries.push(query);
      return [];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 30 });

    is.search("a");
    is.search("ab");
    is.search("abc");
    const u = await nextUpdate(is);
    is.close();

    expect(u.query).toBe("abc");
    expect(queries).toEqual(["abc"]);
  });

  test("a newer query aborts the in-flight search and only its result is delivered", async () => {
    const seen: string[] = [];
    const searchFn: SearchFn = async (query, _opts, signal) => {
      seen.push(query);
      if (query === "slow") {
        await new Promise<void>((resolve, reject) => {
          const t = setTimeout(resolve, 500);
          signal?.addEventListener("abort", () => {
            clearTimeout(t);
            reject(new Error("aborted"));
          });
        });
      }
      return [{ key: `${query}.hit`, score: 1 }];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 5 });

    is.search("slow");
    await tick(40); // let "slow" dispatch and enter its delay
    is.search("fast");
    const u = await nextUpdate(is);
    is.close();

    expect(u.query).toBe("fast");
    expect(u.hits).toEqual([{ key: "fast.hit", score: 1 }]);
    expect(seen).toEqual(["slow", "fast"]);
  });

  test("a too-short query resolves to an empty result with no RPC", async () => {
    let calls = 0;
    const searchFn: SearchFn = async () => {
      calls++;
      return [];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 5, minQueryLength: 3 });

    is.search("ab");
    const u = await nextUpdate(is);
    is.close();

    expect(u.query).toBe("ab");
    expect(u.hits).toEqual([]);
    expect(u.error).toBeUndefined();
    expect(calls).toBe(0);
  });

  test("delivers a search rejection as SearchUpdate.error", async () => {
    const boom = new Error("vertex search is disabled on this server");
    const searchFn: SearchFn = async () => {
      throw boom;
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 5 });

    is.search("anything");
    const u = await nextUpdate(is);
    is.close();

    expect(u.error).toBe(boom);
    expect(u.hits).toEqual([]);
  });

  test("close is idempotent and search after close is a no-op", async () => {
    let calls = 0;
    const searchFn: SearchFn = async () => {
      calls++;
      return [];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 5 });

    is.close();
    is.close(); // must not throw
    is.search("ignored");
    await tick(20);

    expect(calls).toBe(0);
  });

  test("is async-iterable, yielding the latest update", async () => {
    const searchFn: SearchFn = async (query) => [{ key: query, score: 1 }];
    const is = createIncrementalSearch(searchFn, { debounceMs: 5 });

    is.search("hello");
    const iterator = is[Symbol.asyncIterator]();
    const { value, done } = await iterator.next();
    is.close();

    expect(done).toBe(false);
    expect(value?.query).toBe("hello");
    expect(value?.hits).toEqual([{ key: "hello", score: 1 }]);
  });
});
