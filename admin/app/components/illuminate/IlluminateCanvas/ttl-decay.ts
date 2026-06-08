/**
 * TTL decay encoding for the Illuminate canvas (#459).
 *
 * Lantern's defining feature is per-vertex / per-edge TTL: every value
 * expires on its own schedule. The canvas should make that temporal
 * axis visible at a glance so users can spot dying nodes without
 * opening the table or hovering each one in turn. We encode the
 * remaining lifetime as an alpha fade (full opacity → 0.25), and tint
 * nodes that are inside a "cliff" warning window so the user has time
 * to re-extend the TTL before the value disappears.
 *
 * Composition note (locked in repo memory, see #458 and #460):
 *   hop hue (#460) → TTL alpha (#459) → hover dim (#458)
 *
 * The chain is multiplicative — each layer wraps the previous one's
 * output. TTL alpha must be applied BEFORE hover dim, so the hover
 * reducer's swatch swap (low-alpha grey) still wins on out-of-focus
 * nodes regardless of TTL state. When #460 lands it will compute the
 * base hue first, then this module fades it, then hover overrides.
 *
 * Everything in this module is pure so the unit suite can exercise
 * the math without booting React, sigma, or a DOM.
 */

/**
 * Lifetime budget in milliseconds used to map "remaining time" to a
 * `[0, 1]` fraction. We pick a fixed window so the fade rate is
 * predictable across vertices regardless of the original TTL — a
 * vertex written with `ttl=10s` and a vertex written with `ttl=1h`
 * both look full-opacity until they enter the LIFETIME_BUDGET_MS
 * window before expiry, then visibly fade.
 *
 * 10 minutes matches Lantern's typical "live working memory" rhythm:
 * MCP write→recall cycles run on the order of seconds to minutes;
 * anything past 10 minutes is "stable enough to not worry about".
 */
export const LIFETIME_BUDGET_MS = 10 * 60 * 1000;

/**
 * Warning window — vertices inside this distance of expiry get tinted
 * toward red so the user notices in time to re-extend the TTL.
 */
export const WARNING_WITHIN_MS = 60 * 1000;

/**
 * Minimum alpha applied to an about-to-expire vertex (fraction ≈ 0).
 * We don't fade all the way to invisible because the user still needs
 * to see and click the node to renew it. 0.25 keeps the disc visible
 * against any reasonable canvas background while making it obviously
 * weaker than its long-lived neighbours.
 */
export const MIN_ALPHA = 0.25;

/**
 * Compute the remaining-lifetime fraction for an expiration timestamp.
 *
 * Returns:
 *   - `null` when no expiration is set — treat as ∞ (no decay).
 *   - `0` when the value is at or past its expiration — the caller is
 *     expected to drop these from the view entirely
 *     ({@link selectGraphView} filters expired vertices), but we still
 *     return `0` here so render-side fallbacks behave gracefully if a
 *     stale frame is observed mid-tick.
 *   - A value in `(0, 1]` otherwise, linearly scaled against
 *     {@link LIFETIME_BUDGET_MS}: vertices with more remaining time
 *     than the budget cap at `1`.
 *
 * Unparseable timestamps fall back to `null` (treat as ∞) — defensive
 * behaviour: we'd rather render a node at full opacity than drop it
 * over a malformed ISO string.
 */
export function computeTtlFraction(
  expiration: string | undefined,
  nowMs: number,
): number | null {
  if (expiration === undefined || expiration === "") return null;
  const expiresAtMs = Date.parse(expiration);
  if (!Number.isFinite(expiresAtMs)) return null;
  const remainingMs = expiresAtMs - nowMs;
  if (remainingMs <= 0) return 0;
  if (remainingMs >= LIFETIME_BUDGET_MS) return 1;
  return remainingMs / LIFETIME_BUDGET_MS;
}

/**
 * True when the remaining lifetime is inside the warning window.
 * Callers use this to switch from a plain alpha fade to a tinted
 * (red-shifted) swatch.
 */
export function isInWarningWindow(
  expiration: string | undefined,
  nowMs: number,
): boolean {
  if (expiration === undefined || expiration === "") return false;
  const expiresAtMs = Date.parse(expiration);
  if (!Number.isFinite(expiresAtMs)) return false;
  const remainingMs = expiresAtMs - nowMs;
  return remainingMs > 0 && remainingMs <= WARNING_WITHIN_MS;
}

