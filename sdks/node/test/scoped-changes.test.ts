import { expect, test } from "bun:test";
import { create, fromBinary, toBinary } from "@bufbuild/protobuf";
import { createRouterTransport } from "@connectrpc/connect";
import { Lantern } from "../src/client.js";
import { ChangeCursor, decodeChangeFrame } from "../src/scoped-changes.js";
import {
  LanternChangeService,
  WatchChangesResponseSchema,
} from "../src/gen/graph/v1/changes_pb.js";
import { VertexSchema } from "../src/gen/graph/v1/graph_pb.js";
import { LanternError } from "../src/errors.js";

test("opaque cursor is copied and identity mode never exposes current values", () => {
  const bytes = new Uint8Array(32).fill(3);
  const cursor = new ChangeCursor(bytes);
  bytes[0] = 9;
  expect(cursor.toBytes()[0]).toBe(3);
  const copy = cursor.toBytes();
  copy[0] = 8;
  expect(cursor.toBytes()[0]).toBe(3);
  const value = create(WatchChangesResponseSchema, {
    invalidations: [
      {
        identity: { case: "vertexKey", value: "tenant:a" },
        currentImage: {
          case: "vertex",
          value: { key: "tenant:a", value: { case: "string", value: "current" } },
        },
      },
    ],
  });
  expect(() => decodeChangeFrame(value, "identity")).toThrow(LanternError);
  expect(decodeChangeFrame(value, "value").invalidations[0]).toMatchObject({
    kind: "vertex",
    key: "tenant:a",
    current: { key: "tenant:a", value: "current" },
  });
});
test("image identity mismatch and missing invalidation identity fail closed", () => {
  const frame = create(WatchChangesResponseSchema, {
    invalidations: [
      {
        identity: { case: "vertexKey", value: "tenant:a" },
        currentImage: { case: "vertex", value: { key: "other" } },
      },
    ],
  });
  expect(() => decodeChangeFrame(frame, "value")).toThrow();
  expect(() =>
    decodeChangeFrame(create(WatchChangesResponseSchema, { invalidations: [{}] }), "identity"),
  ).toThrow();
});
test("public stream keeps one attempt, ignores unary timeout and cancels on iterator return", async () => {
  let opens = 0;
  let signal: AbortSignal | undefined;
  const client = Lantern.withTransport(
    createRouterTransport(({ service }) =>
      service(LanternChangeService, {
        watchChanges: async function* (request, context) {
          opens++;
          signal = context.signal;
          expect(request.prefix).toBe("tenant:");
          expect(context.timeoutMs()).toBeUndefined();
          yield { bootstrap: true, cursor: new Uint8Array(32).fill(1) };
          await new Promise<void>((resolve) =>
            context.signal.addEventListener("abort", () => resolve(), { once: true }),
          );
        },
      }),
    ),
    { defaultTimeoutMs: 1 },
  );
  const frames = client.watchChanges({ bootstrap: true, prefix: "tenant:" });
  const stream = frames[Symbol.asyncIterator]();
  expect((await stream.next()).value?.bootstrap).toBe(true);
  await stream.return?.();
  expect(signal?.aborted).toBe(true);
  expect(opens).toBe(1);
});

test("unknown nested value fields cannot silently advance a public checkpoint", () => {
  const image = fromBinary(
    VertexSchema,
    new Uint8Array([
      ...toBinary(VertexSchema, create(VertexSchema, { key: "tenant:a" })),
      0x98,
      0x06,
      1,
    ]),
  );
  const frame = create(WatchChangesResponseSchema, {
    cursor: new Uint8Array([1]),
    invalidations: [
      {
        identity: { case: "vertexKey", value: "tenant:a" },
        currentImage: { case: "vertex", value: image },
      },
    ],
  });
  expect(() => decodeChangeFrame(frame, "value")).toThrow(LanternError);
});
