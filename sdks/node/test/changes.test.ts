import { describe, expect, test } from "bun:test";
import { create } from "@bufbuild/protobuf";
import { Code, ConnectError, type Transport } from "@connectrpc/connect";

import {
  FailedPreconditionError,
  IdentityNextCursor,
  InvalidArgumentError,
  Lantern,
  LanternError,
  type IdentityFrame,
  type ChangeFrame,
} from "../src/index.js";
import { connectWeb } from "../src/web.js";
import { decodeIdentityFrame } from "../src/changes.js";
import {
  HLCTimestampSchema,
  IdentityOperation,
  SubscribeResponseSchema,
  type SubscribeRequest,
  type SubscribeResponse,
} from "../src/gen/graph/v1/replication_pb.js";

const ORIGIN = "0102030405060708090a0b0c0d0e0f10";
const ORIGIN_BYTES = Uint8Array.from({ length: 16 }, (_, index) => index + 1);
const MAX_UINT64 = (1n << 64n) - 1n;

function checkpoint(lastSequences: Record<string, bigint> = { [ORIGIN]: 7n }): SubscribeResponse {
  return create(SubscribeResponseSchema, {
    event: { case: "checkpoint", value: { lastSeqPerOrigin: lastSequences } },
  });
}

function chunk(
  overrides: Partial<{
    sequence: bigint;
    operation: IdentityOperation;
    vertexKeys: string[];
    edgeKeys: { tail: string; head: string }[];
    hlcOrigin: Uint8Array;
    isLast: boolean;
  }> = {},
): SubscribeResponse {
  return create(SubscribeResponseSchema, {
    event: {
      case: "identityChunk",
      value: {
        origin: ORIGIN_BYTES,
        seq: overrides.sequence ?? 8n,
        hlc: create(HLCTimestampSchema, {
          wallNs: 123n,
          logical: 4,
          nodeId: overrides.hlcOrigin ?? ORIGIN_BYTES,
        }),
        operation: overrides.operation ?? IdentityOperation.PUT_VERTEX,
        chunkIndex: 0,
        firstItemIndex: 0,
        isLast: overrides.isLast ?? true,
        vertexKeys: overrides.vertexKeys ?? ["v"],
        edgeKeys: overrides.edgeKeys ?? [],
      },
    },
  });
}

function fakeTransport(
  messages: () => AsyncIterable<SubscribeResponse>,
  capture?: (
    request: SubscribeRequest,
    timeoutMs: number | undefined,
    signal: AbortSignal | undefined,
  ) => void,
): Transport {
  return {
    async unary() {
      throw new Error("unexpected unary call");
    },
    async stream(method, _signal, timeoutMs, _header, input) {
      const first = await input[Symbol.asyncIterator]().next();
      capture?.(first.value as SubscribeRequest, timeoutMs, _signal);
      return {
        service: method.parent,
        method,
        stream: true,
        header: new Headers(),
        trailer: new Headers(),
        message: messages(),
      };
    },
  } as unknown as Transport;
}

async function one(stream: AsyncIterable<IdentityFrame>): Promise<IdentityFrame> {
  for await (const frame of stream) return frame;
  throw new Error("identity stream ended without a frame");
}

async function collect(stream: AsyncIterable<IdentityFrame>): Promise<IdentityFrame[]> {
  const frames: IdentityFrame[] = [];
  for await (const frame of stream) frames.push(frame);
  return frames;
}

async function nextWithin<T>(
  iterator: AsyncIterator<T>,
  timeoutMs = 10_000,
): Promise<IteratorResult<T>> {
  let timer: ReturnType<typeof setTimeout> | undefined;
  try {
    return await Promise.race([
      iterator.next(),
      new Promise<never>((_, reject) => {
        timer = setTimeout(() => reject(new Error("identity stream timed out")), timeoutMs);
      }),
    ]);
  } finally {
    clearTimeout(timer);
  }
}

