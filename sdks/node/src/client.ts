/**
 * Promise-based Connect client for the Lantern service.
 *
 * Two entrypoints, one client class:
 *
 *   - `import { Lantern } from "lantern-sdk";`
 *     `Lantern.connect("http://host:6380")` — Node h2c (HTTP/2)
 *     via `@connectrpc/connect-node`.
 *   - `import { Lantern } from "lantern-sdk/web";`
 *     `Lantern.connectWeb("http://host:6380")` — browser fetch
 *     (HTTP/1.1) via `@connectrpc/connect-web`. No
 *     `@connectrpc/connect-node` code is pulled into the browser
 *     bundle.
 *
 * Advanced consumers can inject any `@connectrpc/connect` `Transport`
 * directly via `Lantern.withTransport(transport)` — useful for tests
 * with a transport mock or for embedding the client in a host that
 * already owns the transport (e.g. an Electron renderer).
 *
 * Batch helpers (`putVertices`, `addEdges`, `putEdges`,
 * `deleteVertices`, `deleteEdges`) auto-chunk at
 * `ConnectOptions.batchChunkSize` (default 1000) and throw
 * `BatchError` with a resumable `written` offset on partial failure.
 *
 * Wire: Connect protocol (Connect/JSON by default; flip to binary via
 * `transportOptions.useBinaryFormat`). Built on
 * @connectrpc/connect v2 + codegen via @bufbuild/protoc-gen-es
 * (single plugin emits both message classes and the service schema
 * descriptor `LanternService`).
 */

import { type Client, type Interceptor, type Transport, createClient } from "@connectrpc/connect";
import { fromJson, toJson, type JsonValue } from "@bufbuild/protobuf";

import {
  EdgeSchema,
  LanternService,
  MatchMode as PbMatchMode,
  Objective as PbObjective,
  Reduction as PbReduction,
  ScanOrder as PbScanOrder,
  Weighting as PbWeighting,
  VertexSchema,
  type Edge as PbEdge,
  type GetReplicationStatusResponse,
  type GetServerStatusResponse,
  type Vertex as PbVertex,
} from "./gen/graph/v1/graph_pb.js";

import {
  BatchError,
  InvalidArgumentError,
  LanternError,
  NotFoundError,
  wrapConnectError,
} from "./errors.js";
import {
  Reduction,
  Objective,
  Weighting,
  fromEdgeJson,
  fromVertexJson,
  toEdgeJson,
  toVertexJson,
  edgeRecordToJson,
  vertexRecordToJson,
  type Edge,
  type EdgeInput,
  type Graph,
  type SearchHit,
  type Vertex,
  type VertexInput,
} from "./values.js";
import {
  type ConnectOptions,
  DEFAULT_BATCH_CHUNK_SIZE,
  type DeleteByPrefixOptions,
  type DeleteEdgesByPrefixOptions,
  type EdgeScanOptions,
  type IlluminateOptions,
  type MatchMode,
  type ScanOptions,
  type SearchOptions,
} from "./options.js";
import {
  createIncrementalSearch,
  type IncrementalSearch,
  type IncrementalSearchOptions,
} from "./incremental-search.js";
import { contribIdFrom, makeNonce, validateContribId } from "./contrib.js";
import { decayContributions, type DecayOptions } from "./decay.js";
import {
  DEFAULT_RESTORE_CHUNK_SIZE,
  type BackupRecord,
  type RestoreSource,
  type RestoreStats,
} from "./backup.js";
import { pingHealth, type PingOptions } from "./health.js";

/**
 * Upper bound for the auto-chunk size. Contrib-ID idempotency keys (#895)
 * pack a uint16 per-chunk index into their low bytes, so a chunk larger than
 * 65536 edges would wrap index 65536 back to 0 and collide two contributions
 * under one sequence — silently dropping a weight. chunkSize() clamps to this
 * (mirrors the Go SDK's maxBatchChunkSize).
 */
const MAX_BATCH_CHUNK_SIZE = 1 << 16;

/** Maps the SDK's string match mode onto the wire enum. */
function toPbMatchMode(m: MatchMode | undefined): PbMatchMode {
  switch (m) {
    case "all":
      return PbMatchMode.ALL;
    case "min-should":
      return PbMatchMode.MIN_SHOULD;
    case undefined:
      return PbMatchMode.UNSPECIFIED;
    default:
      return PbMatchMode.ANY;
  }
}

const MAX_UINT32 = 0xffff_ffff;

function validateSearchInteger(name: string, value: number | undefined, max = MAX_UINT32): void {
  if (value === undefined) return;
  if (!Number.isFinite(value) || !Number.isInteger(value) || value < 0 || value > max) {
    throw new InvalidArgumentError(
      `search: ${name} must be an integer in [0, ${max}]; got ${value}`,
    );
  }
}

function validateSearchOptions(opts: SearchOptions): void {
  validateSearchInteger("limit", opts.limit);
  validateSearchInteger("minShouldMatch", opts.minShouldMatch);
  validateSearchInteger("fuzziness", opts.fuzziness, 2);
  if (
    opts.matchMode !== undefined &&
    opts.matchMode !== "any" &&
    opts.matchMode !== "all" &&
    opts.matchMode !== "min-should"
  ) {
    throw new InvalidArgumentError(`search: unrecognized matchMode ${String(opts.matchMode)}`);
  }
  if ((opts.minShouldMatch ?? 0) !== 0 && opts.matchMode !== "min-should") {
    throw new InvalidArgumentError('search: minShouldMatch requires matchMode "min-should"');
  }
  if (!opts.phrase) return;
  if (opts.matchMode !== undefined) {
    throw new InvalidArgumentError("search: phrase cannot be combined with an explicit matchMode");
  }
  if ((opts.minShouldMatch ?? 0) !== 0) {
    throw new InvalidArgumentError("search: phrase cannot be combined with minShouldMatch");
  }
  if ((opts.fuzziness ?? 0) !== 0) {
    throw new InvalidArgumentError("search: phrase cannot be combined with fuzziness");
  }
  if (opts.prefixTerms === true) {
    throw new InvalidArgumentError("search: phrase cannot be combined with prefixTerms");
  }
}

