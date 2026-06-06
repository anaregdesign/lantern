import type { ReactNode } from "react";
import { NavLink } from "react-router";
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
  { to: "/browse", label: "Browse" },
  { to: "/illuminate", label: "Illuminate" },
  { to: "/ops", label: "Ops" },
];

export function AppShell({ children }: AppShellProps) {
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
      <main className={styles.main}>{children}</main>
    </div>
  );
}
