import type { ReactNode } from "react";
import { Link, useLocation } from "react-router";
import { ConnectionSwitcher } from "~/components/shared/ConnectionSwitcher/ConnectionSwitcher";
import styles from "./AppShell.module.css";

export interface AppShellProps {
  children: ReactNode;
}

interface NavEntry {
  to: string;
  label: string;
  /**
   * Optional extra active-state predicate. By default an entry is active
   * only for paths under its own `to`; an entry that fronts several
   * sibling routes (e.g. the Data surface spanning /vertices and /edges)
   * uses this to stay active — and announce `aria-current` — across all
   * of them. See {@link isEntryActive}.
   */
  match?: (pathname: string) => boolean;
}

const NAV: readonly NavEntry[] = [
  { to: "/", label: "Home" },
  // Vertices, Edges, and content Search collapsed into one Data surface
  // (#650): a single entry that lands on the vertex browse screen, with
  // the Vertices / Edges sub-nav switching halves and content search
  // folded in as a find mode. Active for every /vertices and /edges path.
  {
    to: "/vertices",
    label: "Data",
    match: (p) => p.startsWith("/vertices") || p.startsWith("/edges"),
  },
  // CLI is the power-user shortcut to the same RPC surface the other
  // routes wrap, and since #651 it also hosts the interactive graph walk
  // the retired Illuminate surface used to own. It sits before the
  // operator-facing Ops page (#439, #431).
  { to: "/cli", label: "CLI" },
  { to: "/ops", label: "Ops" },
];

/**
 * Active-state for a nav entry. Mirrors NavLink's default matching
 * (exact for "/", prefix-with-boundary otherwise) but also honours the
 * entry's `match` predicate. Computing this ourselves — rather than
 * leaning on NavLink — lets the Data surface set `aria-current` on every
 * /vertices AND /edges path: NavLink derives `aria-current` solely from
 * its own `to`, so an /edges visit would render styled-active yet
 * announce no current page to assistive tech (#655).
 */
function isEntryActive(entry: NavEntry, pathname: string): boolean {
  if (entry.match?.(pathname)) {
    return true;
  }
  if (entry.to === "/") {
    return pathname === "/";
  }
  return pathname === entry.to || pathname.startsWith(`${entry.to}/`);
}

export function AppShell({ children }: AppShellProps) {
  // The /cli route is a full-bleed shell terminal + canvas workspace, so
  // it opts out of the centered max-width column the other routes use and
  // spans the screen width instead (#512).
  const { pathname } = useLocation();
  const mainClassName =
    pathname === "/cli" ? `${styles.main} ${styles.mainFull}` : styles.main;

  return (
    <div className={styles.shell}>
      <header className={styles.header}>
        <span className={styles.brand}>Lantern Admin</span>
        <nav className={styles.nav} aria-label="Primary">
          {NAV.map((entry) => {
            const active = isEntryActive(entry, pathname);
            return (
              <Link
                key={entry.to}
                to={entry.to}
                aria-current={active ? "page" : undefined}
                className={
                  active
                    ? `${styles.navLink} ${styles.navLinkActive}`
                    : styles.navLink
                }
              >
                {entry.label}
              </Link>
            );
          })}
        </nav>
        <div className={styles.connection}>
          <ConnectionSwitcher />
        </div>
      </header>
      <main className={mainClassName}>{children}</main>
    </div>
  );
}
