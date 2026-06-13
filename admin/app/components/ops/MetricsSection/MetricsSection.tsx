import { useMemo } from "react";
import {
  MessageBar,
  MessageBarBody,
  Tab,
  TabList,
} from "@fluentui/react-components";

import {
  METRIC_PANELS,
  PANEL_GROUPS,
  type PanelGroup,
} from "~/lib/client/usecase/ops/metrics/catalog";
import {
  AGG_MODE_OPTIONS,
  type AggMode,
} from "~/lib/client/usecase/ops/metrics/mode";
import {
  RANGE_OPTIONS,
  type RangeKey,
} from "~/lib/client/usecase/ops/metrics/range";
import {
  buildReplicaAliases,
  type ReplicaAliasMap,
} from "~/lib/client/usecase/ops/metrics/selectors";
import type { PanelState } from "~/lib/client/usecase/ops/metrics/state";
import { useMetrics } from "~/lib/client/usecase/ops/metrics/use-metrics";
import { usePrometheusUrl } from "~/lib/client/usecase/ops/metrics/use-prometheus-url";
import { MetricPanel } from "./MetricPanel";
import { PrometheusSwitcher } from "./PrometheusSwitcher";
import { ReplicaKey } from "./ReplicaKey";
import styles from "./MetricsSection.module.css";

export interface MetricsSectionProps {
  /** Poll interval inherited from the Ops toolbar (0 disables polling). */
  pollMs: number;
  /** Bumped by the Ops Refresh button to force an immediate fetch round. */
  refreshNonce: number;
}

/**
 * MetricsSection is the Prometheus time-series half of the Ops page. It sits
 * below the point-in-time status cards and renders the curated panel
 * catalog grouped by concern. The Prometheus endpoint and the time range
 * are ops-local concerns owned here (not in the global header), and polling
 * is driven by the same cadence/refresh signal as the status cards.
 */
export function MetricsSection({ pollMs, refreshNonce }: MetricsSectionProps) {
  const prometheus = usePrometheusUrl();
  const { state, range, setRange, aggMode, setAggMode } = useMetrics({
    prometheusUrl: prometheus.prometheusUrl,
    pollMs,
    refreshNonce,
  });

  // One replica identity map for the whole section, recomputed only when the
  // panel series change. Threading a single map down keeps each replica's
  // alias and colour consistent across every panel.
  const aliases = useMemo(
    () => buildReplicaAliases(state.panels),
    [state.panels],
  );

  const panelStates = Object.values(state.panels);
  const allError =
    panelStates.length > 0 &&
    panelStates.every((panel) => panel.status === "error");

  return (
    <section className={styles.section} data-testid="ops-metrics-section">
      <header className={styles.header}>
        <div>
          <h2 className={styles.title}>Metrics</h2>
          <p className={styles.lead}>
            Time-series from Prometheus. Point-in-time counts above are the live
            snapshot.
          </p>
        </div>
        <div className={styles.toolbar}>
          <TabList
            selectedValue={aggMode}
            onTabSelect={(_, data) => setAggMode(data.value as AggMode)}
            size="small"
            data-testid="ops-metrics-agg-mode"
          >
            {AGG_MODE_OPTIONS.map((option) => (
              <Tab key={option.key} value={option.key}>
                {option.label}
              </Tab>
            ))}
          </TabList>
          <TabList
            selectedValue={range}
            onTabSelect={(_, data) => setRange(data.value as RangeKey)}
            size="small"
            data-testid="ops-metrics-range"
          >
            {RANGE_OPTIONS.map((option) => (
              <Tab key={option.key} value={option.key}>
                {option.label}
              </Tab>
            ))}
          </TabList>
          <PrometheusSwitcher prometheus={prometheus} />
        </div>
      </header>

      {allError && (
        <MessageBar intent="warning" data-testid="ops-metrics-degraded">
          <MessageBarBody>
            All metric panels failed to load. Verify the Prometheus endpoint (
            <code>{prometheus.prometheusUrl}</code>) is reachable — on a local{" "}
            <code>vite preview</code> build there is no Prometheus behind{" "}
            <code>/api/prom</code>.
          </MessageBarBody>
        </MessageBar>
      )}

      <ReplicaKey aliases={aliases} />

      {PANEL_GROUPS.map((group) => {
        const panels = METRIC_PANELS.filter(
          (panel) => panel.group === group.id,
        );
        if (panels.length === 0) {
          return null;
        }
        return (
          <MetricGroup
            key={group.id}
            label={group.label}
            groupId={group.id}
            panels={panels}
            state={state.panels}
            aliases={aliases}
          />
        );
      })}
    </section>
  );
}

interface MetricGroupProps {
  label: string;
  groupId: PanelGroup;
  panels: typeof METRIC_PANELS;
  state: Record<string, PanelState>;
  aliases: ReplicaAliasMap;
}

function MetricGroup({
  label,
  groupId,
  panels,
  state,
  aliases,
}: MetricGroupProps) {
  return (
    <div className={styles.group} data-testid={`ops-metrics-group-${groupId}`}>
      <h3 className={styles.groupTitle}>{label}</h3>
      <div className={styles.grid}>
        {panels.map((panel) => {
          const panelState = state[panel.id];
          if (!panelState) {
            return null;
          }
          return (
            <MetricPanel
              key={panel.id}
              panel={panel}
              state={panelState}
              aliases={aliases}
            />
          );
        })}
      </div>
    </div>
  );
}
