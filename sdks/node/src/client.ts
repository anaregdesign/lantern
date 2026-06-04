/**
 * Promise-based gRPC client for the Lantern service.
 *
 * Open with `Lantern.connect(target)` for a single target or DNS-resolved
 * fan-out, or `Lantern.connectEndpoints([...])` for an explicit static
 * endpoint list. Always call `client.close()` when done, or use it inside
 * a `try { ... } finally { client.close(); }` block.
 *
 * All methods return Promises. Pass `signal` for AbortController-driven
 * cancellation — it translates to a gRPC deadline + channel cancel.
 *
 * Batch helpers (putVertices, addEdges, putEdges, deleteVertices,
 * deleteEdges) auto-chunk at `ConnectOptions.batchChunkSize` (default
 * 1000) and throw `BatchError` with a resumable `written` offset on
 * partial failure.
 */

import {
  type CallOptions,
  type ChannelCredentials,
  type ChannelOptions,
  Metadata,
  type ServiceError,
  credentials as grpcCredentials,
} from "@grpc/grpc-js";

import { BatchError, wrapRpcError } from "./errors.js";
import { hasEndpoints, staticTarget } from "./endpoints.js";
import {
  type ConnectOptions,
  DEFAULT_BATCH_CHUNK_SIZE,
  type DeleteByPrefixOptions,
  type EdgeScanOptions,
  type IlluminateOptions,
  type ScanOptions,
} from "./options.js";
import {
  type Edge,
  type EdgeInput,
  type Graph,
  Optimization,
  type Vertex,
  type VertexInput,
  edgeInputToPb,
  fromPbEdge,
  fromPbVertex,
  vertexInputToPb,
} from "./values.js";

import { LanternServiceClient } from "./generated/graph/v1/graph.js";

function longToNumber(v: unknown): number {
  if (typeof v === "number") return v;
  if (
    v &&
    typeof v === "object" &&
    "toNumber" in v &&
    typeof (v as { toNumber: () => number }).toNumber === "function"
  ) {
    return (v as { toNumber: () => number }).toNumber();
  }
  return Number(v);
}

function longToBigInt(v: unknown): bigint {
  if (typeof v === "bigint") return v;
  if (typeof v === "number") return BigInt(v);
  if (v && typeof v === "object" && "toString" in v) {
    return BigInt((v as { toString(): string }).toString());
  }
  return BigInt(String(v));
}

const SERVICE = "graph.v1.LanternService";

const DEFAULT_SERVICE_CONFIG_OBJECT = {
  loadBalancingConfig: [{ round_robin: {} }],
  methodConfig: [
    {
      name: [{ service: SERVICE }],
      retryPolicy: {
        maxAttempts: 5,
        initialBackoff: "0.1s",
        maxBackoff: "2s",
        backoffMultiplier: 2.0,
        retryableStatusCodes: ["UNAVAILABLE", "RESOURCE_EXHAUSTED"],
      },
    },
    {
      // AddEdge / AddEdges are additive: omitting retryPolicy entirely
      // disables retries so duplicate weights cannot accumulate from
      // transient network blips.
      name: [
        { service: SERVICE, method: "AddEdge" },
        { service: SERVICE, method: "AddEdges" },
      ],
    },
  ],
};

const DEFAULT_SERVICE_CONFIG_JSON = JSON.stringify(DEFAULT_SERVICE_CONFIG_OBJECT);

/** Return the JSON service config the SDK applies by default. */
export function defaultServiceConfig(): string {
  return DEFAULT_SERVICE_CONFIG_JSON;
}

export interface LanternConnectArgs {
  credentials?: ChannelCredentials;
  options?: ConnectOptions;
  channelOptions?: ChannelOptions;
}

export class Lantern {
  private readonly stub: LanternServiceClient;
  private readonly opts: Required<Pick<ConnectOptions, "batchChunkSize">> & ConnectOptions;
  private closed = false;