/**
 * Map a remaining-lifetime fraction to an alpha byte for the
 * `#RRGGBBAA` hex8 swatches sigma's default node program understands.
 * `fraction === null` (no expiration) returns `0xff` (full opacity)
 * so the caller doesn't have to special-case "no TTL" at every site.
 *
 * Verified against sigma's color parser at
 * `sigma/dist/colors-fe6de9d2.cjs.dev.js:226-264` — the parser reads
 * the alpha byte at `val.length === 9` and packs it into the WebGL
 * float via `rgbaToFloat`.
 */
export function alphaByteForFraction(fraction: number | null): number {
  if (fraction === null) return 0xff;
  const clamped = Math.max(0, Math.min(1, fraction));
  const alpha = MIN_ALPHA + (1 - MIN_ALPHA) * clamped;
  return Math.max(0, Math.min(255, Math.round(alpha * 255)));
}

/**
 * Apply the TTL fade to a base color. Returns the same color when
 * `fraction === null` (no expiration), otherwise returns the color
 * with an alpha byte appended. Accepts both `#RGB` and `#RRGGBB`
 * shorthand; any other input (incl. an already-hex8 string) is
 * returned unchanged so this stays safe to call on cached results.
 */
export function applyTtlFade(
  baseColor: string,
  fraction: number | null,
): string {
  if (fraction === null) return baseColor;
  const rgb = normaliseHexToRgb(baseColor);
  if (rgb === null) return baseColor;
  const alpha = alphaByteForFraction(fraction);
  return `#${rgb}${alpha.toString(16).padStart(2, "0")}`;
}

/**
 * Tint a base color toward red proportionally to the warning urgency
 * — `urgency=1` is "1 ms remaining" (full red), `urgency=0` is "at
 * the warning threshold" (unchanged). Used to give warning-window
 * vertices a distinct visual treatment beyond plain alpha fade.
 *
 * Warning colour: `#d13438` (Fluent's standard error red — same hue
 * the ExpirationCell uses for its expiring-soon chip background).
 */
export function applyWarningTint(baseColor: string, urgency: number): string {
  const rgb = normaliseHexToRgb(baseColor);
  if (rgb === null) return baseColor;
  const u = Math.max(0, Math.min(1, urgency));
  const baseR = parseInt(rgb.slice(0, 2), 16);
  const baseG = parseInt(rgb.slice(2, 4), 16);
  const baseB = parseInt(rgb.slice(4, 6), 16);
  const warnR = 0xd1;
  const warnG = 0x34;
  const warnB = 0x38;
  const r = Math.round(baseR + (warnR - baseR) * u);
  const g = Math.round(baseG + (warnG - baseG) * u);
  const b = Math.round(baseB + (warnB - baseB) * u);
  return `#${hex2(r)}${hex2(g)}${hex2(b)}`;
}

/**
 * Compute the "urgency" of a warning-window vertex on a `[0, 1]`
 * scale: `0` at the warning threshold (60 s out), `1` at expiry.
 * Returns `0` outside the window so callers can chain unconditionally.
 */
export function warningUrgency(
  expiration: string | undefined,
  nowMs: number,
): number {
  if (expiration === undefined || expiration === "") return 0;
  const expiresAtMs = Date.parse(expiration);
  if (!Number.isFinite(expiresAtMs)) return 0;
  const remainingMs = expiresAtMs - nowMs;
  if (remainingMs <= 0) return 1;
  if (remainingMs >= WARNING_WITHIN_MS) return 0;
  return 1 - remainingMs / WARNING_WITHIN_MS;
}

function hex2(n: number): string {
  return Math.max(0, Math.min(255, n)).toString(16).padStart(2, "0");
}

/**
 * Normalise `#RGB` / `#RRGGBB` to lowercase `RRGGBB` (no leading `#`).
 * Returns `null` for any other shape — including `#RRGGBBAA`, which
 * we want to leave alone so callers don't double-apply alpha.
 */
function normaliseHexToRgb(color: string): string | null {
  if (color.length === 7 && color.startsWith("#")) {
    const body = color.slice(1).toLowerCase();
    if (/^[0-9a-f]{6}$/.test(body)) return body;
    return null;
  }
  if (color.length === 4 && color.startsWith("#")) {
    const r = color[1];
    const g = color[2];
    const b = color[3];
    if (r === undefined || g === undefined || b === undefined) return null;
    if (
      !/^[0-9a-f]$/i.test(r) ||
      !/^[0-9a-f]$/i.test(g) ||
      !/^[0-9a-f]$/i.test(b)
    ) {
      return null;
    }
    return `${r}${r}${g}${g}${b}${b}`.toLowerCase();
  }
  return null;
}
