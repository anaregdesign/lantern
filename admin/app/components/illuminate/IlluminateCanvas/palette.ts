/**
 * Theme-aware Sigma palette for the Illuminate canvas (#453).
 *
 * The canvas reads Fluent UI design tokens from the cascade so that
 * vertex labels reach WCAG AA contrast against the canvas background
 * in both light and dark themes. Resolution must happen at the level
 * of the canvas container (not `document.documentElement`) because
 * FluentProvider scopes the tokens to its own host element.
 *
 * Each token has a literal light-theme fallback so:
 *   - Unit tests can construct a palette without a real FluentProvider.
 *   - The first paint before CSS hydrates does not produce empty
 *     `color: ""` strings that Sigma silently ignores.
 */

export interface SigmaPalette {
  /** Initial seed fill — Fluent brand accent. */
  seed: string;
  /** Non-seed expansion-origin fill (darker accent for visual hierarchy). */
  origin: string;
  /**
   * Default node fill. Darkened from the prior `#5b5b5b` to `#3f3f46`
   * so adjacent labels reach WCAG AA contrast against both the canvas
   * background and the node disc.
   */
  baseNode: string;
  /** Edge color. */
  edge: string;
  /**
   * Dimmed node fill applied by the hover-focus reducer (#458) to
   * every node outside the focused subset. Uses 8-char hex so sigma's
   * default node program renders it at ~15% alpha (`0x26 / 0xff ≈
   * 0.15`), which is enough for the focused subset to pop without
   * making the surrounding context vanish.
   */
  dimNode: string;
  /** Dimmed edge color paired with {@link dimNode}; same alpha. */
  dimEdge: string;
  /** Label color resolved from `--colorNeutralForeground1`. */
  labelText: string;
  /** Label font stack resolved from `--fontFamilyBase`. */
  labelFont: string;
  /**
   * #460 hop-distance ramp. Each `hopN` is the canvas fill applied to
   * a node whose minimum-hop distance from any expansion origin is
   * exactly `N`; `hopFar` is everything ≥ `HOP_FAR_THRESHOLD` and
   * `hopUnreachable` covers vertices with no path to any origin (the
   * `Number.POSITIVE_INFINITY` case in `selectGraphView`).
   *
   * Fluent token mapping (per #460 spec):
   *   hop0          → colorBrandForeground1 (origin highlight)
   *   hop1          → colorBrandForeground2 (single-hop ring)
   *   hop2          → colorBrandForeground2Hover (two-hop ring)
   *   hopFar        → desaturated neutral   (everything ≥ 3 hops)
   *   hopUnreachable → low-chroma red       (disconnected; visually distinct)
   */
  hop0: string;
  hop1: string;
  hop2: string;
  hopFar: string;
  hopUnreachable: string;
}

export const FALLBACK_PALETTE: SigmaPalette = {
  seed: "#0078d4",
  origin: "#5c2d91",
  baseNode: "#3f3f46",
  edge: "#bdbdbd",
  // Same hue as baseNode/edge but at ~15% alpha so the unfocused
  // subset still hints at the surrounding structure without competing
  // visually with the focused subset.
  dimNode: "#3f3f4626",
  dimEdge: "#bdbdbd26",
  labelText: "#242424",
  labelFont:
    'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
  // #460 hop ramp. Light-theme literals — these are the swatches the
  // canvas falls back to before FluentProvider hydrates and the test
  // suite asserts against. The ramp moves from the Fluent brand
  // accent (hop 0, origin) outward through brand-foreground tints to
  // a desaturated neutral for ≥3 hops, then to a low-chroma red for
  // unreachable. Distinct enough to read at a glance, similar enough
  // in luminance to avoid clobbering the hover dim's contrast story.
  hop0: "#005a9e", // colorBrandForeground1 (light)
  hop1: "#106ebe", // colorBrandForeground2 (light)
  hop2: "#2b88d8", // colorBrandForeground2Hover (light)
  hopFar: "#8a8886", // colorNeutralForeground3 (light, desaturated)
  hopUnreachable: "#a4262c", // colorPaletteRedForeground1 (light, low chroma)
};

