import { describe, expect, it } from "bun:test";
import type { SearchResultRow, SearchVerticesState } from "./state";
import {
  INITIAL_SEARCH_VERTICES_STATE,
  DEFAULT_SEARCH_QUERY_OPTIONS,
} from "./state";
import { searchVerticesReducer, type SearchVerticesAction } from "./reducer";
import {
  createSearchVerticesDriver,
  type SearchVerticesInput,
  type SearchVerticesRunner,
  type SearchVerticesScheduler,
} from "./driver";

class FakeScheduler implements SearchVerticesScheduler {
  #now = 0;
  #nextID = 0;
  #tasks = new Map<number, { at: number; callback: () => void }>();

  setTimeout(callback: () => void, delayMs: number): number {
    const id = ++this.#nextID;
    this.#tasks.set(id, { at: this.#now + delayMs, callback });
    return id;
  }

  clearTimeout(handle: unknown): void {
    this.#tasks.delete(handle as number);
  }

  advanceBy(delayMs: number): void {
    const target = this.#now + delayMs;
    for (;;) {
      const next = [...this.#tasks.entries()]
        .filter(([, task]) => task.at <= target)
        .sort((a, b) => a[1].at - b[1].at || a[0] - b[0])[0];
      if (next === undefined) break;
      const [id, task] = next;
      this.#tasks.delete(id);
      this.#now = task.at;
      task.callback();
    }
    this.#now = target;
  }

  get pending(): number {
    return this.#tasks.size;
  }
}

interface PendingCall {
  query: string;
  epoch: number;
  signal: AbortSignal;
  resolve: (rows: SearchResultRow[]) => void;
  reject: (error: Error) => void;
}

function harness() {
  const scheduler = new FakeScheduler();
  let state: SearchVerticesState = INITIAL_SEARCH_VERTICES_STATE;
  const dispatch = (action: SearchVerticesAction) => {
    state = searchVerticesReducer(state, action);
  };
  const calls: PendingCall[] = [];
  const run: SearchVerticesRunner = (request, send) => {
    send({ type: "SEARCH_REQUESTED", epoch: request.epoch });
    return new Promise<SearchResultRow[]>((resolve, reject) => {
      calls.push({
        query: request.query,
        epoch: request.epoch,
        signal: request.signal!,
        resolve,
        reject,
      });
    }).then(
      (results) =>
        send({ type: "SEARCH_RECEIVED", epoch: request.epoch, results }),
      (error: Error) =>
        send({
          type: "SEARCH_FAILED",
          epoch: request.epoch,
          error: error.message,
        }),
    );
  };
  const driver = createSearchVerticesDriver({ dispatch, run, scheduler });
  const input = (
    query: string,
    options = DEFAULT_SEARCH_QUERY_OPTIONS,
  ): SearchVerticesInput => ({
    client: {} as SearchVerticesInput["client"],
    query,
    limit: 25,
    options,
  });
  return { scheduler, calls, driver, input, state: () => state };
}

const OLD_ROW: SearchResultRow = {
  key: "old",
  score: 1,
  vertex: null,
};

describe("search vertices latest-input-wins driver", () => {
  for (const outcome of ["success", "error"] as const) {
    it(`invalidates an old ${outcome} before the next debounce expires`, async () => {
      const h = harness();
      h.driver.update(h.input("old"), 100);
      h.scheduler.advanceBy(100);
      expect(h.calls).toHaveLength(1);
      expect(h.state().status).toBe("loading");

      h.driver.update(h.input("new"), 100);
      expect(h.calls[0]!.signal.aborted).toBe(true);
      expect(h.state().query).toBe("new");
      expect(h.state().status).toBe("idle");

      if (outcome === "success") h.calls[0]!.resolve([OLD_ROW]);
      else h.calls[0]!.reject(new Error("late old error"));
      await Promise.resolve();
      expect(h.state().results).toEqual([]);
      expect(h.state().error).toBeNull();

      h.scheduler.advanceBy(99);
      expect(h.calls).toHaveLength(1);
      h.scheduler.advanceBy(1);
      expect(h.calls.map((call) => call.query)).toEqual(["old", "new"]);
    });
  }

  it("clears an empty input synchronously and makes no replacement call", () => {
    const h = harness();
    h.driver.update(h.input("old"), 10);
    h.scheduler.advanceBy(10);

    h.driver.update(h.input(""), 10);
    expect(h.calls[0]!.signal.aborted).toBe(true);
    expect(h.state().query).toBe("");
    expect(h.state().status).toBe("idle");
    expect(h.scheduler.pending).toBe(0);
    h.scheduler.advanceBy(100);
    expect(h.calls).toHaveLength(1);
  });

  it("tracks retry work and invalidates it on newer options or cleanup", async () => {
    const h = harness();
    const firstEpoch = h.driver.update(h.input("alpha beta"), 10);
    h.scheduler.advanceBy(10);
    h.driver.retry(h.input("alpha beta"), firstEpoch);
    expect(h.calls).toHaveLength(2);
    expect(h.calls[0]!.signal.aborted).toBe(true);
    expect(h.state().queryEpoch).toBe(firstEpoch + 1);
    h.calls[0]!.resolve([OLD_ROW]); // cancellation loses, but epoch must win
    await Promise.resolve();
    expect(h.state().results).toEqual([]);

    const nextEpoch = h.driver.update(
      h.input("alpha beta", {
        matchMode: "all",
        phrase: false,
        fuzzy: false,
      }),
      10,
    );
    expect(h.calls[1]!.signal.aborted).toBe(true);
    expect(h.state().queryEpoch).toBe(nextEpoch);
    expect(h.state().options.matchMode).toBe("all");
    expect(h.scheduler.pending).toBe(1);

    h.driver.cancel();
    expect(h.scheduler.pending).toBe(0);
    h.scheduler.advanceBy(100);
    expect(h.calls).toHaveLength(2);
  });
});