/**
 * Maps the SDK scan order ("asc" | "desc" | undefined) to the wire ScanOrder
 * enum (#898). Undefined and "asc" both send SCAN_ORDER_ASC so a paginated
 * scan's order is explicit on every page and the server's order-bound cursor
 * check has a concrete value to compare against.
 */
function toPbScanOrder(order: "asc" | "desc" | undefined): PbScanOrder {
  return order === "desc" ? PbScanOrder.DESC : PbScanOrder.ASC;
}

/**
 * Builds the wire SearchOptions from the SDK options, returning undefined when
 * no relevance option is set so the server applies its configured defaults
 * (matching the Go SDK: an explicit matchMode — even "any" — is sent literally).
 */
function buildSearchOptions(opts: SearchOptions) {
  const advanced =
    opts.matchMode !== undefined ||
    (opts.minShouldMatch ?? 0) > 0 ||
    (opts.fuzziness ?? 0) > 0 ||
    opts.prefixTerms === true ||
    opts.phrase === true;
  if (!advanced) return undefined;
  return {
    matchMode: toPbMatchMode(opts.matchMode),
    minShouldMatch: opts.minShouldMatch ?? 0,
    phrase: opts.phrase ?? false,
    fuzziness: opts.fuzziness ?? 0,
    prefixTerms: opts.prefixTerms ?? false,
  };
}

/**
 * Constructor arguments for `Lantern.connect` /
 * `Lantern.connectWeb`. The HTTP base URL is the only required field;
 * everything else either has a sensible default or is an opt-in
 * tuning knob.
 */
export interface LanternArgs {
  options?: ConnectOptions;
  /**
   * Bearer token for servers running with LANTERN_AUTH_TOKENS (#850).
   * Attached as `Authorization: Bearer <token>` on every call via a
   * Connect interceptor prepended ahead of `interceptors`. Bearer tokens
   * over plaintext h2c are sniffable — use an https:// baseUrl outside
   * trusted networks.
   */
  token?: string;
  /**
   * Optional list of Connect interceptors run on every unary call.
   * The order matches @connectrpc/connect's `Interceptor[]` chain —
   * the first entry sees outgoing requests first and responses last.
   */
  interceptors?: Interceptor[];
  /**
   * Override the transport options forwarded to
   * `createConnectTransport`. `baseUrl` is filled in from the
   * constructor arg and cannot be overridden here. The shape is the
   * underlying `@connectrpc/connect-node` / `@connectrpc/connect-web`
   * options object minus `baseUrl`; passed through verbatim so
   * consumers can flip `useBinaryFormat`, override the fetch
   * implementation, etc.
   */
  transportOptions?: Record<string, unknown>;
}

// Connect-es v2 enum values match the proto numeric IDs verbatim — no
// translation needed beyond the type cast. Per #846 the traversal family
// is a oneof selected by the options object shape; the enums below are
// flat numeric axes.
const REDUCTION_TO_PB: Record<number, PbReduction> = {
  [Reduction.UNSPECIFIED]: PbReduction.UNSPECIFIED,
  [Reduction.MINIMUM_SPANNING_TREE]: PbReduction.MINIMUM_SPANNING_TREE,
  [Reduction.SHORTEST_PATH_TREE]: PbReduction.SHORTEST_PATH_TREE,
};
const OBJECTIVE_TO_PB: Record<number, PbObjective> = {
  [Objective.UNSPECIFIED]: PbObjective.UNSPECIFIED,
  [Objective.MINIMIZE]: PbObjective.MINIMIZE,
  [Objective.MAXIMIZE]: PbObjective.MAXIMIZE,
};
const WEIGHTING_TO_PB: Record<number, PbWeighting> = {
  [Weighting.UNSPECIFIED]: PbWeighting.UNSPECIFIED,
  [Weighting.RAW]: PbWeighting.RAW,
  [Weighting.TFIDF]: PbWeighting.TFIDF,
  [Weighting.BM25]: PbWeighting.BM25,
};

/**
 * Build the client-side bearer interceptor behind `LanternArgs.token`
 * (#850). Exported for the entrypoints; not part of the public API
 * surface beyond them.
 */
export function authTokenInterceptor(token: string): Interceptor {
  return (next) => (req) => {
    req.header.set("Authorization", `Bearer ${token}`);
    return next(req);
  };
}

/** Prepend the token interceptor (when configured) ahead of user interceptors. */
export function withTokenInterceptors(args: LanternArgs): Interceptor[] | undefined {
  if (!args.token) return args.interceptors;
  return [authTokenInterceptor(args.token), ...(args.interceptors ?? [])];
}

function normaliseBaseUrl(caller: string, baseUrl: string): string {
  if (!baseUrl) {
    throw new Error(`${caller}: baseUrl is required`);
  }
  if (!/^https?:\/\//.test(baseUrl)) {
    throw new Error(
      `${caller}: baseUrl must include scheme (http:// or https://); got ${JSON.stringify(baseUrl)}`,
    );
  }
  return baseUrl.replace(/\/$/, "");
}

export { normaliseBaseUrl };

/**
 * Promise-based Lantern client built on Connect-Node v2.
 *
 * Construct with `Lantern.connect("http://host:6380")`. All methods
 * return Promises; pass `signal` for AbortController-driven
 * cancellation.
 *
 * Batch helpers auto-chunk at `ConnectOptions.batchChunkSize`
 * (default 1000) and throw `BatchError` with a resumable `written`
 * offset on partial failure.
 */
export class Lantern {
  private readonly client: Client<typeof LanternService>;
  private readonly options: ConnectOptions;
  /**
   * The normalised base URL (`http(s)://host:port`, no trailing slash) the
   * client was built with, or undefined when constructed via
   * {@link Lantern.withTransport} without one. Only `ping()` needs it — it
   * POSTs a Connect+JSON Health/Check to the gRPC-Health-v1 endpoint that
   * rides the same listener, which the typed `client` does not expose.
   */
  private readonly baseUrl?: string;
  /** Lazily-minted per-client nonce seeding automatic contrib IDs (#895). */
  private nonce: Uint8Array | null = null;
  /** Monotonic per-call sequence folded into automatic contrib IDs (#895). */
  private callSeq = 0n;

