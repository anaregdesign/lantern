import { expect, test } from "bun:test";
import {
  createQueryScheduler,
  METRICS_QUERY_CONCURRENCY,
} from "./query-scheduler";

function deferred<T>() {
  let resolve!: (value: T) => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<T>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

async function settle() {
  for (let i = 0; i < 10; i++) await Promise.resolve();
}

test("catalog queries stay bounded and a failed query releases its slot", async () => {
  const schedule = createQueryScheduler();
  const controller = new AbortController();
  const queries = Array.from({ length: 12 }, () => deferred<number>());
  let active = 0;
  let maximum = 0;
  const started: number[] = [];
  const results = Promise.allSettled(
    queries.map((query, index) =>
      schedule(controller.signal, async () => {
        started.push(index);
        maximum = Math.max(maximum, ++active);
        try {
          return await query.promise;
        } finally {
          active--;
        }
      }),
    ),
  );
  await settle();
  expect(started).toHaveLength(METRICS_QUERY_CONCURRENCY);
  queries[0].reject(new Error("one panel is unavailable"));
  await settle();
  expect(started).toHaveLength(METRICS_QUERY_CONCURRENCY + 1);
  for (let i = 1; i < queries.length; i++) queries[i].resolve(i);
  const finished = await results;
  expect(finished[0].status).toBe("rejected");
  expect(finished.slice(1)).toEqual(
    queries.slice(1).map((_, index) => ({
      status: "fulfilled",
      value: index + 1,
    })),
  );
  expect(maximum).toBe(METRICS_QUERY_CONCURRENCY);
});

test("cancellation removes queued work while settling requests keep the shared limit", async () => {
  const schedule = createQueryScheduler(2);
  const old = new AbortController();
  const oldQueries = [deferred<number>(), deferred<number>()];
  const started: string[] = [];
  const stale = Promise.allSettled(
    Array.from({ length: 7 }, (_, index) =>
      schedule(old.signal, async () => {
        started.push(`old-${index}`);
        return oldQueries[index].promise;
      }),
    ),
  );
  await settle();
  old.abort();
  // These deliberately ignore abort to model requests still settling during
  // repeated refresh/range/endpoint changes.
  const current = new AbortController();
  const fresh = Promise.all(
    [0, 1, 2].map((index) =>
      schedule(current.signal, async () => {
        started.push(`new-${index}`);
        return index;
      }),
    ),
  );
  await settle();
  expect(started).toEqual(["old-0", "old-1"]);
  oldQueries[0].resolve(0);
  oldQueries[1].resolve(1);
  expect(await fresh).toEqual([0, 1, 2]);
  expect(started).toEqual(["old-0", "old-1", "new-0", "new-1", "new-2"]);
  const outcomes = await stale;
  expect(
    outcomes.slice(2).every((result) => result.status === "rejected"),
  ).toBe(true);
});

test("already cancelled work never starts and invalid limits fail immediately", async () => {
  const controller = new AbortController();
  controller.abort();
  let called = false;
  const outcomes = await Promise.allSettled([
    createQueryScheduler()(controller.signal, async () => {
      called = true;
    }),
  ]);
  expect(outcomes[0].status).toBe("rejected");
  expect(called).toBe(false);
  for (const limit of [0, -1, 1.5, Number.NaN])
    expect(() => createQueryScheduler(limit)).toThrow();
});
