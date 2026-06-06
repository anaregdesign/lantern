import { describe, expect, test } from "bun:test";
import { editEdgeReducer } from "./reducer";
import { INITIAL_EDIT_EDGE_STATE } from "./state";

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

  test("LOAD_RECEIVED seeds put inputs but clears add inputs", () => {
    const seeded = editEdgeReducer(
      { ...INITIAL_EDIT_EDGE_STATE, loadEpoch: 1 },
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
      { ...INITIAL_EDIT_EDGE_STATE, loadEpoch: 1 },
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
