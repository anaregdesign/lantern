import type { ReplicaAliasMap } from "~/lib/client/usecase/ops/metrics/selectors";

import chartStyles from "./TimeSeriesChart.module.css";
import styles from "./ReplicaKey.module.css";

// Mirror of the categorical hue count in TimeSeriesChart.module.css
// (.color0…N). The swatch reuses those exact classes so a replica's key
// colour is identical to its line colour in every panel.
const COLOR_SLOTS = 8;

export interface ReplicaKeyProps {
  aliases: ReplicaAliasMap;
}

/**
 * ReplicaKey is the always-visible map from each short replica alias
 * (`r0`, `r1`, …) to its full Prometheus `instance` (`IP:port`), with the
 * replica's stable colour swatch. Per-replica panels prefix series with the
 * short alias only — to keep legends legible — so this strip is the single
 * at-a-glance place the operator reads the full identity, complementing the
 * per-series hover `<title>` and the accessible `<desc>`.
 *
 * It renders only when replica aliases exist, i.e. in per-replica mode where
 * panel series carry an `instance` label. In cluster-sum mode the series have
 * no `instance`, the alias map is empty, and the strip is omitted.
 */
export function ReplicaKey({ aliases }: ReplicaKeyProps) {
  const replicas = Object.values(aliases).sort((a, b) =>
    a.alias.localeCompare(b.alias),
  );
  if (replicas.length === 0) {
    return null;
  }
  return (
    <ul
      className={styles.key}
      data-testid="ops-metrics-replica-key"
      aria-label="Replica colour key"
    >
      <li className={styles.caption} aria-hidden="true">
        Replicas
      </li>
      {replicas.map((replica) => (
        <li
          key={replica.instance}
          className={styles.item}
          title={replica.instance}
        >
          <svg
            className={chartStyles.swatch}
            viewBox="0 0 24 8"
            aria-hidden="true"
          >
            <line
              className={`${chartStyles.line} ${colorClass(replica.colorSlot)}`}
              x1="0"
              y1="4"
              x2="24"
              y2="4"
            />
          </svg>
          <span className={styles.alias}>{replica.alias}</span>
          <span className={styles.separator} aria-hidden="true">
            =
          </span>
          <span className={styles.instance}>{replica.instance}</span>
        </li>
      ))}
    </ul>
  );
}

function colorClass(colorSlot: number): string {
  const slot = ((colorSlot % COLOR_SLOTS) + COLOR_SLOTS) % COLOR_SLOTS;
  return chartStyles[`color${slot}`] ?? "";
}