export const LABEL_SIZE = 13;
export const LABEL_WEIGHT = "600";

/**
 * Token reader injected into `resolvePaletteFromTokens`. Receives a
 * CSS custom-property name (with leading `--`) and returns the raw
 * resolved string — typically `getComputedStyle(host).getPropertyValue`
 * in production, or a hand-rolled `Record<string, string>` lookup in
 * unit tests.
 */
export type CssTokenReader = (name: string) => string;

/**
 * Pure-function variant of {@link resolvePalette} that takes a token
 * reader instead of an HTMLElement. Lets the unit suite exercise the
 * full mapping without booting a DOM (bun's default runner runs in
 * Node, so `document` / `getComputedStyle` are unavailable).
 */
export function resolvePaletteFromTokens(reader: CssTokenReader): SigmaPalette {
  const readVar = (name: string, fallback: string): string => {
    const raw = reader(name).trim();
    return raw.length > 0 ? raw : fallback;
  };
  return {
    seed: readVar("--colorBrandBackground", FALLBACK_PALETTE.seed),
    origin: FALLBACK_PALETTE.origin,
    baseNode: FALLBACK_PALETTE.baseNode,
    edge: readVar("--colorNeutralStroke2", FALLBACK_PALETTE.edge),
    // Dim swatches are fixed literals — they need a consistent visual
    // weight across light/dark, which Fluent's neutral stroke ramps
    // don't guarantee at this alpha. Keep them code-side so the
    // focused-subset contrast story doesn't drift with token churn.
    dimNode: FALLBACK_PALETTE.dimNode,
    dimEdge: FALLBACK_PALETTE.dimEdge,
    labelText: readVar("--colorNeutralForeground1", FALLBACK_PALETTE.labelText),
    labelFont: readVar("--fontFamilyBase", FALLBACK_PALETTE.labelFont),
    // #460 hop ramp. Token mapping per spec: hop 0/1/2 trace the
    // Fluent brand foreground/stroke ladder so the warmest stop
    // (origin) reads as "you are here" and the cooler stops indicate
    // distance. `hopFar` flattens to a desaturated neutral so the
    // canvas reads as "out of focus past 2 hops" without falling all
    // the way to the unreachable red.
    //
    // Token rationale: the issue spec suggested `colorBrandStroke1`
    // for hop 2, but in Fluent v9's default brand ramp `BrandStroke1`
    // (Shade80) and `BrandForeground1` (Shade100) collide at the
    // same hex value — hop 0 and hop 2 would render identically.
    // `BrandForeground2Hover` (Shade120) gives us a properly
    // separated third stop in the same brand family.
    hop0: readVar("--colorBrandForeground1", FALLBACK_PALETTE.hop0),
    hop1: readVar("--colorBrandForeground2", FALLBACK_PALETTE.hop1),
    hop2: readVar("--colorBrandForeground2Hover", FALLBACK_PALETTE.hop2),
    hopFar: readVar("--colorNeutralForeground3", FALLBACK_PALETTE.hopFar),
    hopUnreachable: readVar(
      "--colorPaletteRedForeground1",
      FALLBACK_PALETTE.hopUnreachable,
    ),
  };
}

/**
 * Resolve the Sigma palette from the Fluent CSS variables that
 * FluentProvider injects into the cascade. Reads each token from the
 * supplied host element's computed style; falls back to the light-theme
 * literal in `FALLBACK_PALETTE` for any token that is missing or empty.
 *
 * The caller is responsible for deciding when to re-run it — typically
 * on mount and whenever the resolved theme flips.
 */
export function resolvePalette(host: HTMLElement): SigmaPalette {
  const cs = getComputedStyle(host);
  return resolvePaletteFromTokens((name) => cs.getPropertyValue(name));
}