  private constructor(
    client: Client<typeof LanternService>,
    options: ConnectOptions,
    baseUrl?: string,
  ) {
    this.client = client;
    this.options = options;
    this.baseUrl = baseUrl;
  }

  /**
   * Construct a Lantern client around any pre-built
   * `@connectrpc/connect` `Transport`. Most callers should use the
   * entrypoint-specific `connect()` (Node) or `connectWeb()`
   * (browser) helpers instead — those are thin wrappers that pick
   * the right transport package for their runtime so the unused one
   * stays out of the bundle.
   *
   * Use `withTransport()` directly when:
   *   - the host already owns the transport (Electron renderer with
   *     a custom fetch pipeline, a worker thread sharing a parent's
   *     transport, …),
   *   - the test injects a mock transport,
   *   - the runtime is unsupported by the bundled transport helpers.
   *
   * `baseUrl` is optional and used only by `ping()`, which POSTs a
   * Connect+JSON Health/Check outside the typed transport (the
   * gRPC-Health-v1 surface is not part of `LanternService`). The
   * `connect()` / `connectWeb()` helpers fill it in automatically;
   * a `withTransport` client built without one throws from `ping()`.
   */
  static withTransport(
    transport: Transport,
    options: ConnectOptions = {},
    baseUrl?: string,
  ): Lantern {
    return new Lantern(
      createClient(LanternService, transport),
      {
        batchChunkSize: DEFAULT_BATCH_CHUNK_SIZE,
        ...options,
      },
      baseUrl,
    );
  }

  /**
   * Releases the Node http2.Session pool the Connect transport owns.
   * @connectrpc/connect-node v2 does not expose a public close hook;
   * the session pool is torn down by GC and by node's natural
   * shutdown. Kept for API parity with consumers that wrap close()
   * in a `try { ... } finally { client.close(); }` block.
   */
  close(): void {
    // No-op until @connectrpc/connect-node surfaces a public
    // `transport.disconnect()` method. The Node event loop closes
    // h2 sessions on process exit naturally.
  }

  // --- Vertex unary RPCs ---

  async getVertex(key: string, signal?: AbortSignal): Promise<Vertex> {
    return this.invoke(async () => {
      const resp = await this.client.getVertex({ key }, this.callOpts(signal));
      if (!resp.vertex) {
        throw new NotFoundError(`vertex not found: ${key}`);
      }
      return fromVertexJson(toJson(VertexSchema, resp.vertex) as Record<string, unknown>);
    });
  }

  async putVertex(input: VertexInput, signal?: AbortSignal): Promise<void> {
    const vertex = fromJson(VertexSchema, toVertexJson(input) as JsonValue);
    return this.invoke(async () => {
      await this.client.putVertex({ vertex }, this.callOpts(signal));
    });
  }

  /**
   * Conditionally upserts a single vertex, applying the write only when no
   * live vertex already exists at its key (SET NX, #896). Resolves to `true`
   * when the write landed and `false` when an existing live vertex left the
   * stored value and expiration untouched. "Live" follows the server's #750
   * visibility rule, so an expired-but-uncollected vertex does not block the
   * write. The server performs the existence check and the store atomically,
   * closing the check-then-act race a getVertex-then-putVertex sequence has.
   */
  async putVertexIfAbsent(input: VertexInput, signal?: AbortSignal): Promise<boolean> {
    const vertex = fromJson(VertexSchema, toVertexJson(input) as JsonValue);
    return this.invoke(async () => {
      const resp = await this.client.putVertex({ vertex, ifAbsent: true }, this.callOpts(signal));
      return resp.written;
    });
  }

  async deleteVertex(key: string, signal?: AbortSignal): Promise<boolean> {
    return this.invoke(async () => {
      const resp = await this.client.deleteVertex({ key }, this.callOpts(signal));
      return resp.existed;
    });
  }

  async getVertices(
    keys: readonly string[],
    signal?: AbortSignal,
  ): Promise<{ found: Vertex[]; missing: string[] }> {
    if (keys.length === 0) return { found: [], missing: [] };
    const found: Vertex[] = [];
    const missing: string[] = [];
    await this.runBatchRead(keys, async (chunk) => {
      const resp = await this.client.getVertices({ keys: chunk }, this.callOpts(signal));
      for (const v of resp.vertices) {
        found.push(fromVertexJson(toJson(VertexSchema, v) as Record<string, unknown>));
      }
      for (const m of resp.missing) missing.push(m);
    });
    return { found, missing };
  }

  async putVertices(inputs: readonly VertexInput[], signal?: AbortSignal): Promise<void> {
    if (inputs.length === 0) return;
    await this.runBatchWrite(inputs, async (chunk) => {
      const vertices = chunk.map((vi) => fromJson(VertexSchema, toVertexJson(vi) as JsonValue));
      await this.client.putVertices({ vertices }, this.callOpts(signal));
    });
  }

  /**
   * Conditionally upserts a batch of vertices, applying each write only when
   * no live vertex already exists at its key (SET NX, #896). Large batches are
   * automatically chunked according to the client's batch-chunk size.
   *
   * Resolves to `written` — the number of vertices actually stored — and
   * `skippedKeys` — the keys left untouched because a live vertex already
   * existed there (summed/collected across chunks). "Live" follows the
   * server's #750 visibility rule. Each key's existence check and store happen
   * atomically server-side. On partial failure it throws a `BatchError` whose
   * `written` field records the number of inputs in the fully committed
   * chunks, so callers can resume with `inputs.slice(err.written)`.
   */
  async putVerticesIfAbsent(
    inputs: readonly VertexInput[],
    signal?: AbortSignal,
  ): Promise<{ written: number; skippedKeys: string[] }> {
    if (inputs.length === 0) return { written: 0, skippedKeys: [] };
    let written = 0;
    const skippedKeys: string[] = [];
    await this.runBatchWrite(inputs, async (chunk) => {
      const vertices = chunk.map((vi) => fromJson(VertexSchema, toVertexJson(vi) as JsonValue));
      const resp = await this.client.putVertices(
        { vertices, ifAbsent: true },
        this.callOpts(signal),
      );
      written += resp.written;
      for (const k of resp.skippedKeys) skippedKeys.push(k);
    });
    return { written, skippedKeys };
  }

