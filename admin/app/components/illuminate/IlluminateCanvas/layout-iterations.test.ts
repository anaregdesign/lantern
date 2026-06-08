import { describe, expect, test } from "bun:test";
import {
  FA2_ITERATIONS_ADDITIVE,
  FA2_ITERATIONS_COLD,
  FA2_ITERATIONS_DROP,
  decideFa2Iterations,
} from "./layout-iterations";

describe("decideFa2Iterations", () => {
  test("empty graph after reconcile → 0 iterations", () => {
    expect(
      decideFa2Iterations({
        previousNodeCount: 0,
        addedCount: 0,
        droppedCount: 0,
        nextNodeCount: 0,
      }),
    ).toBe(0);
    // even if the prior graph held nodes that all got dropped, still 0:
    expect(
      decideFa2Iterations({
        previousNodeCount: 5,
        addedCount: 0,
        droppedCount: 5,
        nextNodeCount: 0,
      }),
    ).toBe(0);
  });

  test("cold mount (empty → populated) runs the full 80-iteration warm-up", () => {
    expect(
      decideFa2Iterations({
        previousNodeCount: 0,
        addedCount: 5,
        droppedCount: 0,
        nextNodeCount: 5,
      }),
    ).toBe(FA2_ITERATIONS_COLD);
  });

  test("post-Clear reseed (empty → populated, regardless of accounting) cold-starts", () => {
    // After CLEAR the previous reconcile already dropped every node;
    // the NEXT reconcile sees previousNodeCount=0 and treats it as cold.
    expect(
      decideFa2Iterations({
        previousNodeCount: 0,
        addedCount: 12,
        droppedCount: 0,
        nextNodeCount: 12,
      }),
    ).toBe(FA2_ITERATIONS_COLD);
  });

  test("pure attribute refresh (no add/drop, same node set) skips FA2", () => {
    expect(
      decideFa2Iterations({
        previousNodeCount: 5,
        addedCount: 0,
        droppedCount: 0,
        nextNodeCount: 5,
      }),
    ).toBe(0);
  });

  test("additive expansion (only new nodes added) uses the cheap 5-iter relax", () => {
    expect(
      decideFa2Iterations({
        previousNodeCount: 5,
        addedCount: 1,
        droppedCount: 0,
        nextNodeCount: 6,
      }),
    ).toBe(FA2_ITERATIONS_ADDITIVE);
    expect(
      decideFa2Iterations({
        previousNodeCount: 5,
        addedCount: 7,
        droppedCount: 0,
        nextNodeCount: 12,
      }),
    ).toBe(FA2_ITERATIONS_ADDITIVE);
  });

  test("drops (TTL expiry, soft-cap evict, etc.) get the 30-iter partial settle", () => {
    expect(
      decideFa2Iterations({
        previousNodeCount: 6,
        addedCount: 0,
        droppedCount: 1,
        nextNodeCount: 5,
      }),
    ).toBe(FA2_ITERATIONS_DROP);
    // add + drop in the same reconcile is still treated as a drop case.
    expect(
      decideFa2Iterations({
        previousNodeCount: 6,
        addedCount: 2,
        droppedCount: 1,
        nextNodeCount: 7,
      }),
    ).toBe(FA2_ITERATIONS_DROP);
  });
});
