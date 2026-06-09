/**
 * Edge-weight label formatting + the always-on edge-label renderer for
 * the Illuminate canvas (#500, #514).
 *
 * The canvas renders each edge's accumulated weight as an on-edge label
 * (`renderEdgeLabels`). Weights are additive `float32` decay counters, so
 * they are frequently whole numbers but can carry a fraction. We format
 * them compactly so the label stays legible where it sits between two
 * vertices:
 *
 *   - A whole number renders without a decimal point (`3`, not `3.0`).
 *   - A fractional value renders to a single decimal (`2.5`, `0.3`).
 *
 * Kept as a pure helper (mirroring the folder's `force-layout` /
 * `hop-palette` / `ttl-decay` modules) so it is unit-testable without a
 * DOM or a live sigma instance.
 */

import type { EdgeLabelDrawingFunction } from "sigma/rendering";

import type { SigmaPalette } from "./palette";

export function formatEdgeWeight(weight: number): string {
  if (!Number.isFinite(weight)) {
    return "";
  }
  return Number.isInteger(weight) ? String(weight) : weight.toFixed(1);
}

/** Horizontal inner padding (canvas px) between the text and the chip edge. */
const PADDING_X = 4;
/** Vertical inner padding (canvas px) between the text and the chip edge. */
const PADDING_Y = 2;

/**
 * Build an `EdgeLabelDrawingFunction` that paints every edge's weight as a
 * palette-skinned chip centred on the edge (#514).
 *
 * Sigma's built-in `drawStraightEdgeLabel` draws the weight as bare text
 * with NO background and *skips short edges entirely* (`d < sSize + size`),
 * so on a dense illuminate subgraph many weights either vanished or
 * collided with the edge line and the node discs underneath — the "Weight
 * が背景色とかぶって見えにくい" half of #514. This renderer instead:
 *
 *   - draws an opaque chip (filled `labelBackground`, 1px `labelStroke`)
 *     behind the weight so it always contrasts with whatever sits under
 *     the edge, then the weight text on top in `labelText`; and
 *   - draws EVERY edge's weight (no short-edge skip), rotated to sit along
 *     the edge, so the weight is always present and readable.
 *
 * Takes the palette by value so the caller can re-create it on a theme
 * flip and re-apply it via `setSetting("defaultDrawEdgeLabel", …)`.
 */
export function makeDrawEdgeLabel(
  palette: SigmaPalette,
): EdgeLabelDrawingFunction {
  return (context, data, sourceData, targetData, settings) => {
    if (typeof data.label !== "string" || data.label.length === 0) return;

    const size = settings.edgeLabelSize;
    const font = settings.edgeLabelFont;
    const weight = settings.edgeLabelWeight;
    context.font = `${weight} ${size}px ${font}`;

    const label = data.label;
    const textWidth = context.measureText(label).width;

    // Midpoint of the edge in viewport pixels, and the edge's angle —
    // clamped to ±90° so the weight is never drawn upside-down.
    const cx = (sourceData.x + targetData.x) / 2;
    const cy = (sourceData.y + targetData.y) / 2;
    const dx = targetData.x - sourceData.x;
    const dy = targetData.y - sourceData.y;
    let angle = Math.atan2(dy, dx);
    if (angle < -Math.PI / 2) angle += Math.PI;
    if (angle > Math.PI / 2) angle -= Math.PI;

    const boxWidth = textWidth + PADDING_X * 2;
    const boxHeight = size + PADDING_Y * 2;

    context.save();
    context.translate(cx, cy);
    context.rotate(angle);

    // Opaque chip centred on the midpoint. Drop any inherited shadow so
    // the 1px stroke is what separates the chip from the edge/canvas.
    context.fillStyle = palette.labelBackground;
    context.strokeStyle = palette.labelStroke;
    context.lineWidth = 1;
    context.shadowOffsetX = 0;
    context.shadowOffsetY = 0;
    context.shadowBlur = 0;
    context.beginPath();
    context.moveTo(-boxWidth / 2, -boxHeight / 2);
    context.lineTo(boxWidth / 2, -boxHeight / 2);
    context.lineTo(boxWidth / 2, boxHeight / 2);
    context.lineTo(-boxWidth / 2, boxHeight / 2);
    context.closePath();
    context.fill();
    context.stroke();

    // Weight on top of the chip in the foreground token, horizontally
    // centred and vertically nudged onto the text baseline.
    context.fillStyle = palette.labelText;
    context.fillText(label, -textWidth / 2, size / 3);

    context.restore();
  };
}
