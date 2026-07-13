import { FailedPreconditionError, SearchErrorReason, type Lantern } from "lantern-sdk";

/**
 * Maintained SearchVertices contract example. `bun run example:typecheck`
 * compiles this file together with the SDK and the executable main example.
 */
export async function runSearchExamples(client: Lantern): Promise<void> {
  const status = await client.getServerStatus();
  const capability = status.search;
  console.log(
    `search enabled=${capability?.enabled ?? false} positions=${capability?.positionsEnabled ?? false} ` +
      `analyzer=${capability?.analyzerVersion ?? "unknown"} ` +
      `projection=${capability?.projectionVersion ?? "unknown"} ` +
      `fingerprint=${capability?.configFingerprint ?? "unknown"}`,
  );

  // One-shot search, scoped to a key namespace and with explicit membership.
  // A disabled index is a typed UI state, not a message-string comparison.
  try {
    const hits = await client.searchVertices("A", {
      prefix: "string",
      limit: 10,
      matchMode: "all",
    });
    console.log(`one-shot search returned ${hits.length} hits`);
  } catch (error) {
    if (
      error instanceof FailedPreconditionError &&
      error.reason === SearchErrorReason.SEARCH_DISABLED
    ) {
      console.log("search is disabled on this endpoint");
      return;
    }
    throw error;
  }

  // Phrase and fuzzy expansion are separate because those options do not
  // compose. Capability discovery prevents a known phrase precondition error.
  if (capability?.positionsEnabled) {
    await client.searchVertices("quiet cafe", { phrase: true });
  }
  await client.searchVertices("serach", { fuzziness: 1 });

  // This iterator follows an endpoint-sticky bounded snapshot lazily and asks
  // for the exact value/TTL snapshot selected with each score.
  for await (const hit of client.searchVerticesIter("A", {
    limit: 2,
    projection: "full-vertex",
  })) {
    console.log("paged search hit", hit.key, hit.score, hit.vertex);
  }

  // AbortSignal cancellation is terminal and returns no partial page.
  const controller = new AbortController();
  controller.abort();
  try {
    await client.searchVertices("A", {}, controller.signal);
  } catch (error) {
    console.log("cancelled search", error);
  }

  // Incremental search forwards the same one-shot options, cancels superseded
  // calls, and publishes only the newest input.
  const incremental = client.incrementalSearch({
    debounceMs: 0,
    limit: 10,
    prefixTerms: true,
  });
  const iterator = incremental[Symbol.asyncIterator]();
  incremental.search("lan");
  const update = await iterator.next();
  if (!update.done) {
    if (update.value.error) throw update.value.error;
    console.log(`incremental query=${update.value.query} hits=${update.value.hits.length}`);
  }
  incremental.close();
}