describe("identity-only CDC facade", () => {
  test("NEXT cursor is copied, immutable, and checked at uint64 boundaries", () => {
    const input = { [ORIGIN]: 1n };
    const cursor = new IdentityNextCursor(input);
    input[ORIGIN] = 2n;
    expect(cursor.nextSequences[ORIGIN]).toBe(1n);
    expect(Object.isFrozen(cursor.nextSequences)).toBe(true);
    expect(
      IdentityNextCursor.fromLastApplied({ [ORIGIN]: MAX_UINT64 - 1n }).nextSequences[ORIGIN],
    ).toBe(MAX_UINT64);
    expect(() => IdentityNextCursor.fromLastApplied({ [ORIGIN]: MAX_UINT64 })).toThrow(
      InvalidArgumentError,
    );
    for (const value of [0n, MAX_UINT64 + 1n]) {
      expect(() => new IdentityNextCursor({ [ORIGIN]: value })).toThrow(InvalidArgumentError);
    }
    for (const origin of [ORIGIN.toUpperCase(), "0".repeat(32), "bad"]) {
      expect(() => new IdentityNextCursor({ [origin]: 1n })).toThrow(InvalidArgumentError);
    }
  });

  test("typed frames copy exact identities and reject full or malformed frames", () => {
    const source = chunk({
      operation: IdentityOperation.ADD_EDGE,
      vertexKeys: [],
      edgeKeys: [{ tail: "tail", head: "head" }],
    });
    const decoded = decodeIdentityFrame(source);
    expect(decoded.kind).toBe("chunk");
    if (decoded.kind !== "chunk" || source.event.case !== "identityChunk") throw new Error();
    source.event.value.edgeKeys[0]!.tail = "changed";
    expect(decoded.edgeKeys).toEqual([{ tail: "tail", head: "head" }]);
    expect(decoded.hlc).toEqual({ wallNanoseconds: 123n, logical: 4, nodeId: ORIGIN });
    expect(Object.isFrozen(decoded.edgeKeys[0])).toBe(true);
    const cp = decodeIdentityFrame(checkpoint());
    expect(cp.kind).toBe("checkpoint");
    if (cp.kind !== "checkpoint") throw new Error();
    expect(cp.lastSequences[ORIGIN]).toBe(7n);
    expect(Object.isFrozen(cp.lastSequences)).toBe(true);

    const malformed = [
      create(SubscribeResponseSchema, {}),
      create(SubscribeResponseSchema, { event: { case: "mutation", value: {} } }),
      chunk({ sequence: 0n }),
      chunk({ operation: IdentityOperation.UNSPECIFIED }),
      chunk({ operation: IdentityOperation.ADD_EDGE }),
      chunk({ hlcOrigin: new Uint8Array(16) }),
      chunk({ vertexKeys: [], isLast: false }),
      chunk({ vertexKeys: [""] }),
      chunk({
        operation: IdentityOperation.ADD_EDGE,
        vertexKeys: [],
        edgeKeys: [{ tail: "", head: "h" }],
      }),
      chunk({
        operation: IdentityOperation.ADD_EDGE,
        vertexKeys: [],
        edgeKeys: [{ tail: "t", head: "" }],
      }),
      chunk({ vertexKeys: Array.from({ length: 1025 }, (_, index) => `v${index}`) }),
      chunk({ vertexKeys: ["x".repeat(1 << 20)] }),
      checkpoint({ bad: 1n }),
    ];
    const badIndex = chunk();
    if (badIndex.event.case !== "identityChunk") throw new Error();
    badIndex.event.value.firstItemIndex = 1;
    malformed.push(badIndex);
    for (const frame of malformed) {
      expect(() => decodeIdentityFrame(frame)).toThrow(LanternError);
    }
  });

  test("receipt-only final marker carries no invalidation keys", () => {
    const marker = chunk({
      operation: IdentityOperation.RECEIPT_ONLY,
      vertexKeys: [],
      edgeKeys: [],
    });
    const decoded = decodeIdentityFrame(marker);
    expect(decoded.kind).toBe("chunk");
    if (decoded.kind !== "chunk") throw new Error();
    expect(decoded.operation).toBe("receiptOnly");
    expect(decoded.isLast).toBe(true);
    expect(decoded.vertexKeys).toEqual([]);
    expect(decoded.edgeKeys).toEqual([]);
    for (const bad of [
      chunk({ operation: IdentityOperation.RECEIPT_ONLY, vertexKeys: ["x"] }),
      chunk({
        operation: IdentityOperation.RECEIPT_ONLY,
        vertexKeys: [],
        edgeKeys: [{ tail: "t", head: "h" }],
      }),
      chunk({ operation: IdentityOperation.RECEIPT_ONLY, vertexKeys: [], isLast: false }),
    ]) {
      expect(() => decodeIdentityFrame(bad)).toThrow(LanternError);
    }
    for (const field of ["chunkIndex", "firstItemIndex"] as const) {
      const bad = chunk({ operation: IdentityOperation.RECEIPT_ONLY, vertexKeys: [] });
      if (bad.event.case !== "identityChunk") throw new Error();
      bad.event.value[field] = 1;
      expect(() => decodeIdentityFrame(bad)).toThrow(LanternError);
    }
  });

  test("contribution Delete invalidates the edge key only", () => {
    const decoded = decodeIdentityFrame(
      chunk({
        operation: IdentityOperation.DELETE_EDGE_CONTRIBUTION,
        vertexKeys: [],
        edgeKeys: [{ tail: "t", head: "h" }],
      }),
    );
    expect(decoded.kind).toBe("chunk");
    if (decoded.kind !== "chunk") throw new Error();
    expect(decoded.operation).toBe("deleteEdgeContribution");
    expect(decoded.edgeKeys).toEqual([{ tail: "t", head: "h" }]);
    expect(decoded.vertexKeys).toEqual([]);
    expect(() =>
      decodeIdentityFrame(
        chunk({ operation: IdentityOperation.DELETE_EDGE_CONTRIBUTION, vertexKeys: ["v"] }),
      ),
    ).toThrow(LanternError);
  });

  test("bootstrap and resume use identity projection, preserve high-bit uint64, and skip the unary timeout", async () => {
    let request: SubscribeRequest | undefined;
    let timeoutMs: number | undefined = -1;
    const client = Lantern.withTransport(
      fakeTransport(
        async function* () {
          yield checkpoint();
          yield chunk();
        },
        (observed, timeout) => {
          request = observed;
          timeoutMs = timeout;
        },
      ),
      { defaultTimeoutMs: 1 },
    );
    const bootstrapStream = client.subscribeIdentity({ bootstrap: true });
    const iterator = bootstrapStream[Symbol.asyncIterator]();
    const frames = [(await iterator.next()).value, (await iterator.next()).value];
    await iterator.return?.();
    expect(frames.map((frame) => frame.kind)).toEqual(["checkpoint", "chunk"]);
    expect(request?.projection).toBe(2);
    expect(request?.bootstrap).toBe(true);
    expect(request?.fromLocalSeq).toBe(0n);
    expect(request?.fromSeqPerOrigin).toEqual({});
    expect(timeoutMs).toBeUndefined();

    const high = Lantern.withTransport(
      fakeTransport(
        async function* () {
          yield chunk({ sequence: MAX_UINT64 });
        },
        (observed, timeout) => {
          request = observed;
          timeoutMs = timeout;
        },
      ),
    );
    const frame = await one(
      high.subscribeIdentity({
        cursor: new IdentityNextCursor({ [ORIGIN]: MAX_UINT64 }),
        timeoutMs: 5000,
      }),
    );
    expect(request?.fromSeqPerOrigin[ORIGIN]).toBe(MAX_UINT64);
    expect(request?.bootstrap).toBe(false);
    expect(timeoutMs).toBe(5000);
    expect(frame.kind).toBe("chunk");
    if (frame.kind === "chunk") expect(frame.sequence).toBe(MAX_UINT64);
    await expect(
      one(
        high.subscribeIdentity({
          bootstrap: true,
          cursor: new IdentityNextCursor({ [ORIGIN]: 1n }),
        }),
      ),
    ).rejects.toThrow(InvalidArgumentError);
  });

  test("stream maps a retention gap and aborts its RPC on iterator cancellation", async () => {
    const gap = Lantern.withTransport(
      fakeTransport(async function* () {
        yield* [] as SubscribeResponse[];
        throw new ConnectError("gapped", Code.FailedPrecondition);
      }),
    );
    await expect(one(gap.subscribeIdentity())).rejects.toThrow(FailedPreconditionError);

    let rpcSignal: AbortSignal | undefined;
    const live = Lantern.withTransport(
      fakeTransport(
        async function* () {
          yield checkpoint();
          yield chunk();
        },
        (_request, _timeout, signal) => {
          rpcSignal = signal;
        },
      ),
    );
    const iterator = live.subscribeIdentity({ bootstrap: true })[Symbol.asyncIterator]();
    expect((await iterator.next()).value?.kind).toBe("checkpoint");
    expect(rpcSignal?.aborted).toBe(false);
    await iterator.return?.();
    expect(rpcSignal?.aborted).toBe(true);

    const caller = new AbortController();
    const secondStream = live.subscribeIdentity({ bootstrap: true }, caller.signal);
    const second = secondStream[Symbol.asyncIterator]();
    await second.next();
    expect(rpcSignal?.aborted).toBe(false);
    caller.abort();
    expect(rpcSignal?.aborted).toBe(true);
    await second.return?.();
  });

  test("bootstrap ordering and projection variants fail closed", async () => {
    for (const frames of [[] as SubscribeResponse[], [chunk()], [checkpoint(), checkpoint()]]) {
      const client = Lantern.withTransport(
        fakeTransport(async function* () {
          yield* frames;
        }),
      );
      await expect(collect(client.subscribeIdentity({ bootstrap: true }))).rejects.toThrow(
        LanternError,
      );
    }
    const resume = Lantern.withTransport(
      fakeTransport(async function* () {
        yield checkpoint();
      }),
    );
    await expect(collect(resume.subscribeIdentity())).rejects.toThrow(LanternError);
  });

  test("unexpected clean EOF requires recovery, but explicit cancellation does not", async () => {
    const resumed = Lantern.withTransport(
      fakeTransport(async function* () {
        yield chunk();
      }),
    );
    const stream = resumed.subscribeIdentity()[Symbol.asyncIterator]();
    expect((await stream.next()).value?.kind).toBe("chunk");
    await expect(stream.next()).rejects.toThrow(FailedPreconditionError);

    const bootstrapped = Lantern.withTransport(
      fakeTransport(async function* () {
        yield checkpoint();
      }),
    );
    const bootstrap = bootstrapped.subscribeIdentity({ bootstrap: true })[Symbol.asyncIterator]();
    expect((await bootstrap.next()).value?.kind).toBe("checkpoint");
    await expect(bootstrap.next()).rejects.toThrow(FailedPreconditionError);

    const canceled = new AbortController();
    canceled.abort();
    const empty = Lantern.withTransport(
      fakeTransport(async function* () {
        yield* [] as SubscribeResponse[];
      }),
    );
    expect(await collect(empty.subscribeIdentity({ bootstrap: true }, canceled.signal))).toEqual(
      [],
    );
  });
});