  async deleteVertices(keys: readonly string[], signal?: AbortSignal): Promise<number> {
    if (keys.length === 0) return 0;
    let total = 0;
    await this.runBatchWrite(keys, async (chunk) => {
      const resp = await this.client.deleteVertices({ keys: chunk }, this.callOpts(signal));
      total += resp.deleted;
    });
    return total;
  }

  async scanVertices(
    prefix: string,
    opts: ScanOptions = {},
    signal?: AbortSignal,
  ): Promise<{ vertices: Vertex[]; nextCursor: Uint8Array }> {
    return this.invoke(async () => {
      const resp = await this.client.scanVertices(
        {
          prefix,
          limit: opts.limit ?? 0,
          cursor: opts.cursor ?? new Uint8Array(),
          order: toPbScanOrder(opts.order),
        },
        this.callOpts(signal),
      );
      return {
        vertices: resp.vertices.map((v) =>
          fromVertexJson(toJson(VertexSchema, v) as Record<string, unknown>),
        ),
        nextCursor: resp.nextCursor,
      };
    });
  }

  /**
   * Async-iterable form of {@link scanVertices} that pages through the whole
   * prefix range. `batchSize` is the per-call limit (0 = server default). Pass
   * `order` ("asc" default, "desc" for high-to-low) to iterate the range in
   * either direction (#898).
   */
  async *scanVerticesAll(
    prefix: string,
    batchSize?: number,
    signal?: AbortSignal,
    order?: "asc" | "desc",
  ): AsyncIterable<Vertex[]> {
    let cursor: Uint8Array = new Uint8Array();
    while (true) {
      if (signal?.aborted) throw new LanternError("scanVerticesAll aborted");
      const page = await this.scanVertices(
        prefix,
        { limit: batchSize ?? 0, cursor, order },
        signal,
      );
      yield page.vertices;
      if (page.nextCursor.length === 0) return;
      cursor = page.nextCursor;
    }
  }

  /**
   * Lists vertex KEYS under a prefix — the keys-only, wire-efficient
   * counterpart to `scanVertices` (no vertex values are transferred),
   * backing the Redis-familiar `keys` CLI verb.
   *
   * Unlike `scanVertices`, a non-empty `prefix` is REQUIRED: the server
   * rejects an empty prefix with `invalid_argument`. The returned cursor
   * is its own opaque kind and is NOT interchangeable with
   * `scanVertices` / `scanEdges` cursors.
   */
  async scanVertexKeys(
    prefix: string,
    opts: ScanOptions = {},
    signal?: AbortSignal,
  ): Promise<{ keys: string[]; nextCursor: Uint8Array }> {
    return this.invoke(async () => {
      const resp = await this.client.scanVertexKeys(
        {
          prefix,
          limit: opts.limit ?? 0,
          cursor: opts.cursor ?? new Uint8Array(),
          order: toPbScanOrder(opts.order),
        },
        this.callOpts(signal),
      );
      return { keys: resp.keys, nextCursor: resp.nextCursor };
    });
  }

  /**
   * Async-iterable form of {@link scanVertexKeys} that pages through the
   * whole prefix range. `batchSize` is the per-call limit (0 = server
   * default). A non-empty `prefix` is REQUIRED. Pass `order` ("asc" default,
   * "desc" for high-to-low) to iterate the range in either direction (#898).
   */
  async *scanVertexKeysAll(
    prefix: string,
    batchSize?: number,
    signal?: AbortSignal,
    order?: "asc" | "desc",
  ): AsyncIterable<string[]> {
    let cursor: Uint8Array = new Uint8Array();
    while (true) {
      if (signal?.aborted) throw new LanternError("scanVertexKeysAll aborted");
      const page = await this.scanVertexKeys(
        prefix,
        { limit: batchSize ?? 0, cursor, order },
        signal,
      );
      yield page.keys;
      if (page.nextCursor.length === 0) return;
      cursor = page.nextCursor;
    }
  }

  /**
   * Content-addressed vertex search. Runs a relevance-ranked full-text
   * query over vertex *content* (key + value) via the server-side
   * index, returning ranked `{ key, score }` hits in stable `(score DESC,
   * raw key ASC)` order — the seed candidates a caller picks before an
   * `illuminate` traversal, where `score` doubles as the seed's initial
   * weight.
   *
   * Unlike `scanVertices` (a lexicographic key-prefix walk), this
   * matches the *value*. `opts.limit` caps the ranked hits (0 = server
   * default; the server also enforces a hard maximum); `opts.prefix`
   * composes content relevance with a key-prefix namespace scope.
   *
   * Hits carry only the key + score: the value and TTL are omitted, so
   * callers that need them issue a follow-up `getVertices` with the
   * returned keys, preserving rank order. Be defensive about a ranked
   * key that no longer resolves (TTL expiry racing the follow-up read).
   *
   * An empty or unanalysable query yields `[]` (not an error). When the
   * server-side index is disabled (`LANTERN_SEARCH_ENABLED=false`) the
   * call rejects with `FailedPreconditionError`, so callers can render a
   * calm "search not enabled" state rather than treating it as a hard
   * failure.
   */
  async searchVertices(
    query: string,
    opts: SearchOptions = {},
    signal?: AbortSignal,
  ): Promise<SearchHit[]> {
    return this.invoke(async () => {
      validateSearchOptions(opts);
      const resp = await this.client.searchVertices(
        {
          query,
          limit: opts.limit ?? 0,
          prefix: opts.prefix ?? "",
          options: buildSearchOptions(opts),
        },
        this.callOpts(signal),
      );
      return resp.hits.map((h) => ({ key: h.key, score: h.score }));
    });
  }

