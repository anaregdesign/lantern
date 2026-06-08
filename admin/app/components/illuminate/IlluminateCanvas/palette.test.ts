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
});

describe("label constants", () => {
  test("LABEL_SIZE is 13", () => {
    expect(LABEL_SIZE).toBe(13);
  });

  test("LABEL_WEIGHT is semibold (600)", () => {
    expect(LABEL_WEIGHT).toBe("600");
  });
});
