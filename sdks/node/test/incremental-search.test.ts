import { describe, expect, test } from "bun:test";

import {
  createIncrementalSearch,
  DEFAULT_DEBOUNCE_MS,
  DEFAULT_MIN_QUERY_LENGTH,
  type IncrementalSearch,
  type IncrementalSearchScheduler,
  type SearchFn,
  type SearchUpdate,
} from "../src/incremental-search.js";
import type { SearchOptions } from "../src/options.js";

class FakeScheduler implements IncrementalSearchScheduler {
  private now = 0;
  private nextID = 0;
  private readonly tasks = new Map<number, { at: number; callback: () => void }>();

  setTimeout(callback: () => void, delayMs: number): number {
    const id = ++this.nextID;
    this.tasks.set(id, { at: this.now + delayMs, callback });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.tasks.delete(handle as number);
  }

  advanceBy(delayMs: number): void {
    const target = this.now + delayMs;
    for (;;) {
      const next = [...this.tasks.entries()]
        .filter(([, task]) => task.at <= target)
        .sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
      if (next === undefined) break;
      const [id, task] = next;
      this.tasks.delete(id);
      this.now = task.at;
      task.callback();
    }
    this.now = target;
  }
}

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
    const calls: { query: string; opts: SearchOptions }[] = [];
    const searchFn: SearchFn = async (query, opts) => {
      calls.push({ query, opts });
      return [{ key: "user.hi", score: 1.5 }];
    };
    const is = createIncrementalSearch(searchFn, {
      debounceMs: 5,
      limit: 7,
      prefix: "user.",
      matchMode: "min-should",
      minShouldMatch: 2,
      fuzziness: 1,
      prefixTerms: true,
    });

    is.search("hello");
    const u = await nextUpdate(is);
    is.close();

    expect(u.error).toBeUndefined();
    expect(u.query).toBe("hello");
    expect(u.hits).toEqual([{ key: "user.hi", score: 1.5 }]);
    expect(calls).toHaveLength(1);
    expect(calls[0]).toEqual({
      query: "hello",
      opts: {
        limit: 7,
        prefix: "user.",
        matchMode: "min-should",
        minShouldMatch: 2,
        fuzziness: 1,
        prefixTerms: true,
      },
    });
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
    const scheduler = new FakeScheduler();
    const seen: string[] = [];
    let notifyAbort: (() => void) | undefined;
    const aborted = new Promise<void>((resolve) => {
      notifyAbort = resolve;
    });
    const searchFn: SearchFn = async (query, _opts, signal) => {
      seen.push(query);
      if (query === "slow") {
        await new Promise<void>((_resolve, reject) => {
          signal?.addEventListener("abort", () => {
            notifyAbort?.();
            reject(new Error("aborted"));
          });
        });
      }
      return [{ key: `${query}.hit`, score: 1 }];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 80 }, scheduler);

    is.search("slow");
    scheduler.advanceBy(80);
    is.search("fast");
    await aborted;
    const update = nextUpdate(is);
    scheduler.advanceBy(80);
    const u = await update;
    is.close();

    expect(u.query).toBe("fast");
    expect(u.hits).toEqual([{ key: "fast.hit", score: 1 }]);
    expect(seen).toEqual(["slow", "fast"]);
  });

  test.each(["resolve", "reject"] as const)(
    "drops an old %s during the next input's debounce window",
    async (outcome) => {
      const scheduler = new FakeScheduler();
      let settleOld: (() => void) | undefined;
      const searchFn: SearchFn = (query) => {
        if (query !== "old") return Promise.resolve([]);
        return new Promise<SearchUpdate["hits"]>((resolve, reject) => {
          settleOld = () =>
            outcome === "resolve"
              ? resolve([{ key: "stale", score: 1 }])
              : reject(new Error("late failure"));
        });
      };
      const is = createIncrementalSearch(searchFn, { debounceMs: 80 }, scheduler);
      const updates: SearchUpdate[] = [];
      const unsubscribe = is.subscribe((update) => updates.push(update));

      is.search("old");
      scheduler.advanceBy(80);
      is.search("new");
      settleOld?.(); // simulate a transport that fulfills despite abort
      await Promise.resolve();

      expect(updates).toEqual([]);
      unsubscribe();
      is.close();
    },
  );

  test("a too-short query resets synchronously with no RPC", async () => {
    let calls = 0;
    const searchFn: SearchFn = async () => {
      calls++;
      return [];
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 60_000, minQueryLength: 3 });
    const updates: SearchUpdate[] = [];
    const unsubscribe = is.subscribe((update) => updates.push(update));

    is.search("ab");
    const u = updates[0];
    unsubscribe();
    is.close();

    expect(u).toBeDefined();
    expect(u.query).toBe("ab");
    expect(u.hits).toEqual([]);
    expect(u.error).toBeUndefined();
    expect(calls).toBe(0);
  });

  test("a new input removes an unread async-iterator result", async () => {
    const searchFn: SearchFn = async (query) => [{ key: query, score: 1 }];
    const is = createIncrementalSearch(searchFn, { debounceMs: 5 });
    const iterator = is[Symbol.asyncIterator]();

    is.search("old");
    await tick(20); // old result is now buffered in the iterator
    is.search("new");
    const next = iterator.next();
    const { value } = await next;
    is.close();

    expect(value?.query).toBe("new");
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

  test("close cancels pending and active work, is idempotent, and prevents new work", async () => {
    const scheduler = new FakeScheduler();
    let calls = 0;
    let activeAborted = false;
    const searchFn: SearchFn = (_query, _opts, signal) => {
      calls++;
      return new Promise((_resolve, reject) => {
        signal?.addEventListener("abort", () => {
          activeAborted = true;
          reject(new Error("aborted"));
        });
      });
    };
    const is = createIncrementalSearch(searchFn, { debounceMs: 10 }, scheduler);

    is.search("pending");
    is.search("active");
    scheduler.advanceBy(10);
    expect(calls).toBe(1);
    is.close();
    is.close(); // must not throw
    is.search("ignored");
    scheduler.advanceBy(100);
    await Promise.resolve();

    expect(activeAborted).toBe(true);
    expect(calls).toBe(1);
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
