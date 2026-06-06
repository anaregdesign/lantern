import { useEffect, useState } from "react";

export type ResolvedTheme = "light" | "dark";

/**
 * Tracks the user's OS-level color scheme preference and returns the
 * resolved theme. Re-evaluates when `(prefers-color-scheme: dark)` changes.
 *
 * Server-side rendering is disabled for this app, so we can read
 * `window.matchMedia` directly during the first render. The state initialiser
 * still guards against `typeof window === "undefined"` to be safe during
 * tooling that pre-renders for typing (e.g. typegen).
 */
export function usePreferredTheme(): ResolvedTheme {
  const [theme, setTheme] = useState<ResolvedTheme>(() => readPreferredTheme());

  useEffect(() => {
    if (typeof window === "undefined" || !window.matchMedia) {
      return;
    }
    const mql = window.matchMedia("(prefers-color-scheme: dark)");
    const onChange = (e: MediaQueryListEvent) => {
      setTheme(e.matches ? "dark" : "light");
    };
    mql.addEventListener("change", onChange);
    return () => mql.removeEventListener("change", onChange);
  }, []);

  return theme;
}

function readPreferredTheme(): ResolvedTheme {
  if (typeof window === "undefined" || !window.matchMedia) {
    return "light";
  }
  return window.matchMedia("(prefers-color-scheme: dark)").matches
    ? "dark"
    : "light";
}