  private constructor(stub: LanternServiceClient, opts: ConnectOptions) {
    this.stub = stub;
    this.opts = { ...opts, batchChunkSize: opts.batchChunkSize ?? DEFAULT_BATCH_CHUNK_SIZE };
  }

  // ------------------------------------------------------------------
  // Construction / lifecycle
  // ------------------------------------------------------------------

  static connect(target: string, args: LanternConnectArgs = {}): Lantern {
    const opts = args.options ?? {};
    const creds = args.credentials ?? grpcCredentials.createInsecure();
    const channelOptions: ChannelOptions = { ...(args.channelOptions ?? {}) };
    channelOptions["grpc.service_config"] = opts.serviceConfigJson ?? DEFAULT_SERVICE_CONFIG_JSON;
    if (opts.userAgent) channelOptions["grpc.primary_user_agent"] = opts.userAgent;
    const stub = new LanternServiceClient(target, creds, channelOptions);
    return new Lantern(stub, opts);
  }

  static connectEndpoints(endpoints: readonly string[], args: LanternConnectArgs = {}): Lantern {
    return Lantern.connect(staticTarget(endpoints), args);
  }

  close(): void {
    if (this.closed) return;
    this.closed = true;
    this.stub.close();
  }

  /** Expose the underlying gRPC stub for advanced interop. */
  get rawStub(): LanternServiceClient {
    return this.stub;
  }

  // ------------------------------------------------------------------
  // Vertex — single
  // ------------------------------------------------------------------

  async getVertex(key: string, signal?: AbortSignal): Promise<Vertex> {
    const resp = await this.unary<
      { key: string },
      { vertex: import("./generated/graph/v1/graph.js").Vertex | undefined }
    >("getVertex", { key }, signal);
    return fromPbVertex(resp.vertex ?? { key: "", expiration: undefined });
  }

  async putVertex(
    key: string,
    value: VertexInput["value"],
    extra: { ttlSeconds?: number; expiration?: Date; signal?: AbortSignal } = {},
  ): Promise<void> {
    const vertex = vertexInputToPb({
      key,
      value,
      ttlSeconds: extra.ttlSeconds,
      expiration: extra.expiration,
    });
    await this.unary("putVertex", { vertex }, extra.signal);
  }

  async deleteVertex(key: string, signal?: AbortSignal): Promise<boolean> {
    const resp = await this.unary<{ key: string }, { existed: boolean }>(
      "deleteVertex",
      { key },
      signal,
    );
    return resp.existed;
  }

  // ------------------------------------------------------------------
  // Vertex — batch
  // ------------------------------------------------------------------

  async getVertices(
    keys: readonly string[],
    signal?: AbortSignal,
  ): Promise<{ present: Vertex[]; missing: string[] }> {
    const resp = await this.unary<
      { keys: string[] },
      { vertices: import("./generated/graph/v1/graph.js").Vertex[]; missing: string[] }
    >("getVertices", { keys: [...keys] }, signal);
    return {
      present: resp.vertices.map(fromPbVertex),
      missing: [...resp.missing],
    };
  }

  async putVertices(
    inputs: readonly VertexInput[],
    extra: { chunkSize?: number; signal?: AbortSignal } = {},
  ): Promise<number> {
    const chunk = extra.chunkSize ?? this.opts.batchChunkSize;
    let written = 0;
    for (const batch of chunks(inputs, chunk)) {
      try {
        const resp = await this.unary<
          { vertices: import("./generated/graph/v1/graph.js").Vertex[] },
          { written: unknown }
        >("putVertices", { vertices: batch.map(vertexInputToPb) }, extra.signal);
        written += longToNumber(resp.written);
      } catch (err) {
        if (err instanceof BatchError) throw err;
        throw new BatchError(written, err);
      }
    }
    return written;
  }

