import { describe, expect, test } from "bun:test";
import { editEdgeReducer } from "./reducer";
import { INITIAL_EDIT_EDGE_STATE } from "./state";
import { selectAddBody } from "./selectors";

describe("editEdgeReducer", () => {
  test("TARGET_CHANGED bumps epoch", () => {
    const next = editEdgeReducer(INITIAL_EDIT_EDGE_STATE, {
      type: "TARGET_CHANGED",
      tail: "a",
      head: "b",
    });
    expect(next.tail).toBe("a");
    expect(next.head).toBe("b");
    expect(next.loadEpoch).toBe(1);
  });

  test("LOAD_RECEIVED seeds put inputs and keeps the untouched Add draft", () => {
    const seeded = editEdgeReducer(
      { ...INITIAL_EDIT_EDGE_STATE, loadEpoch: 1, loadStatus: "loading" },
      {
        type: "LOAD_RECEIVED",
        epoch: 1,
        edge: { tail: "a", head: "b", weight: 3 },
      },
    );
    expect(seeded.loadStatus).toBe("ready");
    expect(seeded.putInputs.weight).toBe("3");
    expect(seeded.addInputs.weight).toBe("1");
  });

  test("LOAD_RECEIVED with null marks not-found", () => {
    const out = editEdgeReducer(
      { ...INITIAL_EDIT_EDGE_STATE, loadEpoch: 1, loadStatus: "loading" },
      { type: "LOAD_RECEIVED", epoch: 1, edge: null },
    );
    expect(out.loadStatus).toBe("not-found");
    expect(out.edge).toBeNull();
  });

  test("WRITE_SUCCEEDED for add resets add inputs but keeps put inputs", () => {
    const start = {
      ...INITIAL_EDIT_EDGE_STATE,
      addInputs: { ...INITIAL_EDIT_EDGE_STATE.addInputs, weight: "5" },
      putInputs: { ...INITIAL_EDIT_EDGE_STATE.putInputs, weight: "10" },
      addStatus: "saving" as const,
    };
    const out = editEdgeReducer(start, {
      type: "WRITE_SUCCEEDED",
      mode: "add",
      edge: { tail: "a", head: "b", weight: 15 },
    });
    expect(out.addStatus).toBe("saved");
    expect(out.addInputs.weight).toBe("1");
    expect(out.putInputs.weight).toBe("10");
    expect(out.edge?.weight).toBe(15);
  });

  test("WRITE_SUCCEEDED for put refreshes put inputs from server", () => {
    const start = { ...INITIAL_EDIT_EDGE_STATE, putStatus: "saving" as const };
    const out = editEdgeReducer(start, {
      type: "WRITE_SUCCEEDED",
      mode: "put",
      edge: { tail: "a", head: "b", weight: 9.5 },
    });
    expect(out.putStatus).toBe("saved");
    expect(out.putInputs.weight).toBe("9.5");
  });

  test("DELETE_SUCCEEDED clears edge state", () => {
    const start = {
      ...INITIAL_EDIT_EDGE_STATE,
      edge: { tail: "a", head: "b", weight: 1 },
      deleteRequested: true,
      deleteStatus: "deleting" as const,
    };
    const out = editEdgeReducer(start, { type: "DELETE_SUCCEEDED" });
    expect(out.edge).toBeNull();
    expect(out.deleteStatus).toBe("deleted");
    expect(out.deleteRequested).toBe(false);
  });
});

