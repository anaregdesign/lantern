import { Card } from "@fluentui/react-components";
import {
  ArrowRight20Regular,
  Code24Regular,
  DatabaseSearch24Regular,
  PulseSquare24Regular,
} from "@fluentui/react-icons";
import type { ReactElement } from "react";
import { Link } from "react-router";
import styles from "./LandingPage.module.css";

interface Feature {
  to: string;
  icon: ReactElement;
  title: string;
  copy: string;
}

// The Home tiles mirror the three working surfaces in the AppShell nav
// (#655): Data / CLI / Ops. Vertices, Edges, and content search were
// folded into the single Data surface (#650); the standalone Illuminate
// explorer moved into the CLI workspace (#651).
const FEATURES: readonly Feature[] = [
  {
    to: "/vertices",
    icon: <DatabaseSearch24Regular />,
    title: "Data",
    copy: "Browse vertices and edges by key prefix, search vertices by content, and inspect typed values, TTLs, and relationship weights.",
  },
  {
    to: "/cli",
    icon: <Code24Regular />,
    title: "CLI",
    copy: "Type-and-run REPL — same grammar as `lantern repl` (#411). Walk the graph on the canvas and do quick CRUD without leaving the SPA.",
  },
  {
    to: "/ops",
    icon: <PulseSquare24Regular />,
    title: "Ops",
    copy: "Inspect server status and replication health. Triage live before paging.",
  },
];

export function LandingPage() {
  return (
    <div>
      <section className={styles.hero}>
        <h1 className={styles.heroTitle}>Lantern Admin</h1>
        <p className={styles.heroLead}>
          A browser-based control surface for the Lantern in-memory graph KVS.
          Browse data, explore neighborhoods, and watch replication — all
          against a gateway you control.
        </p>
      </section>
      <section className={styles.cards} aria-label="Sections">
        {FEATURES.map((feature) => (
          <Card key={feature.to} className={styles.card}>
            <div className={styles.cardTitle}>
              {feature.icon}
              <span>{feature.title}</span>
            </div>
            <p className={styles.cardCopy}>{feature.copy}</p>
            <Link to={feature.to} className={styles.cardLink}>
              Open {feature.title} <ArrowRight20Regular />
            </Link>
          </Card>
        ))}
      </section>
    </div>
  );
}
