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
  /** Label color resolved from `--colorNeutralForeground1`. */
  labelText: string;
  /** Label font stack resolved from `--fontFamilyBase`. */
  labelFont: string;
}

export const FALLBACK_PALETTE: SigmaPalette = {
  seed: "#0078d4",
  origin: "#5c2d91",
  baseNode: "#3f3f46",
  edge: "#bdbdbd",
  labelText: "#242424",
  labelFont:
    'system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", Roboto, sans-serif',
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
    labelText: readVar("--colorNeutralForeground1", FALLBACK_PALETTE.labelText),
    labelFont: readVar("--fontFamilyBase", FALLBACK_PALETTE.labelFont),
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