describe("late initial Edge reads", () => {
  for (const edge of [null, { tail: "a", head: "b", weight: 3 }]) {
    test(`preserves an authored Add weight and TTL when the read returns ${edge ? "an edge" : "not-found"}`, () => {
      let state = editEdgeReducer(INITIAL_EDIT_EDGE_STATE, {
        type: "TARGET_CHANGED",
        tail: "a",
        head: "b",
      });
      state = editEdgeReducer(state, {
        type: "LOAD_REQUESTED",
        epoch: state.loadEpoch,
      });
      state = editEdgeReducer(state, {
        type: "WEIGHT_CHANGED",
        mode: "add",
        value: "7",
      });
      state = editEdgeReducer(state, {
        type: "TTL_CHANGED",
        mode: "add",
        ttl: { mode: "preset24h", custom: "" },
      });
      const now = Date.parse("2026-10-08T00:00:00Z");
      const authoredBody = selectAddBody(state, now);
      expect(authoredBody.body?.edge).toEqual({
        weight: 7,
        expiration: "2026-10-09T00:00:00.000Z",
      });
      const saving = editEdgeReducer(state, {
        type: "WRITE_REQUESTED",
        mode: "add",
      });
      const readWhileSaving = editEdgeReducer(saving, {
        type: "LOAD_RECEIVED",
        epoch: saving.loadEpoch,
        edge,
      });
      expect(selectAddBody(readWhileSaving, now)).toEqual(authoredBody);
      expect(readWhileSaving.addStatus).toBe("saving");
      state = editEdgeReducer(state, {
        type: "LOAD_RECEIVED",
        epoch: state.loadEpoch,
        edge,
      });
      expect(selectAddBody(state, now)).toEqual(authoredBody);
      expect(state.putInputs.weight).toBe(edge ? "3" : "1");

      state = editEdgeReducer(state, { type: "WRITE_REQUESTED", mode: "add" });
      expect(selectAddBody(state, now)).toEqual(authoredBody);
    });
  }

  for (const edge of [null, { tail: "a", head: "b", weight: 9 }]) {
    test(`a late initial read cannot replace a completed ${edge ? "live" : "absent"} Add result or a new draft`, () => {
      let state = editEdgeReducer(INITIAL_EDIT_EDGE_STATE, {
        type: "TARGET_CHANGED",
        tail: "a",
        head: "b",
      });
      state = editEdgeReducer(state, {
        type: "LOAD_REQUESTED",
        epoch: state.loadEpoch,
      });
      state = editEdgeReducer(state, {
        type: "WEIGHT_CHANGED",
        mode: "add",
        value: "7",
      });
      const sentBody = selectAddBody(state);
      state = editEdgeReducer(state, { type: "WRITE_REQUESTED", mode: "add" });
      state = editEdgeReducer(state, {
        type: "WRITE_SUCCEEDED",
        mode: "add",
        edge,
      });
      expect(sentBody.body?.edge?.weight).toBe(7);
      expect(state.addInputs.weight).toBe("1");
      state = editEdgeReducer(state, {
        type: "WEIGHT_CHANGED",
        mode: "add",
        value: "11",
      });
      for (const completion of [
        {
          type: "LOAD_RECEIVED",
          epoch: state.loadEpoch,
          edge: { tail: "a", head: "b", weight: 2 },
        },
        {
          type: "LOAD_FAILED",
          epoch: state.loadEpoch,
          error: "late read failure",
        },
      ] as const) {
        expect(editEdgeReducer(state, completion)).toBe(state);
      }
      expect(state.edge).toEqual(edge);
      expect(state.loadStatus).toBe(edge ? "ready" : "not-found");
      expect(state.addInputs.weight).toBe("11");
    });
  }

  test("target changes reset the draft and ignore the prior target read", () => {
    let state = editEdgeReducer(INITIAL_EDIT_EDGE_STATE, {
      type: "TARGET_CHANGED",
      tail: "a",
      head: "b",
    });
    const priorEpoch = state.loadEpoch;
    state = editEdgeReducer(state, {
      type: "WEIGHT_CHANGED",
      mode: "add",
      value: "7",
    });
    state = editEdgeReducer(state, {
      type: "TARGET_CHANGED",
      tail: "c",
      head: "d",
    });
    expect(state.addInputs).toEqual(INITIAL_EDIT_EDGE_STATE.addInputs);
    expect(
      editEdgeReducer(state, {
        type: "LOAD_RECEIVED",
        epoch: priorEpoch,
        edge: { tail: "a", head: "b", weight: 2 },
      }),
    ).toBe(state);
  });
});

test("blind acceptance clears stale Edge values without claiming absence", () => {
  const start = {
    ...INITIAL_EDIT_EDGE_STATE,
    edge: { tail: "a", head: "b", weight: 99 },
    loadStatus: "ready" as const,
  };
  for (const action of [
    { type: "WRITE_ACCEPTED_UNDISCLOSED", mode: "add" },
    { type: "WRITE_ACCEPTED_UNDISCLOSED", mode: "put" },
    { type: "DELETE_ACCEPTED_UNDISCLOSED" },
  ] as const) {
    const next = editEdgeReducer(start, action);
    expect(next.edge).toBeNull();
    expect(next.loadStatus).toBe("undisclosed");
    expect(next.deleteStatus).not.toBe("deleted");
  }
});

test("a fresh authored intent releases the handled form without retrying", () => {
  const accepted = editEdgeReducer(INITIAL_EDIT_EDGE_STATE, {
    type: "WRITE_ACCEPTED_UNDISCLOSED",
    mode: "add",
  });
  const next = editEdgeReducer(accepted, {
    type: "WEIGHT_CHANGED",
    mode: "add",
    value: "3",
  });
  expect(next.addStatus).toBe("idle");
  expect(next.edge).toBeNull();
  expect(next.loadStatus).toBe("undisclosed");
});
