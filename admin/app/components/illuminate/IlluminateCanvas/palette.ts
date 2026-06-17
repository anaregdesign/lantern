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
  /**
   * #484 hover-label chip background, resolved from
   * `--colorNeutralBackground1`. The custom hover renderer
   * (`makeDrawNodeHover`) fills the hovered node's label box with this
   * so the box and the {@link labelText} on top of it always contrast,
   * fixing the white-on-white collision sigma's default hover renderer
   * produced in dark theme.
   */
  labelBackground: string;
  /**
   * #484 hover-label chip border, resolved from `--colorNeutralStroke1`.
   * Drawn as a 1px stroke around the hover box so the chip reads as a
   * distinct surface even when its background is close to the canvas
   * background.
   */
  labelStroke: string;
  /** Label font stack resolved from `--fontFamilyBase`. */
  labelFont: string;
  /**
   * #460 hop-distance ramp. Each `hopN` is the canvas fill applied to
   * a node whose minimum-hop distance from any expansion origin is
   * exactly `N`; `hopFar` is everything ≥ `HOP_FAR_THRESHOLD` and
   * `hopUnreachable` covers vertices with no path to any origin (the
   * `Number.POSITIVE_INFINITY` case from `computeHopDistances`).
   *
   * #500 made this a deliberate red→blue diverging colormap (code-side
   * literals, not Fluent brand tokens) so the tiers are obviously
   * distinct at a glance:
   *   hop0          → vivid red    (origin / pinned anchor — "you are here")
   *   hop1          → violet       (single-hop ring; midpoint of the sweep)
   *   hop2          → azure blue   (two-hop ring)
   *   hopFar        → deep blue    (everything ≥ 3 hops — coldest/furthest)
   *   hopUnreachable → neutral grey (disconnected; off the warm→cool ramp)
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
  // #484 hover chip. Light-theme literals for `--colorNeutralBackground1`
  // (#ffffff) and `--colorNeutralStroke1` (#d1d1d1); paired with the
  // `#242424` labelText they give a legible chip before FluentProvider
  // hydrates and in the unit suite.
  labelBackground: "#ffffff",
  labelStroke: "#d1d1d1",
  labelFont:
    'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
  // #500 red→blue diverging hop ramp. Deliberate code-side literals (not
  // Fluent brand tokens): the original #460 ramp pulled hop 0/1/2 from the
  // single-hue brand foreground ladder, which rendered as three nearly
  // identical blues and read as "no colour coding" at all. This sweep runs
  // warm→cool so structural distance from the origin is obvious at a
  // glance — vivid red at the pinned anchor, through a violet midpoint, to
  // azure then deep blue as you move outward; disconnected nodes sit off
  // the ramp in neutral grey. Hues are far enough apart to distinguish the
  // tiers, dark enough to keep the hover-dim contrast story intact.
  hop0: "#d13438", // origin — vivid red ("you are here")
  hop1: "#8764b8", // 1 hop — violet midpoint
  hop2: "#0078d4", // 2 hops — azure blue
  hopFar: "#004e8c", // ≥3 hops — deep/cold blue (furthest)
  hopUnreachable: "#605e5c", // disconnected — neutral grey, off the ramp
};

export const LABEL_SIZE = 13;
export const LABEL_WEIGHT = "600";

/**
 * #500 edge-weight label sizing. Slightly smaller than {@link LABEL_SIZE}
 * so an on-edge weight reads as secondary to the vertex labels it sits
 * between.
 */
export const EDGE_LABEL_SIZE = 11;
export const EDGE_LABEL_WEIGHT = "600";

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
    // #484 hover chip. Follow the Fluent surface tokens so a theme flip
    // re-skins the hover box alongside the label text; the canvas
    // re-applies them via `setSetting("defaultDrawNodeHover", …)` in the
    // palette effect.
    labelBackground: readVar(
      "--colorNeutralBackground1",
      FALLBACK_PALETTE.labelBackground,
    ),
    labelStroke: readVar("--colorNeutralStroke1", FALLBACK_PALETTE.labelStroke),
    labelFont: readVar("--fontFamilyBase", FALLBACK_PALETTE.labelFont),
    // #500: the hop ramp is a controlled red→blue diverging colormap, so
    // every stop is a code-side literal (like the dim swatches). Pulling
    // each tier from an independent Fluent token — or worse, three shades
    // of the single-hue brand ladder, as the original #460 ramp did —
    // can't guarantee the "origin/1hop/2hop are obviously different"
    // property; it flattened to three near-identical blues. Fixed literals
    // keep the sweep extreme and stable across themes.
    hop0: FALLBACK_PALETTE.hop0,
    hop1: FALLBACK_PALETTE.hop1,
    hop2: FALLBACK_PALETTE.hop2,
    hopFar: FALLBACK_PALETTE.hopFar,
    hopUnreachable: FALLBACK_PALETTE.hopUnreachable,
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
