/**
 * Theme-aware always-on node-label renderer for the Illuminate canvas
 * (#514).
 *
 * Sigma's built-in `drawDiscNodeLabel` paints the label text with NO
 * background, so a vertex key drawn over a same-luminance node disc or a
 * busy patch of canvas blends into it and is hard to read — exactly the
 * "Key が背景色とかぶって見えにくい" complaint #514 fixes. The hover
 * renderer (`makeDrawNodeHover`) already solved this for the single
 * hovered node by drawing a palette-skinned chip behind the text; this
 * module brings the same contrast guarantee to EVERY node label so keys
 * stay legible without having to hover.
 *
 * {@link makeDrawNodeLabel} returns a drop-in `NodeLabelDrawingFunction`
 * that keeps sigma's text placement (to the right of the disc) but first
 * fills a rectangular chip behind the text from `labelBackground`,
 * outlines it with a 1px `labelStroke`, then draws the label in
 * `labelText`. Because the chip follows the surface token and the text
 * follows the foreground token, the two always contrast in both light and
 * dark themes.
 *
 * Colocated with the canvas (pure 2D-canvas drawing math, not interaction
 * state) and takes the palette by value so the caller can re-create it on
 * a theme flip and re-apply it via `setSetting("defaultDrawNodeLabel", …)`.
 */

import type { NodeLabelDrawingFunction } from "sigma/rendering";

import type { SigmaPalette } from "./palette";

/** Horizontal inner padding (canvas px) between the text and the chip edge. */
const PADDING_X = 4;
/** Vertical inner padding (canvas px) between the text and the chip edge. */
const PADDING_Y = 2;

/**
 * Build a `NodeLabelDrawingFunction` that paints every node's label as a
 * palette-skinned chip from the supplied {@link SigmaPalette}. Reads the
 * live font metrics off sigma's settings (so it tracks the non-hover
 * label sizing) but takes its colours from the palette argument,
 * guaranteeing box/text contrast.
 *
 * A node whose reduced label is empty (the hover-focus reducer clears the
 * label string of every out-of-focus node, #458) draws nothing, so the
 * focused-subset dimming keeps working unchanged.
 */
export function makeDrawNodeLabel(
  palette: SigmaPalette,
): NodeLabelDrawingFunction {
  return (context, data, settings) => {
    if (typeof data.label !== "string" || data.label.length === 0) return;

    const size = settings.labelSize;
    const font = settings.labelFont;
    const weight = settings.labelWeight;
    context.font = `${weight} ${size}px ${font}`;

    const label = data.label;
    const textWidth = context.measureText(label).width;
    // Same anchor sigma's `drawDiscNodeLabel` uses: text sits to the
    // right of the disc, vertically centred on the node.
    const tx = data.x + data.size + 3;
    const ty = data.y + size / 3;

    const boxX = tx - PADDING_X;
    const boxY = data.y - size / 2 - PADDING_Y;
    const boxWidth = textWidth + PADDING_X * 2;
    const boxHeight = size + PADDING_Y * 2;

    // Chip behind the text. Drop any inherited shadow so the 1px stroke
    // is what separates the chip from the canvas (mirrors the hover
    // renderer's contrast story).
    context.fillStyle = palette.labelBackground;
    context.strokeStyle = palette.labelStroke;
    context.lineWidth = 1;
    context.shadowOffsetX = 0;
    context.shadowOffsetY = 0;
    context.shadowBlur = 0;
    context.beginPath();
    context.moveTo(boxX, boxY);
    context.lineTo(boxX + boxWidth, boxY);
    context.lineTo(boxX + boxWidth, boxY + boxHeight);
    context.lineTo(boxX, boxY + boxHeight);
    context.closePath();
    context.fill();
    context.stroke();

    // Label on top of the chip in the foreground token — painted here
    // (not delegated to sigma) so the box/text contrast is proven by this
    // function alone and can never silently diverge.
    context.fillStyle = palette.labelText;
    context.fillText(label, tx, ty);
  };
}