  /**
   * Opens a search-as-you-type driver over {@link searchVertices}: push a
   * query with `search` on each keystroke, then receive ranked results via
   * `subscribe` or by `for await`-ing the returned driver. It debounces rapid
   * input, aborts the previous in-flight RPC when a newer query arrives, and
   * drops a late reply from a superseded query, so a consumer only ever
   * observes the latest query's result. Pure client-side orchestration over
   * the existing RPC — no extra wire surface.
   *
   * Errors (including a `FailedPreconditionError` when the server-side index
   * is disabled) arrive as `SearchUpdate.error` rather than rejecting. Call
   * `close()` when done to stop the driver and abort any in-flight call.
   */
  incrementalSearch(opts: IncrementalSearchOptions = {}): IncrementalSearch {
    return createIncrementalSearch(
      (query, searchOpts, signal) => this.searchVertices(query, searchOpts, signal),
      opts,
    );
  }

  async countVerticesByPrefix(prefix: string, signal?: AbortSignal): Promise<bigint> {
    return this.invoke(async () => {
      const resp = await this.client.countVerticesByPrefix({ prefix }, this.callOpts(signal));
      return resp.count;
    });
  }

  async deleteVerticesByPrefix(
    prefix: string,
    opts: DeleteByPrefixOptions = {},
    signal?: AbortSignal,
  ): Promise<bigint> {
    return this.invoke(async () => {
      const resp = await this.client.deleteVerticesByPrefix(
        {
          prefix,
          limit: opts.limit ?? 0,
          dryRun: opts.dryRun ?? false,
        },
        this.callOpts(signal),
      );
      return resp.deleted;
    });
  }

  // --- Edge unary RPCs ---

  async getEdge(tail: string, head: string, signal?: AbortSignal): Promise<Edge> {
    return this.invoke(async () => {
      const resp = await this.client.getEdge({ tail, head }, this.callOpts(signal));
      if (!resp.edge) {
        throw new NotFoundError(`edge not found: ${tail} -> ${head}`);
      }
      return fromEdgeJson(toJson(EdgeSchema, resp.edge) as Record<string, unknown>);
    });
  }

  async addEdge(input: EdgeInput, signal?: AbortSignal): Promise<number> {
    const edge = fromJson(EdgeSchema, toEdgeJson(input) as JsonValue);
    const contribId = this.singleContribId(input);
    return this.invoke(async () => {
      const resp = await this.client.addEdge(
        contribId ? { edge, contribId } : { edge },
        this.callOpts(signal),
      );
      return resp.effectiveWeight;
    });
  }

  async putEdge(input: EdgeInput, signal?: AbortSignal): Promise<void> {
    const edge = fromJson(EdgeSchema, toEdgeJson(input) as JsonValue);
    return this.invoke(async () => {
      await this.client.putEdge({ edge }, this.callOpts(signal));
    });
  }

  async deleteEdge(tail: string, head: string, signal?: AbortSignal): Promise<boolean> {
    return this.invoke(async () => {
      const resp = await this.client.deleteEdge({ tail, head }, this.callOpts(signal));
      return resp.existed;
    });
  }

  async getEdges(
    refs: readonly { tail: string; head: string }[],
    signal?: AbortSignal,
  ): Promise<{ found: Edge[]; missing: { tail: string; head: string }[] }> {
    if (refs.length === 0) return { found: [], missing: [] };
    const found: Edge[] = [];
    const missing: { tail: string; head: string }[] = [];
    await this.runBatchRead(refs, async (chunk) => {
      const resp = await this.client.getEdges(
        { edges: chunk.map((r) => ({ tail: r.tail, head: r.head })) },
        this.callOpts(signal),
      );
      for (const e of resp.edges) {
        found.push(fromEdgeJson(toJson(EdgeSchema, e) as Record<string, unknown>));
      }
      for (const m of resp.missing) {
        missing.push({ tail: m.tail, head: m.head });
      }
    });
    return { found, missing };
  }

  async addEdges(inputs: readonly EdgeInput[], signal?: AbortSignal): Promise<number[]> {
    if (inputs.length === 0) return [];
    const effective: number[] = [];
    await this.runBatchWrite(inputs, async (chunk) => {
      const edges = chunk.map((e) => fromJson(EdgeSchema, toEdgeJson(e) as JsonValue));
      const contribIds = this.chunkContribIds(chunk);
      const resp = await this.client.addEdges(
        contribIds ? { edges, contribIds } : { edges },
        this.callOpts(signal),
      );
      for (const w of resp.effectiveWeights) {
        effective.push(w);
      }
    });
    return effective;
  }

  /**
   * Accumulates a single edge whose contributed live weight follows the
   * geometric decay staircase described by `opts`, using `Date.now()` as the
   * t=0 reference (#953, parity with the Go SDK's `AddDecayingEdge`). It
   * expands `opts` (see {@link decayContributions}) into up to `opts.steps`
   * additive contributions and applies them in one `addEdges` batch, so it
   * inherits that path's automatic chunking, contrib-id dedup (when
   * `idempotentAdds` is on), and post-accumulation effective-weight
   * reporting.
   *
   * Returns the edge's effective (live-sum) weight immediately after the add
   * — any preexisting live weight on `(tail, head)` plus `opts.initialWeight`.
   *
   * The whole curve is a single `addEdges` request (steps <=
   * MAX_DECAY_STEPS is far below the batch chunk size), so the server
   * validates every contribution's expiration before applying any: a horizon
   * longer than the server's `LANTERN_TOMBSTONE_TTL` rejects the entire add
   * rather than writing a truncated staircase. A later `putEdge` or
   * `deleteEdge` on the same endpoints replaces or removes every
   * contribution, discarding whatever remains of the schedule.
   *
   * @throws {InvalidArgumentError} synchronously (as a rejected promise) when
   * `opts` is ill-formed or its curve underflows float32 to zero.
   */
  async addDecayingEdge(
    tail: string,
    head: string,
    opts: DecayOptions,
    signal?: AbortSignal,
  ): Promise<number> {
    const inputs = decayContributions(tail, head, opts, Date.now());
    const effective = await this.addEdges(inputs, signal);
    // The contributions share (tail, head) and are applied in order, so the
    // last entry's effective weight is the full post-add live sum.
    return effective[effective.length - 1] ?? 0;
  }

