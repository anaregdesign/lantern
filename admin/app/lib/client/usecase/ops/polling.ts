export interface PollingClock {
  schedule(callback: () => void, delayMs: number): () => void;
}

const browserClock: PollingClock = {
  schedule(callback, delayMs) {
    const timer = setTimeout(callback, delayMs);
    return () => clearTimeout(timer);
  },
};

/** Start the next poll only after the preceding round settles. */
export function startPolling(
  round: (signal: AbortSignal) => Promise<unknown>,
  pollMs: number,
  clock: PollingClock = browserClock,
): () => void {
  const controller = new AbortController();
  let cancelTimer: (() => void) | undefined;
  const scheduleNext = () => {
    if (!controller.signal.aborted && pollMs > 0) {
      cancelTimer = clock.schedule(run, pollMs);
    }
  };
  const run = () => {
    if (controller.signal.aborted) return;
    // Each round owns its result/error reporting. A rejected round must not
    // create an unhandled rejection or prevent a later retry.
    void Promise.resolve()
      .then(() => {
        if (!controller.signal.aborted) return round(controller.signal);
      })
      .then(scheduleNext, scheduleNext);
  };
  run();
  return () => {
    controller.abort();
    cancelTimer?.();
  };
}
