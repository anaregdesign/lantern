import { describe, expect, test } from "bun:test";
import {
  buildAddEdgeBody,
  buildPutEdgeBody,
  inputsFromEdge,
} from "./edge-codec";
import { INITIAL_TTL_INPUT } from "../edit-vertex/value-codec";

const FIXED_NOW = Date.parse("2026-01-02T03:04:05Z");

describe("edge-codec", () => {
  test("buildAddEdgeBody with positive weight + preset TTL", () => {
    const out = buildAddEdgeBody(
      { weight: "0.5", ttl: { ...INITIAL_TTL_INPUT, mode: "preset5m" } },
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.edge?.weight).toBe(0.5);
    expect(out.body?.edge?.expiration).toBe(
      new Date(FIXED_NOW + 5 * 60_000).toISOString(),
    );
  });

  test("buildPutEdgeBody allows negative weight", () => {
    const out = buildPutEdgeBody(
      { weight: "-1.25", ttl: { ...INITIAL_TTL_INPUT, mode: "none" } },
      FIXED_NOW,
    );
    expect(out.error).toBeNull();
    expect(out.body?.edge?.weight).toBe(-1.25);
    expect(out.body?.edge?.expiration).toBeUndefined();
  });

  test("rejects empty weight", () => {
    const out = buildAddEdgeBody(
      { weight: "", ttl: INITIAL_TTL_INPUT },
      FIXED_NOW,
    );
    expect(out.body).toBeNull();
    expect(out.error).toMatch(/weight/i);
  });

  test("rejects NaN weight", () => {
    const out = buildPutEdgeBody(
      { weight: "abc", ttl: INITIAL_TTL_INPUT },
      FIXED_NOW,
    );
    expect(out.body).toBeNull();
  });

  test("rejects invalid custom TTL", () => {
    const out = buildAddEdgeBody(
      {
        weight: "1",
        ttl: { mode: "custom", custom: "xyz" },
      },
      FIXED_NOW,
    );
    expect(out.body).toBeNull();
  });

  test("inputsFromEdge seeds weight from existing edge", () => {
    expect(inputsFromEdge({ tail: "a", head: "b", weight: 2.5 }).weight).toBe(
      "2.5",
    );
    expect(inputsFromEdge(null).weight).toBe("1");
  });
});