  async putEdges(inputs: readonly EdgeInput[], signal?: AbortSignal): Promise<void> {
    if (inputs.length === 0) return;
    await this.runBatchWrite(inputs, async (chunk) => {
      const edges = chunk.map((e) => fromJson(EdgeSchema, toEdgeJson(e) as JsonValue));
      await this.client.putEdges({ edges }, this.callOpts(signal));
    });
  }

  async deleteEdges(
    refs: readonly { tail: string; head: string }[],
    signal?: AbortSignal,
  ): Promise<number> {
    if (refs.length === 0) return 0;
    let total = 0;
    await this.runBatchWrite(refs, async (chunk) => {
      const resp = await this.client.deleteEdges(
        { edges: chunk.map((r) => ({ tail: r.tail, head: r.head })) },
        this.callOpts(signal),
      );
      total += resp.deleted;
    });
    return total;
  }

  async scanEdges(
    opts: EdgeScanOptions = {},
    signal?: AbortSignal,
  ): Promise<{ edges: Edge[]; nextCursor: Uint8Array }> {
    return this.invoke(async () => {
      const resp = await this.client.scanEdges(
        {
          tailPrefix: opts.tailPrefix ?? "",
          headPrefix: opts.headPrefix ?? "",
          limit: opts.limit ?? 0,
          cursor: opts.cursor ?? new Uint8Array(),
        },
        this.callOpts(signal),
      );
      return {
        edges: resp.edges.map((e) =>
          fromEdgeJson(toJson(EdgeSchema, e) as Record<string, unknown>),
        ),
        nextCursor: resp.nextCursor,
      };
    });
  }

  async *scanEdgesAll(
    opts: EdgeScanOptions = {},
    batchSize?: number,
    signal?: AbortSignal,
  ): AsyncIterable<Edge[]> {
    let cursor: Uint8Array = opts.cursor ?? new Uint8Array();
    while (true) {
      if (signal?.aborted) throw new LanternError("scanEdgesAll aborted");
      const page = await this.scanEdges(
        { ...opts, limit: batchSize ?? opts.limit ?? 0, cursor },
        signal,
      );
      yield page.edges;
      if (page.nextCursor.length === 0) return;
      cursor = page.nextCursor;
    }
  }

  /**
   * Bulk-deletes edges whose tail and/or head key carries the given prefix,
   * the edge-shaped sibling of {@link deleteVerticesByPrefix}. At least one of
   * `tailPrefix` / `headPrefix` must be non-empty; a both-empty request is
   * rejected with `InvalidArgumentError` to prevent a whole-graph edge wipe.
   * An empty prefix on one axis matches any key on that axis, so the deleted
   * set is the intersection of the two prefix filters. `limit` caps how many
   * matching edges are removed in one call (0 = server default); loop until the
   * returned count is below `limit` to drain a large namespace. Pass
   * `dryRun: true` to learn the count that *would* be deleted without mutating.
   * Returns the number of edges deleted (or that would be deleted under
   * `dryRun`).
   */
  async deleteEdgesByPrefix(
    opts: DeleteEdgesByPrefixOptions = {},
    signal?: AbortSignal,
  ): Promise<bigint> {
    return this.invoke(async () => {
      const resp = await this.client.deleteEdgesByPrefix(
        {
          tailPrefix: opts.tailPrefix ?? "",
          headPrefix: opts.headPrefix ?? "",
          limit: opts.limit ?? 0,
          dryRun: opts.dryRun ?? false,
        },
        this.callOpts(signal),
      );
      return resp.deleted;
    });
  }

  async illuminate(seed: string, opts: IlluminateOptions, signal?: AbortSignal): Promise<Graph>;
  async illuminate(seed: string, opts?: IlluminateOptions, signal?: AbortSignal): Promise<Graph> {
    if (!seed) throw new InvalidArgumentError("illuminate: seed is required");
    if (!opts) {
      throw new InvalidArgumentError("illuminate: traversal family is required");
    }
    const families = [opts.bfs, opts.ppr, opts.community].filter((f) => f !== undefined).length;
    if (families !== 1) {
      throw new InvalidArgumentError(
        "illuminate: exactly one of bfs, ppr and community is required (the traversal family is a wire oneof)",
      );
    }
    if (
      opts.bfs &&
      (!Number.isSafeInteger(opts.bfs.step) ||
        !Number.isSafeInteger(opts.bfs.fanOut) ||
        opts.bfs.step <= 0 ||
        opts.bfs.fanOut <= 0)
    ) {
      throw new InvalidArgumentError(
        "illuminate: bfs step and fanOut must both be positive integers",
      );
    }
    return this.invoke(async () => {
      const params = opts.community
        ? ({
            case: "community" as const,
            value: {
              maxSize: opts.community.maxSize ?? 0,
              restartProb: opts.community.restartProb ?? 0,
              epsilon: opts.community.epsilon ?? 0,
              reduction:
                REDUCTION_TO_PB[opts.community.reduction ?? Reduction.UNSPECIFIED] ??
                PbReduction.UNSPECIFIED,
              objective:
                OBJECTIVE_TO_PB[opts.community.objective ?? Objective.UNSPECIFIED] ??
                PbObjective.UNSPECIFIED,
            },
          } as const)
        : opts.ppr
          ? ({
              case: "ppr" as const,
              value: {
                topN: opts.ppr.topN ?? 0,
                restartProb: opts.ppr.restartProb ?? 0,
                epsilon: opts.ppr.epsilon ?? 0,
              },
            } as const)
          : opts.bfs
            ? ({
                case: "bfs" as const,
                value: {
                  step: opts.bfs.step,
                  fanOut: opts.bfs.fanOut,
                  objective:
                    OBJECTIVE_TO_PB[opts.bfs.objective ?? Objective.UNSPECIFIED] ??
                    PbObjective.UNSPECIFIED,
                  reduction:
                    REDUCTION_TO_PB[opts.bfs.reduction ?? Reduction.UNSPECIFIED] ??
                    PbReduction.UNSPECIFIED,
                },
              } as const)
            : undefined;
      const resp = await this.client.illuminate(
        {
          seed,
          weighting:
            WEIGHTING_TO_PB[opts.weighting ?? Weighting.UNSPECIFIED] ?? PbWeighting.UNSPECIFIED,
          vertexPrefix: opts.vertexPrefix ?? "",
          ...(params ? { params } : {}),
        },
        this.callOpts(signal),
      );
      const vertices = new Map<string, Vertex>();
      for (const v of resp.graph?.vertices ?? []) {
        const flat = toJson(VertexSchema, v) as Record<string, unknown>;
        vertices.set(String(flat.key ?? ""), fromVertexJson(flat));
      }
      const edges = new Map<string, Map<string, number>>();
      for (const e of resp.graph?.edges ?? []) {
        const flat = toJson(EdgeSchema, e) as Record<string, unknown>;
        const tail = String(flat.tail ?? "");
        const head = String(flat.head ?? "");
        const weight = Number(flat.weight ?? 0);
        if (!edges.has(tail)) edges.set(tail, new Map());
        edges.get(tail)!.set(head, weight);
      }
      return { vertices, edges };
    });
  }

