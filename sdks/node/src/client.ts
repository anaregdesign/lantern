/**
 * Promise-based Connect-Node client for the Lantern service.
 *
 * Open with `Lantern.connect("http://host:6380")` against the server's
 * Connect listener. All methods return Promises; pass `signal` for
 * AbortController-driven cancellation.
 *
 * Batch helpers (`putVertices`, `addEdges`, `putEdges`,
 * `deleteVertices`, `deleteEdges`) auto-chunk at
 * `ConnectOptions.batchChunkSize` (default 1000) and throw
 * `BatchError` with a resumable `written` offset on partial failure.
 *
 * Wire: Connect protocol (Connect/JSON by default; flip to binary via
 * `transportOptions.useBinaryFormat`). Built on @connectrpc/connect v2
 * + @connectrpc/connect-node v2; codegen via @bufbuild/protoc-gen-es
 * (single plugin emits both message classes and the service schema
 * descriptor `LanternService`).
 */

import { type Client, type Interceptor, createClient } from "@connectrpc/connect";
import { createConnectTransport, type ConnectTransportOptions } from "@connectrpc/connect-node";
import { fromJson, toJson, type JsonValue } from "@bufbuild/protobuf";

import {
  EdgeSchema,
  LanternService,
  Algorithm as PbAlgorithm,
  Objective as PbObjective,
  Weighting as PbWeighting,
  VertexSchema,
} from "./gen/graph/v1/graph_pb.js";

import {
  BatchError,
  InvalidArgumentError,
  LanternError,
  NotFoundError,
  wrapConnectError,
} from "./errors.js";
import {
  Algorithm,
  Objective,
  Weighting,
  fromEdgeJson,
  fromVertexJson,
  toEdgeJson,
  toVertexJson,
  type Edge,
  type EdgeInput,
  type Graph,
  type Vertex,
  type VertexInput,
} from "./values.js";
import {
  type ConnectOptions,
  DEFAULT_BATCH_CHUNK_SIZE,
  type DeleteByPrefixOptions,
  type EdgeScanOptions,
  type IlluminateOptions,
  type ScanOptions,
} from "./options.js";

/**
 * Constructor arguments for `Lantern.connect`. The HTTP base URL is the
 * only required field; everything else either has a sensible default or
 * is an opt-in tuning knob.
 */
export interface LanternArgs {
  options?: ConnectOptions;
  /**
   * Optional list of Connect interceptors run on every unary call.
   * The order matches @connectrpc/connect's `Interceptor[]` chain —
   * the first entry sees outgoing requests first and responses last.
   */
  interceptors?: Interceptor[];
  /**
   * Override the transport options forwarded to
   * `createConnectTransport`. `baseUrl` is filled in from the
   * constructor arg and cannot be overridden here.
   */
  transportOptions?: Omit<ConnectTransportOptions, "baseUrl">;
}

// Connect-es v2 enum values match the proto numeric IDs verbatim — no
// translation needed beyond the type cast. Per #410 the Illuminate
// request carries three orthogonal axes; each is a flat numeric enum on
// the wire so a single Number(...) coercion is sufficient.
const ALGORITHM_TO_PB: Record<number, PbAlgorithm> = {
  [Algorithm.UNSPECIFIED]: PbAlgorithm.UNSPECIFIED,
  [Algorithm.MINIMUM_SPANNING_TREE]: PbAlgorithm.MINIMUM_SPANNING_TREE,
  [Algorithm.SHORTEST_PATH_TREE]: PbAlgorithm.SHORTEST_PATH_TREE,
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
};

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

  private constructor(client: Client<typeof LanternService>, options: ConnectOptions) {
    this.client = client;
    this.options = options;
  }

  /**
   * Open a Connect-Node client against the supplied base URL. The
   * base URL MUST include the scheme — `http://` for h2c (the server
   * default), or `https://` for TLS.
   *
   * Defaults:
   *   - HTTP/2 transport (Connect protocol, JSON codec); set
   *     `args.transportOptions.useBinaryFormat = true` to flip to
   *     protobuf.
   *   - Batch chunk size 1000.
   *   - No per-call timeout; pass
   *     `args.options.defaultTimeoutMs` to apply one.
   */
  static connect(baseUrl: string, args: LanternArgs = {}): Lantern {
    if (!baseUrl) {
      throw new Error("Lantern.connect: baseUrl is required");
    }
    if (!/^https?:\/\//.test(baseUrl)) {
      throw new Error(
        `Lantern.connect: baseUrl must include scheme (http:// or https://); got ${JSON.stringify(baseUrl)}`,
      );
    }
    const normalised = baseUrl.replace(/\/$/, "");
    const transport = createConnectTransport({
      baseUrl: normalised,
      httpVersion: "2",
      interceptors: args.interceptors,
      ...(args.transportOptions as Partial<ConnectTransportOptions> | undefined),
    } as ConnectTransportOptions);
    return new Lantern(createClient(LanternService, transport), {
      batchChunkSize: DEFAULT_BATCH_CHUNK_SIZE,
      ...(args.options ?? {}),
    });
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

  async *scanVerticesAll(
    prefix: string,
    batchSize?: number,
    signal?: AbortSignal,
  ): AsyncIterable<Vertex[]> {
    let cursor: Uint8Array = new Uint8Array();
    while (true) {
      if (signal?.aborted) throw new LanternError("scanVerticesAll aborted");
      const page = await this.scanVertices(prefix, { limit: batchSize ?? 0, cursor }, signal);
      yield page.vertices;
      if (page.nextCursor.length === 0) return;
      cursor = page.nextCursor;
    }
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

  async addEdge(input: EdgeInput, signal?: AbortSignal): Promise<void> {
    const edge = fromJson(EdgeSchema, toEdgeJson(input) as JsonValue);
    return this.invoke(async () => {
      await this.client.addEdge({ edge }, this.callOpts(signal));
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

  async addEdges(inputs: readonly EdgeInput[], signal?: AbortSignal): Promise<void> {
    if (inputs.length === 0) return;
    await this.runBatchWrite(inputs, async (chunk) => {
      const edges = chunk.map((e) => fromJson(EdgeSchema, toEdgeJson(e) as JsonValue));
      await this.client.addEdges({ edges }, this.callOpts(signal));
    });
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

  async illuminate(
    seed: string,
    opts: IlluminateOptions = {},
    signal?: AbortSignal,
  ): Promise<Graph> {
    if (!seed) throw new InvalidArgumentError("illuminate: seed is required");
    return this.invoke(async () => {
      const resp = await this.client.illuminate(
        {
          seed,
          step: opts.step ?? 0,
          k: opts.k ?? 0,
          algorithm:
            ALGORITHM_TO_PB[opts.algorithm ?? Algorithm.UNSPECIFIED] ?? PbAlgorithm.UNSPECIFIED,
          objective:
            OBJECTIVE_TO_PB[opts.objective ?? Objective.UNSPECIFIED] ?? PbObjective.UNSPECIFIED,
          weighting:
            WEIGHTING_TO_PB[opts.weighting ?? Weighting.UNSPECIFIED] ?? PbWeighting.UNSPECIFIED,
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
    return n > 0 ? n : DEFAULT_BATCH_CHUNK_SIZE;
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
