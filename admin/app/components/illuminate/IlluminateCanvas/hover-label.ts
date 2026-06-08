/**
 * Theme-aware hover-label renderer for the Illuminate canvas (#484).
 *
 * Sigma's built-in `defaultDrawNodeHover` paints a hard-coded near-white
 * (`#FFF`) rounded box behind the hovered node's label and then draws the
 * label text in `settings.labelColor`. The canvas wires `labelColor` to
 * the Fluent `--colorNeutralForeground1` token, which is *also* near-white
 * in dark theme — so the hovered label rendered as white-on-white and was
 * unreadable exactly when the user needed it most.
 *
 * {@link makeDrawNodeHover} returns a drop-in `NodeHoverDrawingFunction`
 * that keeps sigma's box geometry (a rounded rectangle fused to the node
 * disc) but skins it from the palette: the box is filled with
 * `labelBackground` (`--colorNeutralBackground1`) and outlined with a 1px
 * `labelStroke` (`--colorNeutralStroke1`) so it reads as a chip, then the
 * label is drawn in `labelText` (`--colorNeutralForeground1`). Because the
 * box now follows the surface token and the text follows the foreground
 * token, the two always contrast in both light and dark themes.
 *
 * The function is colocated with the canvas (it is pure 2D-canvas drawing
 * math, not interaction state) and takes the palette by value so the
 * caller can re-create it whenever the theme flips and re-apply it via
 * `setSetting("defaultDrawNodeHover", …)`.
 */

import type { NodeHoverDrawingFunction } from "sigma/rendering";

import type { SigmaPalette } from "./palette";

/**
 * Inner padding (canvas px) between the label text and the chip edge.
 * Mirrors sigma's default `PADDING = 2` so the hover box keeps the same
 * proportions the rest of the canvas was tuned against.
 */
const PADDING = 2;

/**
 * Build a `NodeHoverDrawingFunction` that paints the hovered node's label
 * chip from the supplied {@link SigmaPalette}. The returned function reads
 * `labelSize` / `labelFont` / `labelWeight` off sigma's live settings (so
 * it tracks the same font metrics as the non-hover labels) but takes its
 * colours from the palette argument, guaranteeing box/text contrast.
 */
export function makeDrawNodeHover(
  palette: SigmaPalette,
): NodeHoverDrawingFunction {
  return (context, data, settings) => {
    const size = settings.labelSize;
    const font = settings.labelFont;
    const weight = settings.labelWeight;
    context.font = `${weight} ${size}px ${font}`;

    // Skin the chip from the theme tokens and drop sigma's default drop
    // shadow — the 1px stroke is what now separates the chip from the
    // canvas, and a shadow would only muddy the contrast we just fixed.
    context.fillStyle = palette.labelBackground;
    context.strokeStyle = palette.labelStroke;
    context.lineWidth = 1;
    context.shadowOffsetX = 0;
    context.shadowOffsetY = 0;
    context.shadowBlur = 0;

    if (typeof data.label === "string") {
      // Same rounded-rectangle-fused-to-disc geometry as sigma's
      // `drawDiscNodeHover`, so the chip hugs the node identically; only
      // the fill/stroke styling changes.
      const textWidth = context.measureText(data.label).width;
      const boxWidth = Math.round(textWidth + 5);
      const boxHeight = Math.round(size + 2 * PADDING);
      const radius = Math.max(data.size, size / 2) + PADDING;
      const angleRadian = Math.asin(boxHeight / 2 / radius);
      const xDeltaCoord = Math.sqrt(
        Math.abs(Math.pow(radius, 2) - Math.pow(boxHeight / 2, 2)),
      );

      context.beginPath();
      context.moveTo(data.x + xDeltaCoord, data.y + boxHeight / 2);
      context.lineTo(data.x + radius + boxWidth, data.y + boxHeight / 2);
      context.lineTo(data.x + radius + boxWidth, data.y - boxHeight / 2);
      context.lineTo(data.x + xDeltaCoord, data.y - boxHeight / 2);
      context.arc(data.x, data.y, radius, angleRadian, -angleRadian);
      context.closePath();
      context.fill();
      context.stroke();
    } else {
      // No label: sigma falls back to a plain disc halo. Keep that, just
      // skinned from the palette.
      context.beginPath();
      context.arc(data.x, data.y, data.size + PADDING, 0, Math.PI * 2);
      context.closePath();
      context.fill();
      context.stroke();
    }

    // Draw the label on top of the chip in the foreground token. We paint
    // it here (rather than delegating to sigma's `drawDiscNodeLabel`) so
    // the renderer is self-contained and the box/text contrast is proven
    // by this function alone — the text colour can never silently diverge
    // from the box we just filled.
    if (typeof data.label === "string" && data.label.length > 0) {
      context.fillStyle = palette.labelText;
      context.fillText(data.label, data.x + data.size + 3, data.y + size / 3);
    }
  };
}
