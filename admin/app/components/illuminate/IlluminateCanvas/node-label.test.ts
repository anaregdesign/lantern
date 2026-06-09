import { describe, expect, test } from "bun:test";

import { makeDrawNodeLabel } from "./node-label";
import { FALLBACK_PALETTE, type SigmaPalette } from "./palette";

/**
 * Unit coverage for the always-on node-label renderer (#514).
 *
 * Like the hover renderer it is pure 2D-canvas drawing, so we drive it
 * with a fake `CanvasRenderingContext2D` that records each drawing call
 * together with the *active* style state at the moment of the call. The
 * suite asserts the chip is filled with `labelBackground`, stroked with a
 * 1px `labelStroke`, and the key text is painted in `labelText` — the
 * "Key が背景色とかぶって見えにくい" collision #514 fixes.
 */

interface RecordedOp {
  op: string;
  fillStyle: string;
  strokeStyle: string;
  lineWidth: number;
  args: unknown[];
}

function makeFakeContext(textWidth = 42) {
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

const SETTINGS = {
  labelSize: 13,
  labelFont: "system-ui, sans-serif",
  labelWeight: "600",
};

// A distinctive palette so each colour assertion is unambiguous (none of
// the three values collide).
const PALETTE: SigmaPalette = {
  ...FALLBACK_PALETTE,
  labelBackground: "#101010",
  labelStroke: "#808080",
  labelText: "#fafafa",
};

function draw(
  palette: SigmaPalette,
  data: { x: number; y: number; size: number; label?: string; color?: string },
  textWidth = 42,
) {
  const fake = makeFakeContext(textWidth);
  const fn = makeDrawNodeLabel(palette);
  fn(
    fake.ctx as unknown as CanvasRenderingContext2D,
    data as unknown as Parameters<ReturnType<typeof makeDrawNodeLabel>>[1],
    SETTINGS as unknown as Parameters<ReturnType<typeof makeDrawNodeLabel>>[2],
  );
  return fake;
}

describe("makeDrawNodeLabel", () => {
  test("fills the label chip with labelBackground", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "alpha",
      color: "#0078d4",
    });
    const fill = ops.find((o) => o.op === "fill");
    expect(fill).toBeDefined();
    expect(fill?.fillStyle).toBe(PALETTE.labelBackground);
  });

  test("outlines the chip with a 1px labelStroke border", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "alpha",
      color: "#0078d4",
    });
    const stroke = ops.find((o) => o.op === "stroke");
    expect(stroke).toBeDefined();
    expect(stroke?.strokeStyle).toBe(PALETTE.labelStroke);
    expect(stroke?.lineWidth).toBe(1);
  });

  test("draws the key text in labelText so it contrasts with the chip", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "alpha",
      color: "#0078d4",
    });
    const text = ops.find((o) => o.op === "fillText");
    expect(text).toBeDefined();
    expect(text?.fillStyle).toBe(PALETTE.labelText);
    // The text colour must differ from the box fill — that is the exact
    // collision #514 fixes.
    expect(text?.fillStyle).not.toBe(PALETTE.labelBackground);
  });

  test("positions the label at sigma's canonical offset (x + size + 3, y + size/3)", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "alpha",
      color: "#0078d4",
    });
    const text = ops.find((o) => o.op === "fillText");
    expect(text?.args).toEqual(["alpha", 100 + 8 + 3, 50 + 13 / 3]);
  });

  test("paints the chip before the text so the text lands on top", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "alpha",
      color: "#0078d4",
    });
    const fillIdx = ops.findIndex((o) => o.op === "fill");
    const textIdx = ops.findIndex((o) => o.op === "fillText");
    expect(fillIdx).toBeGreaterThanOrEqual(0);
    expect(textIdx).toBeGreaterThan(fillIdx);
  });

  test("draws nothing when the reduced label is empty (hover-dim path)", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "",
      color: "#0078d4",
    });
    expect(ops).toHaveLength(0);
  });

  test("draws nothing when the label is missing", () => {
    const { ops } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      color: "#0078d4",
    });
    expect(ops).toHaveLength(0);
  });
});
