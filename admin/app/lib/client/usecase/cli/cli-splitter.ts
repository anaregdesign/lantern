/**
 * Pure helpers for the /cli page L/R splitter (#465).
 *
 * The DOM-coupled React hook lives in {@link ./use-cli-splitter}; everything
 * here is side-effect free so it can be unit-tested under `bun:test` without
 * a DOM. The split is intentional: clamping and key handling are easy to get
 * subtly wrong, so they should be covered by tests, while the
 * PointerEvent/ResizeObserver wiring is best left to Playwright.
 */

/** localStorage key used to persist the canvas's share of the row. */
export const SPLITTER_STORAGE_KEY = "cli.splitRatio";

/** Default canvas share (60% canvas / 40% right column). */
export const SPLITTER_DEFAULT_RATIO = 0.6;

/** Neither pane may collapse below this many CSS pixels. */
export const SPLITTER_MIN_PANE_PX = 360;

/** Below this viewport width the split layout is disabled. */
export const SPLITTER_BREAKPOINT_PX = 1024;

/** Per-arrow keyboard nudge, expressed as a fraction of the row. */
export const SPLITTER_NUDGE = 0.02;

/** Shift+arrow jump size. */
export const SPLITTER_JUMP = 0.1;

/**
 * Clamp a desired canvas fraction so that neither pane shrinks below
 * {@link SPLITTER_MIN_PANE_PX}. When the container itself is too narrow to
 * honour the min size on both sides, falls back to a soft [0.1, 0.9] clamp so
 * the splitter remains usable — narrow viewports are not the primary target
 * anyway.
 */
export function clampRatio(
  desired: number,
  containerWidth: number,
  minPanePx: number = SPLITTER_MIN_PANE_PX,
): number {
  if (!Number.isFinite(desired)) {
    return SPLITTER_DEFAULT_RATIO;
  }
  if (containerWidth <= 0 || minPanePx * 2 >= containerWidth) {
    return clampInRange(desired, 0.1, 0.9);
  }
  const minRatio = minPanePx / containerWidth;
  const maxRatio = 1 - minPanePx / containerWidth;
  return clampInRange(desired, minRatio, maxRatio);
}

/**
 * Parse a value previously written by {@link saveRatio}. Returns null if the
 * value is missing, malformed, or outside the open interval (0, 1). The caller
 * is expected to fall back to {@link SPLITTER_DEFAULT_RATIO} in that case.
 */
export function parseStoredRatio(raw: string | null): number | null {
  if (raw === null) return null;
  const n = Number.parseFloat(raw);
  if (!Number.isFinite(n)) return null;
  if (n <= 0 || n >= 1) return null;
  return n;
}

/** Inverse of {@link parseStoredRatio}; serialised to four decimals. */
export function formatStoredRatio(ratio: number): string {
  return ratio.toFixed(4);
}

export interface NudgeOptions {
  shiftKey: boolean;
}

/**
 * Map a keydown event to the desired next ratio, or null if the key is not a
 * splitter control. The caller is responsible for clamping the result; the
 * `Home`/`End` cases intentionally return values outside [0, 1] so the clamp
 * snaps to the actual min/max for the current container width.
 */
export function nudgeRatio(
  current: number,
  key: string,
  opts: NudgeOptions,
): number | null {
  const step = opts.shiftKey ? SPLITTER_JUMP : SPLITTER_NUDGE;
  switch (key) {
    case "ArrowLeft":
      return current - step;
    case "ArrowRight":
      return current + step;
    case "Home":
      return 0;
    case "End":
      return 1;
    case "Enter":
      return SPLITTER_DEFAULT_RATIO;
    default:
      return null;
  }
}

function clampInRange(value: number, lo: number, hi: number): number {
  if (lo > hi) return (lo + hi) / 2;
  if (value < lo) return lo;
  if (value > hi) return hi;
  return value;
}
