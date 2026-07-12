/**
 * incremental-search.ts — the search-as-you-type driver that wraps
 * `Lantern.searchVertices` with debounce, in-flight cancellation, and
 * stale-drop so a UI can fire a fresh search on every keystroke and only ever
 * observe the result of the LATEST query.
 *
 * It is the full-text analogue of the admin SPA's vertex picker: rapid query
 * updates are coalesced and debounced, a newer query aborts the previous
 * in-flight RPC, and a late reply from a superseded query is dropped. The wire
 * surface is unchanged — this is pure client-side orchestration over the
 * existing `searchVertices` RPC, so it needs no proto or server support.
 */

import type { SearchHit } from "./values.js";
import type { SearchOptions } from "./options.js";
import { InvalidArgumentError } from "./errors.js";

/** Default debounce window (ms); mirrors the admin SPA's `SUGGEST_DEBOUNCE_MS`. */
export const DEFAULT_DEBOUNCE_MS = 150;

/** Default shortest query (in code points) that triggers an RPC. */
export const DEFAULT_MIN_QUERY_LENGTH = 1;

export interface IncrementalSearchOptions extends SearchOptions {
  /** Quiet period after the last `search` call before the RPC fires (default 150). */
  debounceMs?: number;
  /**
   * Shortest query (code points) that triggers an RPC; a shorter one resolves
   * immediately to an empty result with no round-trip (default 1). Pass 0 to
   * search even the empty query.
   */
  minQueryLength?: number;
}

/**
 * One delivery from an {@link IncrementalSearch}: the query that produced it
 * paired with either its ranked hits or the error `searchVertices` rejected
 * with. On a defined `error`, `hits` is empty. `query` is always the exact
 * string passed to `search`, so a consumer rendering results can confirm the
 * delivery still matches the text currently in the input box.
 */
export interface SearchUpdate {
  query: string;
  hits: SearchHit[];
  error?: unknown;
}

/**
 * The search primitive the driver drives. Structurally matches
 * `Lantern.searchVertices`, so `client.incrementalSearch()` simply binds the
 * method — but accepting the function (rather than the client) keeps this
 * module free of a client import and trivially testable with a fake.
 */
export type SearchFn = (
  query: string,
  opts: SearchOptions,
  signal?: AbortSignal,
) => Promise<SearchHit[]>;

/** Clock abstraction used to test debounce behavior without wall-clock sleeps. */
export interface IncrementalSearchScheduler {
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

const systemScheduler: IncrementalSearchScheduler = {
  setTimeout: (callback, delayMs) => setTimeout(callback, delayMs),
  clearTimeout: (handle) => clearTimeout(handle as ReturnType<typeof setTimeout>),
};

/**
 * A search-as-you-type driver. Push queries with `search` as the user types;
 * receive ranked results via `subscribe` (which returns an unsubscribe) or by
 * `for await`-ing the driver. Call `close` (or rely on it via the async
 * iterator's `return`) to stop it and abort any in-flight call.
 */
export interface IncrementalSearch {
  search(query: string): void;
  subscribe(listener: (update: SearchUpdate) => void): () => void;
  close(): void;
  [Symbol.asyncIterator](): AsyncIterator<SearchUpdate>;
}

/**
 * Builds an {@link IncrementalSearch} around any {@link SearchFn}. Most callers
 * use `Lantern.incrementalSearch()`, which binds `searchVertices`; this factory
 * is exported for tests and for driving a custom search primitive. The
 * scheduler parameter supports deterministic clocks; applications normally
 * leave it at the system default.
 */
export function createIncrementalSearch(
  searchFn: SearchFn,
  options: IncrementalSearchOptions = {},
  scheduler: IncrementalSearchScheduler = systemScheduler,
): IncrementalSearch {
  const debounceMs = options.debounceMs ?? DEFAULT_DEBOUNCE_MS;
  const minQueryLength = options.minQueryLength ?? DEFAULT_MIN_QUERY_LENGTH;
  for (const [name, value] of [
    ["debounceMs", debounceMs],
    ["minQueryLength", minQueryLength],
  ] as const) {
    if (!Number.isFinite(value) || !Number.isInteger(value) || value < 0) {
      throw new InvalidArgumentError(`incremental search: ${name} must be a non-negative integer`);
    }
  }
  const { debounceMs: _debounceMs, minQueryLength: _minQueryLength, ...searchOpts } = options;

  const listeners = new Set<(update: SearchUpdate) => void>();
  let epoch = 0;
  let timer: unknown;
  let inFlight: AbortController | undefined;
  let closed = false;

  // Async-iterator plumbing: a one-slot, newest-wins buffer plus a pending
  // next() resolver, so a slow `for await` consumer always sees the latest
  // update rather than a backlog.
  let buffered: SearchUpdate | undefined;
  let pendingNext: ((result: IteratorResult<SearchUpdate>) => void) | undefined;

  function emit(update: SearchUpdate): void {
    for (const listener of listeners) listener(update);
    if (pendingNext) {
      const resolve = pendingNext;
      pendingNext = undefined;
      resolve({ value: update, done: false });
    } else {
      buffered = update; // newest-wins
    }
  }

  function dispatch(query: string, myEpoch: number): void {
    if (closed || myEpoch !== epoch) return;

    const controller = new AbortController();
    inFlight = controller;
    void searchFn(query, searchOpts, controller.signal).then(
      (hits) => {
        if (closed || myEpoch !== epoch) return; // superseded: drop
        emit({ query, hits });
      },
      (error) => {
        // Drop a stale or aborted rejection; surface a live one.
        if (closed || myEpoch !== epoch || controller.signal.aborted) return;
        emit({ query, hits: [], error });
      },
    );
  }

  function search(query: string): void {
    if (closed) return;
    // Latest INPUT wins: invalidate buffered output, the pending debounce,
    // and the active RPC before waiting to dispatch the new query.
    const myEpoch = ++epoch;
    buffered = undefined;
    if (timer !== undefined) {
      scheduler.clearTimeout(timer);
      timer = undefined;
    }
    inFlight?.abort();
    inFlight = undefined;

    if ([...query].length < minQueryLength) {
      // Too short to search: reset immediately, with no debounce or RPC.
      emit({ query, hits: [] });
      return;
    }

    timer = scheduler.setTimeout(() => {
      timer = undefined;
      dispatch(query, myEpoch);
    }, debounceMs);
  }

  function close(): void {
    if (closed) return;
    closed = true;
    epoch++;
    if (timer !== undefined) {
      scheduler.clearTimeout(timer);
      timer = undefined;
    }
    inFlight?.abort();
    inFlight = undefined;
    if (pendingNext) {
      const resolve = pendingNext;
      pendingNext = undefined;
      resolve({ value: undefined, done: true });
    }
  }

  function subscribe(listener: (update: SearchUpdate) => void): () => void {
    listeners.add(listener);
    return () => {
      listeners.delete(listener);
    };
  }

  const iterator: AsyncIterator<SearchUpdate> = {
    next(): Promise<IteratorResult<SearchUpdate>> {
      if (buffered !== undefined) {
        const value = buffered;
        buffered = undefined;
        return Promise.resolve({ value, done: false });
      }
      if (closed) return Promise.resolve({ value: undefined, done: true });
      return new Promise((resolve) => {
        pendingNext = resolve;
      });
    },
    return(): Promise<IteratorResult<SearchUpdate>> {
      close();
      return Promise.resolve({ value: undefined, done: true });
    },
  };

  return {
    search,
    subscribe,
    close,
    [Symbol.asyncIterator]() {
      return iterator;
    },
  };
}