  // --- Observability RPCs ---

  /**
   * Returns the server's identity, build info, runtime configuration
   * and live cache counts. Used by admin's Ops view; the returned
   * shape is the raw `GetServerStatusResponse` protobuf-es message
   * (timestamps as `Timestamp`, durations as `Duration`, counts as
   * `bigint`). Callers that want JSON-flat values can pass the result
   * through `toJson(GetServerStatusResponseSchema, ...)`.
   */
  async getServerStatus(signal?: AbortSignal): Promise<GetServerStatusResponse> {
    return this.invoke(() => this.client.getServerStatus({}, this.callOpts(signal)));
  }

  /**
   * Returns a per-peer snapshot of the local replication pump state.
   * On single-node deployments returns `{ enabled: false, peers: [] }`.
   * The returned shape is the raw `GetReplicationStatusResponse`
   * protobuf-es message; admin renders it via its own selector layer.
   */
  async getReplicationStatus(signal?: AbortSignal): Promise<GetReplicationStatusResponse> {
    return this.invoke(() => this.client.getReplicationStatus({}, this.callOpts(signal)));
  }

  // --- Backup / restore (#685) ---

  /**
   * Stream a whole-graph, point-in-time dump as an async iterable of
   * {@link BackupRecord}s — every live vertex followed by every folded live
   * edge. Consumes the server-streaming `BackupSnapshot` RPC and decodes each
   * frame **kind-preservingly**, so narrow numeric value types survive to
   * {@link restore} losslessly.
   *
   * `opts.prefix` scopes the dump to vertices whose key begins with the given
   * prefix (and edges incident to them); omit it to dump the whole graph.
   * Pass a `signal` to abort mid-stream. Serialise the records yourself, or
   * pipe them through {@link backupRecordToNdjson} for a Node-native NDJSON
   * file you can later feed back to {@link restore}.
   *
   * @example
   * for await (const rec of client.backup()) {
   *   process.stdout.write(backupRecordToNdjson(rec) + "\n");
   * }
   */
  async *backup(opts: { prefix?: string } = {}, signal?: AbortSignal): AsyncIterable<BackupRecord> {
    const stream = this.client.backupSnapshot(
      { vertexPrefix: opts.prefix ?? "" },
      this.callOpts(signal),
    );
    try {
      for await (const frame of stream) {
        if (frame.record.case === "vertex") {
          const flat = toJson(VertexSchema, frame.record.value) as Record<string, unknown>;
          yield { kind: "vertex", vertex: fromVertexJson(flat) };
        } else if (frame.record.case === "edge") {
          const flat = toJson(EdgeSchema, frame.record.value) as Record<string, unknown>;
          yield { kind: "edge", edge: fromEdgeJson(flat) };
        }
      }
    } catch (err) {
      throw wrapConnectError(err);
    }
  }

  /**
   * Replay a dump — the output of {@link backup}, a decoded NDJSON file, or
   * any (async) iterable of {@link BackupRecord}s — back into the graph via
   * the batch `putVertices` / `putEdges` surface (there is no dedicated
   * restore RPC). Records are re-encoded kind-preservingly, so the load is
   * lossless. Batches flush every `opts.chunkSize` records
   * (default {@link DEFAULT_RESTORE_CHUNK_SIZE}); a final flush writes any
   * remainder **vertices before edges**, so real vertex values land before
   * `PutEdges` would otherwise auto-create bare endpoints.
   *
   * Returns the number of vertices and edges written. Restore is **not**
   * transactional — a mid-stream error leaves already-flushed batches in
   * place.
   */
  async restore(
    source: RestoreSource,
    opts: { chunkSize?: number } = {},
    signal?: AbortSignal,
  ): Promise<RestoreStats> {
    const chunkSize =
      opts.chunkSize && opts.chunkSize > 0 ? opts.chunkSize : DEFAULT_RESTORE_CHUNK_SIZE;
    const stats: RestoreStats = { vertices: 0, edges: 0 };
    let vbatch: PbVertex[] = [];
    let ebatch: PbEdge[] = [];

    const flushVertices = async (): Promise<void> => {
      if (vbatch.length === 0) return;
      const vertices = vbatch;
      vbatch = [];
      await this.invoke(() => this.client.putVertices({ vertices }, this.callOpts(signal)));
      stats.vertices += vertices.length;
    };
    const flushEdges = async (): Promise<void> => {
      if (ebatch.length === 0) return;
      const edges = ebatch;
      ebatch = [];
      await this.invoke(() => this.client.putEdges({ edges }, this.callOpts(signal)));
      stats.edges += edges.length;
    };

    for await (const rec of source) {
      if (rec.kind === "vertex") {
        vbatch.push(fromJson(VertexSchema, vertexRecordToJson(rec.vertex) as JsonValue));
        if (vbatch.length >= chunkSize) await flushVertices();
      } else {
        ebatch.push(fromJson(EdgeSchema, edgeRecordToJson(rec.edge) as JsonValue));
        if (ebatch.length >= chunkSize) await flushEdges();
      }
    }
    await flushVertices();
    await flushEdges();
    return stats;
  }