  async deleteVertices(
    keys: readonly string[],
    extra: { chunkSize?: number; signal?: AbortSignal } = {},
  ): Promise<number> {
    const chunk = extra.chunkSize ?? this.opts.batchChunkSize;
    let deleted = 0;
    let seen = 0;
    for (const batch of chunks(keys, chunk)) {
      try {
        const resp = await this.unary<{ keys: string[] }, { deleted: unknown }>(
          "deleteVertices",
          { keys: [...batch] },
          extra.signal,
        );
        deleted += longToNumber(resp.deleted);
        seen += batch.length;
      } catch (err) {
        throw new BatchError(seen, err);
      }
    }
    return deleted;
  }

  // ------------------------------------------------------------------
  // Vertex — prefix
  // ------------------------------------------------------------------

  async scanVertices(
    prefix: string,
    options: ScanOptions & { signal?: AbortSignal } = {},
  ): Promise<{ vertices: Vertex[]; nextCursor: Uint8Array }> {
    const resp = await this.unary<
      { prefix: string; limit: number; cursor: Buffer },
      { vertices: import("./generated/graph/v1/graph.js").Vertex[]; nextCursor: Buffer }
    >(
      "scanVertices",
      {
        prefix,
        limit: options.limit ?? 0,
        cursor: Buffer.from(options.cursor ?? new Uint8Array()),
      },
      options.signal,
    );
    return {
      vertices: resp.vertices.map(fromPbVertex),
      nextCursor: new Uint8Array(resp.nextCursor),
    };
  }

  async *scanVerticesAll(
    prefix: string,
    options: { limit?: number; signal?: AbortSignal } = {},
  ): AsyncIterableIterator<Vertex[]> {
    let cursor: Uint8Array = Buffer.alloc(0);
    for (;;) {
      const page = await this.scanVertices(prefix, {
        limit: options.limit ?? 0,
        cursor,
        signal: options.signal,
      });
      if (page.vertices.length > 0) yield page.vertices;
      if (page.nextCursor.length === 0) return;
      cursor = page.nextCursor;
    }
  }

  async countVerticesByPrefix(prefix: string, signal?: AbortSignal): Promise<bigint> {
    const resp = await this.unary<{ prefix: string }, { count: unknown }>(
      "countVerticesByPrefix",
      { prefix },
      signal,
    );
    return longToBigInt(resp.count);
  }

  async deleteVerticesByPrefix(
    prefix: string,
    options: DeleteByPrefixOptions & { signal?: AbortSignal } = {},
  ): Promise<bigint> {
    const resp = await this.unary<
      { prefix: string; limit: number; dryRun: boolean },
      { deleted: unknown }
    >(
      "deleteVerticesByPrefix",
      { prefix, limit: options.limit ?? 0, dryRun: options.dryRun ?? false },
      options.signal,
    );
    return longToBigInt(resp.deleted);
  }

  // ------------------------------------------------------------------
  // Edge — single
  // ------------------------------------------------------------------

  async getEdge(tail: string, head: string, signal?: AbortSignal): Promise<Edge> {
    const resp = await this.unary<
      { tail: string; head: string },
      { edge: import("./generated/graph/v1/graph.js").Edge | undefined }
    >("getEdge", { tail, head }, signal);
    return fromPbEdge(resp.edge ?? { tail: "", head: "", weight: 0, expiration: undefined });
  }

  async addEdge(
    tail: string,
    head: string,
    weight = 1.0,
    extra: { ttlSeconds?: number; expiration?: Date; signal?: AbortSignal } = {},
  ): Promise<void> {
    const edge = edgeInputToPb({
      tail,
      head,
      weight,
      ttlSeconds: extra.ttlSeconds,
      expiration: extra.expiration,
    });
    await this.unary("addEdge", { edge }, extra.signal);
  }

  async putEdge(
    tail: string,
    head: string,
    weight = 1.0,
    extra: { ttlSeconds?: number; expiration?: Date; signal?: AbortSignal } = {},
  ): Promise<void> {
    const edge = edgeInputToPb({
      tail,
      head,
      weight,
      ttlSeconds: extra.ttlSeconds,
      expiration: extra.expiration,
    });
    await this.unary("putEdge", { edge }, extra.signal);
  }

