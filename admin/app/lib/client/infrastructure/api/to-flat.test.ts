/**
 * Unit tests for the SDK rich-shape → flat-JSON conversion (#409),
 * focused on the permanent-vertex / permanent-edge expiration sentinel
 * (#654).
 *
 * A vertex or edge written with no TTL is permanent. The server encodes
 * that as the Go zero `time.Time` (`0001-01-01T00:00:00Z`), which the
 * Node SDK surfaces as a `Date` with a non-positive epoch value rather
 * than `null`. `sdkVertexToFlat` / `sdkEdgeToFlat` are the single
 * chokepoint every browse / search / detail / CLI surface reads through,
 * so dropping the sentinel here makes every downstream cell render
 * "never" instead of a long-expired chip ("739783d ago").
 */
import { describe, expect, test } from "bun:test";

import type { Edge as SdkEdge, Vertex as SdkVertex } from "lantern-sdk/web";

import { sdkEdgeToFlat, sdkVertexToFlat } from "./to-flat";

// The Go zero `time.Time` (`0001-01-01T00:00:00Z`) in epoch milliseconds.
const PROTO_ZERO_TIME = new Date("0001-01-01T00:00:00Z");
// The Unix epoch (`1970-01-01T00:00:00Z`) — the other non-positive sentinel.
const UNIX_EPOCH = new Date(0);
// A genuine, future expiration.
const FUTURE = new Date("2999-01-01T00:00:00Z");

function vertex(expiration: Date | null): SdkVertex {
  return { key: "k", value: "v", kind: "string", expiration };
}

function edge(expiration: Date | null): SdkEdge {
  return { tail: "a", head: "b", weight: 1, expiration };
}

describe("sdkVertexToFlat expiration", () => {
  test("drops the proto zero-time sentinel (permanent vertex)", () => {
    expect(sdkVertexToFlat(vertex(PROTO_ZERO_TIME)).expiration).toBeUndefined();
  });

  test("drops the Unix-epoch sentinel", () => {
    expect(sdkVertexToFlat(vertex(UNIX_EPOCH)).expiration).toBeUndefined();
  });

  test("omits expiration when the SDK reports null", () => {
    expect(sdkVertexToFlat(vertex(null)).expiration).toBeUndefined();
  });

  test("keeps a genuine future expiration as an ISO string", () => {
    expect(sdkVertexToFlat(vertex(FUTURE)).expiration).toBe(
      FUTURE.toISOString(),
    );
  });
});

describe("sdkEdgeToFlat expiration", () => {
  test("drops the proto zero-time sentinel (permanent edge)", () => {
    expect(sdkEdgeToFlat(edge(PROTO_ZERO_TIME)).expiration).toBeUndefined();
  });

  test("drops the Unix-epoch sentinel", () => {
    expect(sdkEdgeToFlat(edge(UNIX_EPOCH)).expiration).toBeUndefined();
  });

  test("omits expiration when the SDK reports null", () => {
    expect(sdkEdgeToFlat(edge(null)).expiration).toBeUndefined();
  });

  test("keeps a genuine future expiration as an ISO string", () => {
    expect(sdkEdgeToFlat(edge(FUTURE)).expiration).toBe(FUTURE.toISOString());
  });
});
