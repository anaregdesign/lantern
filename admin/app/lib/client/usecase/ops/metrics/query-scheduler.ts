export const METRICS_QUERY_CONCURRENCY = 4;

interface QueuedQuery {
  start(): void;
}

/** Keep one limit across refreshes, including queries still settling on abort. */
export function createQueryScheduler(limit = METRICS_QUERY_CONCURRENCY) {
  if (!Number.isSafeInteger(limit) || limit < 1)
    throw new Error("A query limit must be a positive integer.");
  let active = 0;
  const queue: QueuedQuery[] = [];
  const drain = () => {
    while (active < limit && queue.length) queue.shift()!.start();
  };

  return function schedule<T>(
    signal: AbortSignal,
    query: () => Promise<T>,
  ): Promise<T> {
    if (signal.aborted) return Promise.reject(signal.reason);
    return new Promise<T>((resolve, reject) => {
      const cancel = () => {
        const index = queue.indexOf(job);
        if (index < 0) return;
        queue.splice(index, 1);
        signal.removeEventListener("abort", cancel);
        reject(signal.reason);
      };
      const job: QueuedQuery = {
        start() {
          signal.removeEventListener("abort", cancel);
          active++;
          void Promise.resolve()
            .then(() => {
              signal.throwIfAborted();
              return query();
            })
            .then(resolve, reject)
            .finally(() => {
              active--;
              drain();
            });
        },
      };
      queue.push(job);
      signal.addEventListener("abort", cancel, { once: true });
      drain();
    });
  };
}
