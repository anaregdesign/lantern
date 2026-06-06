import { Card } from "@fluentui/react-components";
import {
  ArrowRight20Regular,
  DatabaseSearch24Regular,
  LightbulbFilament24Regular,
  LinkMultiple24Regular,
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

const FEATURES: readonly Feature[] = [
  {
    to: "/vertices",
    icon: <DatabaseSearch24Regular />,
    title: "Vertices",
    copy: "Scan vertices by key prefix and inspect their typed values and TTLs.",
  },
  {
    to: "/edges",
    icon: <LinkMultiple24Regular />,
    title: "Edges",
    copy: "Filter edges by tail and head prefix to follow relationships and weights.",
  },
  {
    to: "/illuminate",
    icon: <LightbulbFilament24Regular />,
    title: "Illuminate",
    copy: "Walk the graph visually with Sigma.js — click any vertex to reseed the neighborhood and switch between SPT and MST views.",
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
