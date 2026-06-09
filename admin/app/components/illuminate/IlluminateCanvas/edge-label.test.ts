import { describe, expect, test } from "bun:test";

import { formatEdgeWeight, makeDrawEdgeLabel } from "./edge-label";
import { FALLBACK_PALETTE, type SigmaPalette } from "./palette";

describe("formatEdgeWeight", () => {
  test("renders a whole number without a decimal point", () => {
    expect(formatEdgeWeight(3)).toBe("3");
    expect(formatEdgeWeight(0)).toBe("0");
    expect(formatEdgeWeight(42)).toBe("42");
  });

  test("renders a fractional value to one decimal", () => {
    expect(formatEdgeWeight(2.5)).toBe("2.5");
    expect(formatEdgeWeight(0.3)).toBe("0.3");
  });

  test("rounds to a single decimal place", () => {
    expect(formatEdgeWeight(1.24)).toBe("1.2");
    expect(formatEdgeWeight(1.26)).toBe("1.3");
  });

  test("returns an empty string for non-finite input", () => {
    expect(formatEdgeWeight(Number.NaN)).toBe("");
    expect(formatEdgeWeight(Number.POSITIVE_INFINITY)).toBe("");
  });
});

/**
 * Unit coverage for the always-on edge-label renderer (#514). Like the
 * node renderer it is pure 2D-canvas drawing, so we drive it with a fake
 * `CanvasRenderingContext2D` that records each drawing call together with
 * the active style at the moment of the call — plus `save`/`restore`/
 * `translate`/`rotate`, which the edge renderer uses to draw the weight
 * rotated along the edge.
 */
interface RecordedOp {
  op: string;
  fillStyle: string;
  strokeStyle: string;
  lineWidth: number;
  args: unknown[];
}

function makeFakeContext(textWidth = 20) {
  const ops: RecordedOp[] = [];
  const state = {
    font: "",
    fillStyle: "",
    strokeStyle: "",
    lineWidth: 0,
    shadowOffsetX: 0,
    shadowOffsetY: 0,
    shadowBlur: 0,
  };
  const record = (op: string, args: unknown[] = []) =>
    ops.push({
      op,
      fillStyle: state.fillStyle,
      strokeStyle: state.strokeStyle,
      lineWidth: state.lineWidth,
      args,
    });

  const ctx = {
    get font() {
      return state.font;
    },
    set font(v: string) {
      state.font = v;
    },
    get fillStyle() {
      return state.fillStyle;
    },
    set fillStyle(v: string) {
      state.fillStyle = v;
    },
    get strokeStyle() {
      return state.strokeStyle;
    },
    set strokeStyle(v: string) {
      state.strokeStyle = v;
    },
    get lineWidth() {
      return state.lineWidth;
    },
    set lineWidth(v: number) {
      state.lineWidth = v;
    },
    get shadowOffsetX() {
      return state.shadowOffsetX;
    },
    set shadowOffsetX(v: number) {
      state.shadowOffsetX = v;
    },
    get shadowOffsetY() {
      return state.shadowOffsetY;
    },
    set shadowOffsetY(v: number) {
      state.shadowOffsetY = v;
    },
    get shadowBlur() {
      return state.shadowBlur;
    },
    set shadowBlur(v: number) {
      state.shadowBlur = v;
    },
    measureText: (_t: string) => ({ width: textWidth }),
    save: () => record("save"),
    restore: () => record("restore"),
    translate: (...a: number[]) => record("translate", a),
    rotate: (...a: number[]) => record("rotate", a),
    beginPath: () => record("beginPath"),
    moveTo: (...a: number[]) => record("moveTo", a),
    lineTo: (...a: number[]) => record("lineTo", a),
    closePath: () => record("closePath"),
    fill: () => record("fill"),
    stroke: () => record("stroke"),
    fillText: (...a: unknown[]) => record("fillText", a),
  };

  return { ctx, ops, state };
}

const EDGE_SETTINGS = {
  edgeLabelSize: 11,
  edgeLabelFont: "system-ui, sans-serif",
  edgeLabelWeight: "600",
};

const PALETTE: SigmaPalette = {
  ...FALLBACK_PALETTE,
  labelBackground: "#101010",
  labelStroke: "#808080",
  labelText: "#fafafa",
};

function drawEdge(
  data: { label?: string },
  sourceData: { x: number; y: number; size: number },
  targetData: { x: number; y: number; size: number },
  textWidth = 20,
) {
  const fake = makeFakeContext(textWidth);
  const fn = makeDrawEdgeLabel(PALETTE);
  type Args = Parameters<ReturnType<typeof makeDrawEdgeLabel>>;
  fn(
    fake.ctx as unknown as CanvasRenderingContext2D,
    data as unknown as Args[1],
    sourceData as unknown as Args[2],
    targetData as unknown as Args[3],
    EDGE_SETTINGS as unknown as Args[4],
  );
  return fake;
}

describe("makeDrawEdgeLabel", () => {
  const SRC = { x: 0, y: 0, size: 7 };
  const TGT = { x: 100, y: 0, size: 7 };

  test("fills the weight chip with labelBackground", () => {
    const { ops } = drawEdge({ label: "3" }, SRC, TGT);
    const fill = ops.find((o) => o.op === "fill");
    expect(fill?.fillStyle).toBe(PALETTE.labelBackground);
  });

  test("outlines the chip with a 1px labelStroke border", () => {
    const { ops } = drawEdge({ label: "3" }, SRC, TGT);
    const stroke = ops.find((o) => o.op === "stroke");
    expect(stroke?.strokeStyle).toBe(PALETTE.labelStroke);
    expect(stroke?.lineWidth).toBe(1);
  });

  test("draws the weight in labelText so it contrasts with the chip", () => {
    const { ops } = drawEdge({ label: "3" }, SRC, TGT);
    const text = ops.find((o) => o.op === "fillText");
    expect(text?.fillStyle).toBe(PALETTE.labelText);
    expect(text?.fillStyle).not.toBe(PALETTE.labelBackground);
    expect((text?.args ?? [])[0]).toBe("3");
  });

  test("centres on the edge midpoint and rotates within a hairpin frame", () => {
    const { ops } = drawEdge({ label: "3" }, SRC, TGT);
    const translate = ops.find((o) => o.op === "translate");
    // Midpoint of (0,0)→(100,0) is (50,0).
    expect(translate?.args).toEqual([50, 0]);
    const rotate = ops.find((o) => o.op === "rotate");
    const a = (rotate?.args ?? [])[0] as number;
    expect(Math.abs(a)).toBeLessThanOrEqual(Math.PI / 2 + 1e-9);
    // Save/restore must bracket the draw so the rotation never leaks.
    expect(ops[0]?.op).toBe("save");
    expect(ops[ops.length - 1]?.op).toBe("restore");
  });

  test("draws short edges too (no length-based skip)", () => {
    // Source and target almost coincident — sigma's default renderer
    // would skip this; #514's renderer must still show the weight.
    const { ops } = drawEdge({ label: "9" }, SRC, { x: 1, y: 0, size: 7 });
    expect(ops.some((o) => o.op === "fillText")).toBe(true);
  });

  test("draws nothing when the edge has no label", () => {
    const { ops } = drawEdge({}, SRC, TGT);
    expect(ops).toHaveLength(0);
  });
});
