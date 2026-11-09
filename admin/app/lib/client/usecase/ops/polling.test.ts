import { expect, test } from "bun:test";
import { startPolling, type PollingClock } from "./polling";

function deferred() {
  let resolve!: () => void;
  let reject!: (error: Error) => void;
  const promise = new Promise<void>((yes, no) => {
    resolve = yes;
    reject = no;
  });
  return { promise, resolve, reject };
}

async function settle() {
  for (let i = 0; i < 8; i++) await Promise.resolve();
}

function clock() {
  let now = 0;
  const timers = new Map<number, { at: number; callback(): void }>();
  let id = 0;
  return {
    schedule(callback: () => void, delayMs: number) {
      const timer = ++id;
      timers.set(timer, { at: now + delayMs, callback });
      return () => void timers.delete(timer);
    },
    advance(ms: number) {
      now += ms;
      for (const [timer, job] of timers) {
        if (job.at <= now) {
          timers.delete(timer);
          job.callback();
        }
      }
    },
    pending: () => timers.size,
  } satisfies PollingClock & {
    advance(ms: number): void;
    pending(): number;
  };
}

test("a slow round cannot overlap timer polls; the delay starts after completion", async () => {
  const time = clock();
  const first = deferred();
  let calls = 0;
  const stop = startPolling(
    () => {
      calls++;
      return first.promise;
    },
    5000,
    time,
  );
  await settle();
  expect(calls).toBe(1);
  time.advance(20_000);
  await settle();
  expect(calls).toBe(1);
  expect(time.pending()).toBe(0);
  first.resolve();
  await settle();
  expect(time.pending()).toBe(1);
  time.advance(4999);
  await settle();
  expect(calls).toBe(1);
  time.advance(1);
  await settle();
  expect(calls).toBe(2);
  stop();
  expect(time.pending()).toBe(0);
});

test("stopping cancels an active round and prevents late settlement from restarting it", async () => {
  const time = clock();
  const pending = deferred();
  let signal!: AbortSignal;
  const stop = startPolling(
    (value) => {
      signal = value;
      return pending.promise;
    },
    5000,
    time,
  );
  await settle();
  stop();
  expect(signal.aborted).toBe(true);
  pending.resolve();
  await settle();
  time.advance(10_000);
  expect(time.pending()).toBe(0);
});

test("polling off still fetches once, including a rejected round", async () => {
  const time = clock();
  let calls = 0;
  const stop = startPolling(
    async () => {
      calls++;
      throw new Error("reported by the round");
    },
    0,
    time,
  );
  await settle();
  time.advance(10_000);
  expect(calls).toBe(1);
  expect(time.pending()).toBe(0);
  stop();
});

test("repeated refresh/range/endpoint replacements cannot resurrect old timers", async () => {
  const time = clock();
  const oldRounds = [deferred(), deferred(), deferred()];
  for (const old of oldRounds) {
    const stop = startPolling(() => old.promise, 5000, time);
    await settle();
    stop();
  }
  let calls = 0;
  const stop = startPolling(
    async () => {
      calls++;
    },
    5000,
    time,
  );
  await settle();
  for (const old of oldRounds) old.resolve();
  await settle();
  expect(time.pending()).toBe(1);
  time.advance(5000);
  await settle();
  expect(calls).toBe(2);
  expect(time.pending()).toBe(1);
  stop();
});
