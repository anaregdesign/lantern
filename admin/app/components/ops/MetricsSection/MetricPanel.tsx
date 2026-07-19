import {
  Card,
  MessageBar,
  MessageBarBody,
  Spinner,
} from "@fluentui/react-components";

import type { PanelSpec } from "~/lib/client/usecase/ops/metrics/catalog";
import {
  summariseSeries,
  toChartSeries,
  type ReplicaAliasMap,
} from "~/lib/client/usecase/ops/metrics/selectors";
import type { PanelState } from "~/lib/client/usecase/ops/metrics/state";
import { TimeSeriesChart } from "./TimeSeriesChart";
import styles from "./MetricPanel.module.css";

export interface MetricPanelProps {
  panel: PanelSpec;
  state: PanelState;
  aliases?: ReplicaAliasMap;
}

/**
 * MetricPanel renders one catalog panel. It owns only presentation: the
 * PanelState comes from the metrics reducer and the chart series are
 * derived through the tested selector layer. One panel failing (e.g. a
 * metric the server has not emitted yet) surfaces an inline MessageBar and
 * never affects its siblings.
 */
export function MetricPanel({ panel, state, aliases }: MetricPanelProps) {
  const testId = `ops-metric-${panel.id}`;
  const chart = toChartSeries(panel.title, state.series, aliases);
  const pending = state.status === "idle" || state.status === "loading";

  return (
    <Card className={styles.panel} data-testid={testId}>
      <header className={styles.head}>
        <h4 className={styles.title}>{panel.title}</h4>
        <p className={styles.description}>{panel.description}</p>
      </header>
      {pending && <Spinner size="tiny" label="Loading…" />}
      {state.status === "error" && state.error && (
        <MessageBar intent="error" data-testid={`${testId}-error`}>
          <MessageBarBody>{state.error}</MessageBarBody>
        </MessageBar>
      )}
      {state.status === "ready" &&
        (chart.length === 0 ? (
          <p className={styles.empty} data-testid={`${testId}-empty`}>
            No data in the selected range.
          </p>
        ) : (
          <>
            <TimeSeriesChart
              series={chart}
              unit={panel.unit}
              ariaLabel={`${panel.title} — ${panel.description}`}
            />
            <p className={styles.summary} data-testid={`${testId}-summary`}>
              {summariseSeries(chart, panel.unit)}
            </p>
          </>
        ))}
    </Card>
  );
}
