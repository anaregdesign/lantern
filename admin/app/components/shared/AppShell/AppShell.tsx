import type { ReactNode } from "react";
import { NavLink, useLocation } from "react-router";
import { ConnectionSwitcher } from "~/components/shared/ConnectionSwitcher/ConnectionSwitcher";
import styles from "./AppShell.module.css";

export interface AppShellProps {
  children: ReactNode;
}

interface NavEntry {
  to: string;
  label: string;
}

const NAV: readonly NavEntry[] = [
  { to: "/", label: "Home" },
  { to: "/vertices", label: "Vertices" },
  { to: "/edges", label: "Edges" },
  { to: "/illuminate", label: "Illuminate" },
  // CLI is the power-user shortcut to the same RPC surface the other
  // routes wrap; place it next to Illuminate (the other "explore"
  // surface) and before the operator-facing Ops page (#439, #431).
  { to: "/cli", label: "CLI" },
  { to: "/ops", label: "Ops" },
];

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
          {NAV.map((entry) => (
            <NavLink
              key={entry.to}
              to={entry.to}
              end={entry.to === "/"}
              className={({ isActive }) =>
                isActive
                  ? `${styles.navLink} ${styles.navLinkActive}`
                  : styles.navLink
              }
            >
              {entry.label}
            </NavLink>
          ))}
        </nav>
        <div className={styles.connection}>
          <ConnectionSwitcher />
        </div>
      </header>
      <main className={mainClassName}>{children}</main>
    </div>
  );
}