  async deleteEdge(tail: string, head: string, signal?: AbortSignal): Promise<boolean> {
    const resp = await this.unary<{ tail: string; head: string }, { existed: boolean }>(
      "deleteEdge",
      { tail, head },
      signal,
    );
    return resp.existed;
  }

  // ------------------------------------------------------------------
  // Edge — batch
  // ------------------------------------------------------------------

  async getEdges(
    pairs: readonly { tail: string; head: string }[],
    signal?: AbortSignal,
  ): Promise<{ present: Edge[]; missing: { tail: string; head: string }[] }> {
    const resp = await this.unary<
      { edges: { tail: string; head: string }[] },
      {
        edges: import("./generated/graph/v1/graph.js").Edge[];
        missing: { tail: string; head: string }[];
      }
    >("getEdges", { edges: [...pairs] }, signal);
    return {
      present: resp.edges.map(fromPbEdge),
      missing: resp.missing.map((k) => ({ tail: k.tail, head: k.head })),
    };
  }

  async addEdges(
    inputs: readonly EdgeInput[],
    extra: { chunkSize?: number; signal?: AbortSignal } = {},
  ): Promise<number> {
    return this.batchEdgeWrite("addEdges", inputs, extra);
  }

  async putEdges(
    inputs: readonly EdgeInput[],
    extra: { chunkSize?: number; signal?: AbortSignal } = {},
  ): Promise<number> {
    return this.batchEdgeWrite("putEdges", inputs, extra);
  }

  async deleteEdges(
    pairs: readonly { tail: string; head: string }[],
    extra: { chunkSize?: number; signal?: AbortSignal } = {},
  ): Promise<number> {
    const chunk = extra.chunkSize ?? this.opts.batchChunkSize;
    let deleted = 0;
    let seen = 0;
    for (const batch of chunks(pairs, chunk)) {
      try {
        const resp = await this.unary<
          { edges: { tail: string; head: string }[] },
          { deleted: unknown }
        >("deleteEdges", { edges: [...batch] }, extra.signal);
        deleted += longToNumber(resp.deleted);
        seen += batch.length;
      } catch (err) {
        throw new BatchError(seen, err);
      }
    }
    return deleted;
  }

  // ------------------------------------------------------------------
  // Edge — prefix
  // ------------------------------------------------------------------

  async scanEdges(
    options: EdgeScanOptions & { signal?: AbortSignal } = {},
  ): Promise<{ edges: Edge[]; nextCursor: Uint8Array }> {
    const resp = await this.unary<
      { tailPrefix: string; headPrefix: string; limit: number; cursor: Buffer },
      { edges: import("./generated/graph/v1/graph.js").Edge[]; nextCursor: Buffer }
    >(
      "scanEdges",
      {
        tailPrefix: options.tailPrefix ?? "",
        headPrefix: options.headPrefix ?? "",
        limit: options.limit ?? 0,
        cursor: Buffer.from(options.cursor ?? new Uint8Array()),
      },
      options.signal,
    );
    return {
      edges: resp.edges.map(fromPbEdge),
      nextCursor: new Uint8Array(resp.nextCursor),
    };
  }

  async *scanEdgesAll(
    options: {
      tailPrefix?: string;
      headPrefix?: string;
      limit?: number;
      signal?: AbortSignal;
    } = {},
  ): AsyncIterableIterator<Edge[]> {
    let cursor: Uint8Array = Buffer.alloc(0);
    for (;;) {
      const page = await this.scanEdges({
        tailPrefix: options.tailPrefix,
        headPrefix: options.headPrefix,
        limit: options.limit ?? 0,
        cursor,
        signal: options.signal,
      });
      if (page.edges.length > 0) yield page.edges;
      if (page.nextCursor.length === 0) return;
      cursor = page.nextCursor;
    }
  }

