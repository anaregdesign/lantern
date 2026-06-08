import { describe, expect, test } from "bun:test";

import {
  FALLBACK_PALETTE,
  LABEL_SIZE,
  LABEL_WEIGHT,
  resolvePaletteFromTokens,
} from "./palette";

/**
 * Unit coverage for the theme-aware palette resolution (#453).
 *
 * `resolvePaletteFromTokens` is the pure-function variant so the test
 * does not need a DOM. Production code resolves via `resolvePalette`
 * which composes this same logic with `getComputedStyle(host)`.
 */

function readerFrom(tokens: Record<string, string>) {
  return (name: string) => tokens[name] ?? "";
}

describe("resolvePaletteFromTokens", () => {
  test("returns the fallback palette when no CSS variables are set", () => {
    const palette = resolvePaletteFromTokens(readerFrom({}));
    expect(palette).toEqual(FALLBACK_PALETTE);
  });

  test("reads --colorNeutralForeground1 for label text", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorNeutralForeground1": "#111111" }),
    );
    expect(palette.labelText).toBe("#111111");
  });

  test("reads --colorBrandBackground for seed fill", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorBrandBackground": "#2563eb" }),
    );
    expect(palette.seed).toBe("#2563eb");
  });

  test("reads --colorNeutralStroke2 for edge color", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorNeutralStroke2": "#abcdef" }),
    );
    expect(palette.edge).toBe("#abcdef");
  });

  test("reads --fontFamilyBase for label font", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--fontFamilyBase": "Inter, sans-serif" }),
    );
    expect(palette.labelFont).toBe("Inter, sans-serif");
  });

  test("trims whitespace around the resolved CSS variable", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorNeutralForeground1": "  #0a0a0a  " }),
    );
    expect(palette.labelText).toBe("#0a0a0a");
  });

  test("keeps `baseNode` and `origin` as code-side literals so the contrast story does not drift with Fluent token churn", () => {
    const palette = resolvePaletteFromTokens(readerFrom({}));
    // The two visually-anchored colours are fixed for the lifetime of
    // the contrast plan in #453. If Fluent re-themes, the brand seed
    // colour can move, but these two stay where the contrast checker
    // proved them.
    expect(palette.baseNode).toBe("#3f3f46");
    expect(palette.origin).toBe("#5c2d91");
  });

  test("dim swatches stay at the fallback literals regardless of Fluent CSS variables (#458)", () => {
    // The hover-focus reducer (#458) needs a stable ~15% alpha across
    // both themes; neutral stroke tokens differ enough across themes
    // that we keep the dim swatches hard-coded.
    const palette = resolvePaletteFromTokens(
      readerFrom({
        "--colorNeutralStroke2": "#000000",
        "--colorBrandBackground": "#ff00ff",
      }),
    );
    expect(palette.dimNode).toBe("#3f3f4626");
    expect(palette.dimEdge).toBe("#bdbdbd26");
  });

  test("dim swatches encode alpha 0x26 (~15%) so sigma's WebGL blend renders them at ~15% opacity (#458)", () => {
    // Sigma's `parseColor` reads the trailing two hex digits of a
    // 9-char `#RRGGBBAA` as alpha (`parseInt("26", 16) / 255 \u2248 0.149`).
    // The reducer documentation in IlluminateCanvas relies on this
    // exact mapping; if Fluent ever bumps the desired dim intensity
    // we change it here so the unit + e2e tests catch any drift.
    expect(FALLBACK_PALETTE.dimNode.slice(-2)).toBe("26");
    expect(FALLBACK_PALETTE.dimEdge.slice(-2)).toBe("26");
    expect(parseInt("26", 16) / 255).toBeCloseTo(0.149, 3);
  });

  test("empty CSS variable values fall back to the light-theme literal", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({
        "--colorNeutralForeground1": "",
        "--colorBrandBackground": "   ",
      }),
    );
    expect(palette.labelText).toBe(FALLBACK_PALETTE.labelText);
    expect(palette.seed).toBe(FALLBACK_PALETTE.seed);
  });

  // ── #460 hop ramp ───────────────────────────────────────────────
  // The hop-distance reducer reads `hop0/1/2/Far/Unreachable` off the
  // palette and applies them BEFORE the TTL fade so a node's hue
  // communicates structural distance and its alpha communicates
  // remaining lifetime. The token mapping comes straight from the
  // #460 spec; the test pins it so a Fluent token rename can't
  // silently change the canvas reading.

  test("reads --colorBrandForeground1 for hop 0 (origin)", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorBrandForeground1": "#aabbcc" }),
    );
    expect(palette.hop0).toBe("#aabbcc");
  });

  test("reads --colorBrandForeground2 for hop 1", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorBrandForeground2": "#112233" }),
    );
    expect(palette.hop1).toBe("#112233");
  });

  test("reads --colorBrandForeground2Hover for hop 2", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorBrandForeground2Hover": "#445566" }),
    );
    expect(palette.hop2).toBe("#445566");
  });

  test("reads --colorNeutralForeground3 for hopFar (≥3 hops)", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorNeutralForeground3": "#778899" }),
    );
    expect(palette.hopFar).toBe("#778899");
  });

  test("reads --colorPaletteRedForeground1 for unreachable", () => {
    const palette = resolvePaletteFromTokens(
      readerFrom({ "--colorPaletteRedForeground1": "#992222" }),
    );
    expect(palette.hopUnreachable).toBe("#992222");
  });

  test("hop ramp falls back to the light-theme literals when no tokens are set", () => {
    const palette = resolvePaletteFromTokens(readerFrom({}));
    expect(palette.hop0).toBe(FALLBACK_PALETTE.hop0);
    expect(palette.hop1).toBe(FALLBACK_PALETTE.hop1);
    expect(palette.hop2).toBe(FALLBACK_PALETTE.hop2);
    expect(palette.hopFar).toBe(FALLBACK_PALETTE.hopFar);
    expect(palette.hopUnreachable).toBe(FALLBACK_PALETTE.hopUnreachable);
  });
});

describe("label constants", () => {
  test("LABEL_SIZE is 13", () => {
    expect(LABEL_SIZE).toBe(13);
  });

  test("LABEL_WEIGHT is semibold (600)", () => {
    expect(LABEL_WEIGHT).toBe("600");
  });
});