  // --- Health ---

  /**
   * Probe server readiness via the gRPC-Health-v1 `Health/Check` that rides
   * the primary listener (auth-exempt). Resolves when the server reports
   * SERVING; throws {@link HealthStatusError} on any other status, or a
   * generic {@link LanternError} on transport / non-200 / decode failure. A
   * `signal` (and the client's `defaultTimeoutMs`, if set) bound the call.
   *
   * Only available on clients built through `connect()` / `connectWeb()` — or
   * a `withTransport` client given a `baseUrl` — since the probe POSTs outside
   * the typed transport. A transport-only client has no URL and throws
   * {@link InvalidArgumentError}.
   */
  async ping(signal?: AbortSignal): Promise<void> {
    if (!this.baseUrl) {
      throw new InvalidArgumentError(
        "ping requires a base URL; build the client via connect()/connectWeb(), " +
          "or pass baseUrl to withTransport()",
      );
    }
    const opts: PingOptions = {};
    if (signal) opts.signal = signal;
    if (this.options.defaultTimeoutMs && this.options.defaultTimeoutMs > 0) {
      opts.timeoutMs = this.options.defaultTimeoutMs;
    }
    await pingHealth(this.baseUrl, opts);
  }

  // --- internals ---

  private callOpts(signal?: AbortSignal): {
    signal?: AbortSignal;
    timeoutMs?: number;
  } {
    const out: { signal?: AbortSignal; timeoutMs?: number } = {};
    if (signal) out.signal = signal;
    if (this.options.defaultTimeoutMs && this.options.defaultTimeoutMs > 0) {
      out.timeoutMs = this.options.defaultTimeoutMs;
    }
    return out;
  }

  private async invoke<T>(fn: () => Promise<T>): Promise<T> {
    try {
      return await fn();
    } catch (err) {
      throw wrapConnectError(err);
    }
  }

  private chunkSize(): number {
    const n = this.options.batchChunkSize ?? DEFAULT_BATCH_CHUNK_SIZE;
    const positive = n > 0 ? n : DEFAULT_BATCH_CHUNK_SIZE;
    return Math.min(positive, MAX_BATCH_CHUNK_SIZE);
  }

  /** Whether automatic contrib IDs are enabled for Add calls (#895). */
  private get autoContrib(): boolean {
    return this.options.idempotentAdds === true;
  }

  /** Bump and return the monotonic per-call contrib sequence (#895). */
  private nextSeq(): bigint {
    this.callSeq += 1n;
    return this.callSeq;
  }

  /** Lazily mint the per-client nonce that seeds automatic contrib IDs (#895). */
  private getNonce(): Uint8Array {
    if (!this.nonce) {
      this.nonce = makeNonce();
    }
    return this.nonce;
  }

  /**
   * Resolve the contrib ID for a singular `addEdge` (#895): a caller-supplied
   * id (validated) wins; otherwise an automatic id when `idempotentAdds` is
   * on; otherwise undefined (the field stays absent → legacy additive path).
   */
  private singleContribId(input: EdgeInput): Uint8Array | undefined {
    if (input.contribId !== undefined) {
      return validateContribId(input.contribId);
    }
    if (this.autoContrib) {
      return contribIdFrom(this.getNonce(), this.nextSeq(), 0);
    }
    return undefined;
  }

  /**
   * Build the index-aligned `contrib_ids` for one `addEdges` chunk (#895).
   * Returns undefined when neither automatic ids nor any caller-supplied id
   * applies (so the wire field stays absent → legacy). Otherwise every edge
   * gets a slot: a caller id (validated) wins, else an automatic id when
   * enabled, else an empty slot the server reads as absent. One sequence is
   * consumed per chunk so a retried chunk re-sends identical bytes and the
   * ids stay aligned with `edges` regardless of how the batch was chunked.
   */
  private chunkContribIds(chunk: readonly EdgeInput[]): Uint8Array[] | undefined {
    const auto = this.autoContrib;
    const anyCaller = chunk.some((e) => e.contribId !== undefined);
    if (!auto && !anyCaller) return undefined;
    const seq = auto ? this.nextSeq() : 0n;
    const nonce = auto ? this.getNonce() : null;
    return chunk.map((e, i) => {
      if (e.contribId !== undefined) return validateContribId(e.contribId);
      if (auto && nonce) return contribIdFrom(nonce, seq, i);
      return new Uint8Array();
    });
  }

  private async runBatchWrite<T>(
    items: readonly T[],
    sendChunk: (chunk: T[]) => Promise<void>,
  ): Promise<void> {
    const size = this.chunkSize();
    let written = 0;
    for (let i = 0; i < items.length; i += size) {
      const chunk = items.slice(i, Math.min(i + size, items.length));
      try {
        await sendChunk(chunk);
      } catch (err) {
        throw new BatchError(written, wrapConnectError(err));
      }
      written += chunk.length;
    }
  }

  private async runBatchRead<T>(
    items: readonly T[],
    sendChunk: (chunk: T[]) => Promise<void>,
  ): Promise<void> {
    const size = this.chunkSize();
    for (let i = 0; i < items.length; i += size) {
      const chunk = items.slice(i, Math.min(i + size, items.length));
      try {
        await sendChunk(chunk);
      } catch (err) {
        throw wrapConnectError(err);
      }
    }
  }
}
