import type { SearchVerticesAction } from "./reducer";
import type { FetchSearchResultsInput } from "./handlers";
import type { SearchQueryOptions } from "./state";

export interface SearchVerticesInput {
  client: FetchSearchResultsInput["client"];
  query: string;
  limit: number;
  prefix?: string;
  options: SearchQueryOptions;
}

export interface SearchVerticesScheduler {
  setTimeout(callback: () => void, delayMs: number): unknown;
  clearTimeout(handle: unknown): void;
}

export type SearchVerticesRunner = (
  input: FetchSearchResultsInput,
  dispatch: (action: SearchVerticesAction) => void,
) => Promise<void>;

export interface SearchVerticesDriver {
  /** Invalidates old work immediately and debounces only the new RPC start. */
  update(input: SearchVerticesInput, debounceMs: number): number;
  /** Re-runs the current input immediately when its epoch is still live. */
  retry(input: SearchVerticesInput, epoch: number): void;
  /** Cancels timers, primary calls, and retries owned by this lifecycle. */
  cancel(): void;
}

interface CreateSearchVerticesDriverOptions {
  dispatch: (action: SearchVerticesAction) => void;
  run: SearchVerticesRunner;
  scheduler: SearchVerticesScheduler;
}

/**
 * Owns latest-input-wins orchestration independently of React rendering.
 * Keeping timer and AbortController ownership here makes input invalidation
 * synchronous and lets tests drive the debounce with a deterministic clock.
 */
export function createSearchVerticesDriver({
  dispatch,
  run,
  scheduler,
}: CreateSearchVerticesDriverOptions): SearchVerticesDriver {
  let epoch = 0;
  let timer: unknown;
  const active = new Set<AbortController>();

  const clearTimer = () => {
    if (timer === undefined) return;
    scheduler.clearTimeout(timer);
    timer = undefined;
  };

  const abortActive = () => {
    for (const controller of active) controller.abort();
    active.clear();
  };

  const start = (input: SearchVerticesInput, expectedEpoch: number) => {
    if (expectedEpoch !== epoch) return;
    const controller = new AbortController();
    active.add(controller);
    void run(
      { ...input, epoch: expectedEpoch, signal: controller.signal },
      dispatch,
    ).finally(() => active.delete(controller));
  };

  return {
    update(input, debounceMs) {
      epoch++;
      const nextEpoch = epoch;
      clearTimer();
      abortActive();
      dispatch({
        type: "INPUT_CHANGED",
        query: input.query,
        options: input.options,
        epoch: nextEpoch,
      });

      if (input.query.length === 0) return nextEpoch;
      timer = scheduler.setTimeout(() => {
        timer = undefined;
        start(input, nextEpoch);
      }, debounceMs);
      return nextEpoch;
    },

    retry(input, expectedEpoch) {
      if (input.query.length === 0 || expectedEpoch !== epoch) return;
      epoch++;
      const retryEpoch = epoch;
      clearTimer();
      abortActive();
      // A retry is a distinct attempt. Advancing the reducer epoch prevents
      // an aborted predecessor that still fulfills from overwriting it.
      dispatch({
        type: "INPUT_CHANGED",
        query: input.query,
        options: input.options,
        epoch: retryEpoch,
      });
      start(input, retryEpoch);
    },

    cancel() {
      epoch++;
      clearTimer();
      abortActive();
    },
  };
}

export const browserSearchVerticesScheduler: SearchVerticesScheduler = {
  setTimeout: (callback, delayMs) => window.setTimeout(callback, delayMs),
  clearTimeout: (handle) => window.clearTimeout(handle as number),
};