const wireEndpoint = process.env.LANTERN_NODE_REAL_WIRE_ENDPOINT;
const gapEndpoint = process.env.LANTERN_NODE_IDENTITY_GAP_ENDPOINT;
async function nextVisible(
  iterator: AsyncIterator<ChangeFrame>,
  key: string,
): Promise<ChangeFrame> {
  for (let count = 0; count < 100; count++) {
    const next = await nextWithin(iterator);
    if (next.done) throw new Error("public CDC ended before expected invalidation");
    if (next.value.invalidations.some((item) => item.kind === "vertex" && item.key === key))
      return next.value;
  }
  throw new Error("public CDC omitted expected invalidation");
}
if (wireEndpoint) {
  test("public CDC bootstraps and resumes with opaque checkpoints", async () => {
    const client = connectWeb(wireEndpoint);
    const prefix = `node-cdc-${crypto.randomUUID()}`;
    const firstKey = `${prefix}-first`;
    const secondKey = `${prefix}-second`;
    const frames = client.watchChanges({ prefix, bootstrap: true });
    const bootstrap = frames[Symbol.asyncIterator]();
    try {
      const first = await nextWithin(bootstrap);
      expect(first.value?.bootstrap).toBe(true);
      await client.putVertex({ key: firstKey, value: "identity-stream-must-not-carry-this" });
      const change = await nextVisible(bootstrap, firstKey);
      expect(change.cursor).toBeDefined();
      expect(change.invalidations.every((item) => item.current === undefined)).toBe(true);
      expect(JSON.stringify(change)).not.toContain("identity-stream-must-not-carry-this");
      const frames = client.watchChanges({ prefix, cursor: change.cursor! });
      const resumed = frames[Symbol.asyncIterator]();
      try {
        const waiting = nextVisible(resumed, secondKey);
        await client.putVertex({ key: secondKey, value: "second" });
        expect((await waiting).cursor).toBeDefined();
      } finally {
        await resumed.return?.();
      }
    } finally {
      await bootstrap.return?.();
      client.close();
    }
  });
}
if (gapEndpoint) {
  test("public CDC rejects an evicted opaque checkpoint", async () => {
    const client = connectWeb(gapEndpoint);
    const frames = client.watchChanges({ bootstrap: true });
    const stream = frames[Symbol.asyncIterator]();
    const first = await nextWithin(stream);
    expect(first.value?.cursor).toBeDefined();
    await stream.return?.();
    try {
      for (let index = 0; index < 3; index++)
        await client.putVertex({ key: `node-gap-${crypto.randomUUID()}`, value: "value" });
      const resumed = client.watchChanges({ cursor: first.value!.cursor! });
      await expect(resumed[Symbol.asyncIterator]().next()).rejects.toBeInstanceOf(
        FailedPreconditionError,
      );
    } finally {
      client.close();
    }
  });
}
