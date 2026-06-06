import { describe, expect, test } from "bun:test";
import { editVertexReducer } from "./reducer";
import { INITIAL_EDIT_VERTEX_STATE } from "./state";

describe("editVertexReducer", () => {
  test("KEY_CHANGED bumps epoch and resets", () => {
    const s1 = editVertexReducer(INITIAL_EDIT_VERTEX_STATE, {
      type: "KEY_CHANGED",
      key: "a",
    });
    expect(s1.key).toBe("a");
    expect(s1.loadEpoch).toBe(1);
    expect(s1.loadStatus).toBe("idle");
  });

  test("LOAD_REQUESTED only applies for matching epoch", () => {
    const s1 = editVertexReducer(INITIAL_EDIT_VERTEX_STATE, {
      type: "KEY_CHANGED",
      key: "a",
    });
    const stale = editVertexReducer(s1, {
      type: "LOAD_REQUESTED",
      epoch: 0,
    });
    expect(stale.loadStatus).toBe("idle");
    const fresh = editVertexReducer(s1, {
      type: "LOAD_REQUESTED",
      epoch: 1,
    });
    expect(fresh.loadStatus).toBe("loading");
  });

  test("LOAD_RECEIVED with null marks not-found", () => {
    const s1 = editVertexReducer(INITIAL_EDIT_VERTEX_STATE, {
      type: "KEY_CHANGED",
      key: "x",
    });
    const s2 = editVertexReducer(s1, {
      type: "LOAD_RECEIVED",
      epoch: 1,
      vertex: null,
    });
    expect(s2.loadStatus).toBe("not-found");
    expect(s2.vertex).toBeNull();
  });

  test("LOAD_RECEIVED seeds inputs and kind", () => {
    const s1 = editVertexReducer(INITIAL_EDIT_VERTEX_STATE, {
      type: "KEY_CHANGED",
      key: "x",
    });
    const s2 = editVertexReducer(s1, {
      type: "LOAD_RECEIVED",
      epoch: 1,
      vertex: { key: "x", int32: 42 },
    });
    expect(s2.loadStatus).toBe("ready");
    expect(s2.kind).toBe("int32");
    expect(s2.inputs.int32).toBe("42");
  });

  test("EDIT_BEGUN requires loaded state", () => {
    const begun = editVertexReducer(INITIAL_EDIT_VERTEX_STATE, {
      type: "EDIT_BEGUN",
    });
    expect(begun.mode).toBe("view");
    const ready = editVertexReducer(
      {
        ...INITIAL_EDIT_VERTEX_STATE,
        loadStatus: "ready",
        vertex: { key: "x", string: "y" },
      },
      { type: "EDIT_BEGUN" },
    );
    expect(ready.mode).toBe("edit");
  });

  test("EDIT_CANCELED restores inputs from the loaded vertex", () => {
    const loaded = editVertexReducer(
      {
        ...INITIAL_EDIT_VERTEX_STATE,
        loadStatus: "ready",
        vertex: { key: "x", string: "original" },
      },
      { type: "EDIT_BEGUN" },
    );
    const typed = editVertexReducer(loaded, {
      type: "INPUT_CHANGED",
      field: "string",
      value: "typed",
    });
    expect(typed.inputs.string).toBe("typed");
    const cancelled = editVertexReducer(typed, { type: "EDIT_CANCELED" });
    expect(cancelled.mode).toBe("view");
    expect(cancelled.inputs.string).toBe("original");
  });

  test("SAVE_SUCCEEDED returns to view mode with fresh inputs", () => {
    const editing = editVertexReducer(
      {
        ...INITIAL_EDIT_VERTEX_STATE,
        mode: "edit",
        saveStatus: "saving",
      },
      {
        type: "SAVE_SUCCEEDED",
        vertex: { key: "x", float64: 1.25 },
      },
    );
    expect(editing.mode).toBe("view");
    expect(editing.kind).toBe("float64");
    expect(editing.inputs.float64).toBe("1.25");
    expect(editing.saveStatus).toBe("saved");
  });

  test("delete dialog lifecycle", () => {
    const opened = editVertexReducer(INITIAL_EDIT_VERTEX_STATE, {
      type: "DELETE_OPENED",
    });
    expect(opened.deleteRequested).toBe(true);
    const cancelled = editVertexReducer(opened, { type: "DELETE_CANCELED" });
    expect(cancelled.deleteRequested).toBe(false);
    const requesting = editVertexReducer(opened, {
      type: "DELETE_REQUESTED",
    });
    expect(requesting.deleteStatus).toBe("deleting");
    const done = editVertexReducer(requesting, {
      type: "DELETE_SUCCEEDED",
    });
    expect(done.deleteStatus).toBe("deleted");
    expect(done.vertex).toBeNull();
  });

  test("KIND_CHANGED keeps existing inputs", () => {
    const s = editVertexReducer(
      { ...INITIAL_EDIT_VERTEX_STATE, mode: "edit" },
      { type: "INPUT_CHANGED", field: "float64", value: "1.5" },
    );
    const after = editVertexReducer(s, {
      type: "KIND_CHANGED",
      kind: "int32",
    });
    expect(after.kind).toBe("int32");
    expect(after.inputs.float64).toBe("1.5");
  });
});
