import { describe, expect, test } from "bun:test";

import { makeDrawNodeHover } from "./hover-label";
import { FALLBACK_PALETTE, type SigmaPalette } from "./palette";

/**
 * Unit coverage for the theme-aware hover-label renderer (#484).
 *
 * The renderer is pure 2D-canvas drawing, so we drive it with a fake
 * `CanvasRenderingContext2D` that records each drawing call together with
 * the *active* style state at the moment of the call. That lets the suite
 * assert the box was filled with `labelBackground`, stroked with
 * `labelStroke`, and that the label text was painted in `labelText` — the
 * collision #484 fixes is precisely "text drawn in the same colour as the
 * box behind it".
 */

interface RecordedOp {
  op: string;
  fillStyle: string;
  strokeStyle: string;
  lineWidth: number;
  shadowBlur: number;
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
    shadowColor: "",
  };
  const record = (op: string, args: unknown[] = []) =>
    ops.push({
      op,
      fillStyle: state.fillStyle,
      strokeStyle: state.strokeStyle,
      lineWidth: state.lineWidth,
      shadowBlur: state.shadowBlur,
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
    get shadowColor() {
      return state.shadowColor;
    },
    set shadowColor(v: string) {
      state.shadowColor = v;
    },
    measureText: (_t: string) => ({ width: textWidth }),
    beginPath: () => record("beginPath"),
    moveTo: (...a: number[]) => record("moveTo", a),
    lineTo: (...a: number[]) => record("lineTo", a),
    arc: (...a: number[]) => record("arc", a),
    closePath: () => record("closePath"),
    fill: () => record("fill"),
    stroke: () => record("stroke"),
    fillText: (...a: unknown[]) => record("fillText", a),
  };

  return { ctx, ops, state };
}

// Minimal sigma settings the renderer reads. The full Settings type is
// large; the renderer only touches these three fields, so we cast a
// partial through `unknown` at the call site.
const SETTINGS = {
  labelSize: 13,
  labelFont: "system-ui, sans-serif",
  labelWeight: "600",
};

// A distinctive palette so each colour assertion is unambiguous (none of
// the four values collide).
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
  const fn = makeDrawNodeHover(palette);
  fn(
    fake.ctx as unknown as CanvasRenderingContext2D,
    data as unknown as Parameters<ReturnType<typeof makeDrawNodeHover>>[1],
    SETTINGS as unknown as Parameters<ReturnType<typeof makeDrawNodeHover>>[2],
  );
  return fake;
}

describe("makeDrawNodeHover", () => {
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

  test("draws the label text in labelText so it contrasts with the chip", () => {
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
    // collision #484 fixes.
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

  test("draws no drop shadow (shadowBlur stays 0 through the fill)", () => {
    const { ops, state } = draw(PALETTE, {
      x: 100,
      y: 50,
      size: 8,
      label: "alpha",
      color: "#0078d4",
    });
    const fill = ops.find((o) => o.op === "fill");
    expect(fill?.shadowBlur).toBe(0);
    expect(state.shadowBlur).toBe(0);
  });

  test("sets the font from sigma's label settings", () => {
    const { state } = draw(PALETTE, {
      x: 0,
      y: 0,
      size: 6,
      label: "x",
      color: "#000",
    });
    expect(state.font).toBe("600 13px system-ui, sans-serif");
  });

  test("falls back to a plain disc halo and paints no text when the node has no label", () => {
    const { ops } = draw(PALETTE, { x: 10, y: 20, size: 6, color: "#000" });
    // The label-less branch draws a full-circle arc, fills + strokes it,
    // and never calls fillText.
    const arc = ops.find((o) => o.op === "arc");
    expect(arc).toBeDefined();
    expect(arc?.args).toEqual([10, 20, 6 + 2, 0, Math.PI * 2]);
    expect(ops.some((o) => o.op === "fill")).toBe(true);
    expect(ops.some((o) => o.op === "stroke")).toBe(true);
    expect(ops.some((o) => o.op === "fillText")).toBe(false);
  });

  test("measures the label to size the chip width", () => {
    // A label-bearing node must measure its text (the box width derives
    // from it). We assert the renderer routes through measureText by
    // giving the fake a known width and checking a rectangle vertex uses
    // it: the right edge is at x + radius + round(textWidth + 5).
    const textWidth = 60;
    const { ops } = draw(
      PALETTE,
      { x: 100, y: 50, size: 8, label: "alpha", color: "#0078d4" },
      textWidth,
    );
    const radius = Math.max(8, 13 / 2) + 2; // matches the renderer
    const expectedRight = 100 + radius + Math.round(textWidth + 5);
    const rightVertex = ops.find(
      (o) => o.op === "lineTo" && (o.args as number[])[0] === expectedRight,
    );
    expect(rightVertex).toBeDefined();
  });

  test("the fallback palette already separates the hover box from the label text", () => {
    // Guard the real shipped default: the light fallback chip (#ffffff)
    // and label (#242424) must contrast even before Fluent hydrates.
    const { ops } = draw(FALLBACK_PALETTE, {
      x: 5,
      y: 5,
      size: 7,
      label: "node",
      color: "#000",
    });
    const fill = ops.find((o) => o.op === "fill");
    const text = ops.find((o) => o.op === "fillText");
    expect(fill?.fillStyle).toBe(FALLBACK_PALETTE.labelBackground);
    expect(text?.fillStyle).toBe(FALLBACK_PALETTE.labelText);
    expect(fill?.fillStyle).not.toBe(text?.fillStyle);
  });
});