  // ------------------------------------------------------------------
  // Illuminate
  // ------------------------------------------------------------------

  async illuminate(
    seed: string,
    options: IlluminateOptions & { signal?: AbortSignal } = {},
  ): Promise<Graph> {
    const resp = await this.unary<
      { seed: string; step: number; k: number; tfidf: boolean; optimization: number },
      {
        graph:
          | {
              vertices: import("./generated/graph/v1/graph.js").Vertex[];
              edges: import("./generated/graph/v1/graph.js").Edge[];
            }
          | undefined;
      }
    >(
      "illuminate",
      {
        seed,
        step: options.step ?? 0,
        k: options.k ?? 0,
        tfidf: options.tfidf ?? false,
        optimization: options.optimization ?? Optimization.UNSPECIFIED,
      },
      options.signal,
    );
    const graph: Graph = { vertices: new Map(), edges: new Map() };
    if (!resp.graph) return graph;
    for (const v of resp.graph.vertices) {
      const sv = fromPbVertex(v);
      graph.vertices.set(sv.key, sv);
    }
    for (const e of resp.graph.edges) {
      let row = graph.edges.get(e.tail);
      if (!row) {
        row = new Map();
        graph.edges.set(e.tail, row);
      }
      row.set(e.head, e.weight);
    }
    return graph;
  }

  // ------------------------------------------------------------------
  // Internals
  // ------------------------------------------------------------------

  private async batchEdgeWrite(
    method: "addEdges" | "putEdges",
    inputs: readonly EdgeInput[],
    extra: { chunkSize?: number; signal?: AbortSignal },
  ): Promise<number> {
    const chunk = extra.chunkSize ?? this.opts.batchChunkSize;
    let written = 0;
    for (const batch of chunks(inputs, chunk)) {
      try {
        const resp = await this.unary<
          { edges: import("./generated/graph/v1/graph.js").Edge[] },
          { written: unknown }
        >(method, { edges: batch.map(edgeInputToPb) }, extra.signal);
        written += longToNumber(resp.written);
      } catch (err) {
        throw new BatchError(written, err);
      }
    }
    return written;
  }

  private unary<Req, Resp>(
    method: keyof LanternServiceClient,
    req: Req,
    signal?: AbortSignal,
  ): Promise<Resp> {
    return new Promise<Resp>((resolve, reject) => {
      if (signal?.aborted) {
        reject(wrapRpcError(new Error("aborted")));
        return;
      }
      const callOptions: CallOptions = {};
      if (this.opts.defaultTimeoutMs !== undefined) {
        callOptions.deadline = new Date(Date.now() + this.opts.defaultTimeoutMs);
      }
      type UnaryFn = (
        request: Req,
        metadata: Metadata,
        options: CallOptions,
        callback: (err: ServiceError | null, response: Resp) => void,
      ) => { cancel(): void };
      const stubAny = this.stub as unknown as Record<string, UnaryFn>;
      const fn = stubAny[method as string];
      if (!fn) {
        reject(new Error(`unknown rpc method: ${String(method)}`));
        return;
      }
      const call = fn.call(
        this.stub as unknown as ThisType<UnaryFn>,
        req,
        new Metadata(),
        callOptions,
        (err, response) => {
          if (err) {
            reject(wrapRpcError(err));
            return;
          }
          resolve(response);
        },
      );
      if (signal) {
        const onAbort = () => {
          try {
            call.cancel();
          } catch {
            /* ignore */
          }
        };
        if (signal.aborted) onAbort();
        else signal.addEventListener("abort", onAbort, { once: true });
      }
    });
  }
}

function* chunks<T>(seq: readonly T[], n: number): IterableIterator<readonly T[]> {
  if (n <= 0) {
    yield seq;
    return;
  }
  for (let i = 0; i < seq.length; i += n) {
    yield seq.slice(i, i + n);
  }
}

export { hasEndpoints, staticTarget };
