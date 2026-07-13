import {
  Badge,
  Button,
  Card,
  MessageBar,
  MessageBarBody,
  Spinner,
  Switch,
} from "@fluentui/react-components";
import { ArrowClockwise20Regular } from "@fluentui/react-icons";
import { useCallback, useState } from "react";

import {
  peerStatePillIntent,
  replicationCardSummary,
  serverCardSummary,
  formatStaleness,
  formatCount,
} from "~/lib/client/usecase/ops/selectors";
import { DEFAULT_POLL_MS } from "~/lib/client/usecase/ops/state";
import { useOps } from "~/lib/client/usecase/ops/use-ops";
import { MetricsSection } from "../MetricsSection/MetricsSection";
import { SearchStatusCard } from "../SearchStatusCard/SearchStatusCard";
import styles from "./OpsPage.module.css";

/**
 * OpsPage is the single-route dashboard #322 ships: a server-status
 * card (S1) + a replication card (S2) polled in parallel. The S3
 * grpc-health-v1 card promised by the original spec is deferred —
 * Connect-Web cannot call the gRPC-Health-v1 surface without a
 * dedicated codegen pass, and GetServerStatus already covers the
 * "is the server alive" signal callers need for triage. A follow-up
 * Issue will land the dedicated health card once the grpchealth
 * codegen is in place.
 */
export function OpsPage() {
  const ops = useOps();
  const [refreshNonce, setRefreshNonce] = useState(0);
  const pollingOn = ops.state.pollMs > 0;
  const onPollToggle = (_: unknown, data: { checked: boolean }) => {
    ops.setPollMs(data.checked ? DEFAULT_POLL_MS : 0);
  };
  // The toolbar Refresh button refreshes both halves of the page: the
  // status cards (ops.refresh) and the metric panels (refreshNonce bump,
  // which the MetricsSection effect observes).
  const onRefresh = useCallback(() => {
    ops.refresh();
    setRefreshNonce((n) => n + 1);
  }, [ops]);
  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <div>
          <h1 className={styles.title}>Ops</h1>
          <p className={styles.lead}>
            Server status and replication health, polled every{" "}
            {Math.round(DEFAULT_POLL_MS / 1000)}s.
          </p>
        </div>
        <div className={styles.toolbar}>
          <Switch
            label={pollingOn ? "Auto-poll on" : "Auto-poll off"}
            checked={pollingOn}
            onChange={onPollToggle}
            data-testid="ops-poll-toggle"
          />
          <Button
            icon={<ArrowClockwise20Regular />}
            onClick={onRefresh}
            data-testid="ops-refresh"
          >
            Refresh
          </Button>
        </div>
      </header>
      <section className={styles.cards}>
        <ServerCard
          status={ops.state.server.status}
          data={ops.state.server.data}
          error={ops.state.server.error}
        />
        <ReplicationCard
          status={ops.state.replication.status}
          data={ops.state.replication.data}
          error={ops.state.replication.error}
        />
        <SearchStatusCard
          status={ops.state.server.status}
          data={ops.state.server.data}
          error={ops.state.server.error}
        />
      </section>
      <MetricsSection pollMs={ops.state.pollMs} refreshNonce={refreshNonce} />
    </div>
  );
}

interface ServerCardProps {
  status: "idle" | "loading" | "ready" | "error";
  data: ReturnType<typeof useOps>["state"]["server"]["data"];
  error: string | null;
}

function ServerCard({ status, data, error }: ServerCardProps) {
  return (
    <Card className={styles.card} data-testid="ops-server-card">
      <h2 className={styles.cardTitle}>Server status</h2>
      <p className={styles.cardLead}>
        From <code>GetServerStatus</code>. Vertex / edge counts are sampled and
        approximate.
      </p>
      {status === "loading" && <Spinner size="tiny" label="Loading…" />}
      {status === "error" && error && (
        <MessageBar intent="error" data-testid="ops-server-error">
          <MessageBarBody>GetServerStatus failed: {error}</MessageBarBody>
        </MessageBar>
      )}
      {status === "ready" && data && (
        <dl className={styles.definitionList}>
          {serverCardSummary(data).map(([label, value]) => (
            <ServerRow key={label} label={label} value={value} />
          ))}
        </dl>
      )}
    </Card>
  );
}

function ServerRow({ label, value }: { label: string; value: string }) {
  return (
    <>
      <dt>{label}</dt>
      <dd>{value}</dd>
    </>
  );
}

interface ReplicationCardProps {
  status: "idle" | "loading" | "ready" | "error";
  data: ReturnType<typeof useOps>["state"]["replication"]["data"];
  error: string | null;
}

function ReplicationCard({ status, data, error }: ReplicationCardProps) {
  return (
    <Card className={styles.card} data-testid="ops-replication-card">
      <h2 className={styles.cardTitle}>Replication</h2>
      <p className={styles.cardLead}>
        From <code>GetReplicationStatus</code>. Empty state on single-instance
        deployments.
      </p>
      {status === "loading" && <Spinner size="tiny" label="Loading…" />}
      {status === "error" && error && (
        <MessageBar intent="error" data-testid="ops-replication-error">
          <MessageBarBody>GetReplicationStatus failed: {error}</MessageBarBody>
        </MessageBar>
      )}
      {status === "ready" && data && (
        <>
          <dl className={styles.definitionList}>
            {replicationCardSummary(data).map(([label, value]) => (
              <ServerRow key={label} label={label} value={value} />
            ))}
          </dl>
          {data.enabled ? (
            data.peers.length > 0 ? (
              <div className={styles.peerTableScroll}>
                <table
                  className={styles.peerTable}
                  data-testid="ops-peer-table"
                >
                  <thead>
                    <tr>
                      <th>Peer</th>
                      <th>State</th>
                      <th>Last event</th>
                      <th>Applied seq</th>
                    </tr>
                  </thead>
                  <tbody>
                    {data.peers.map((p) => (
                      <PeerRows key={p.address} peer={p} />
                    ))}
                  </tbody>
                </table>
              </div>
            ) : (
              <p className={styles.empty} data-testid="ops-peer-empty">
                Replication enabled, but no peers have reported yet.
              </p>
            )
          ) : (
            <p className={styles.empty} data-testid="ops-replication-disabled">
              Single-instance deployment — set <code>LANTERN_PEERS</code> to
              wire replication. See the{" "}
              <a
                href="https://github.com/anaregdesign/lantern/blob/main/docs/ha-runbook.md"
                target="_blank"
                rel="noreferrer noopener"
              >
                HA runbook
              </a>
              .
            </p>
          )}
        </>
      )}
    </Card>
  );
}

function PeerRows({
  peer,
}: {
  peer: NonNullable<
    ReturnType<typeof useOps>["state"]["replication"]["data"]
  >["peers"][number];
}) {
  return (
    <>
      <tr>
        <td>{peer.address}</td>
        <td>
          <Badge appearance="filled" color={badgeColor(peer.state)}>
            {peer.state}
          </Badge>
        </td>
        <td>{formatStaleness(peer.stalenessMs)}</td>
        <td>{formatCount(peer.appliedSeq)}</td>
      </tr>
      {peer.error && (
        <tr>
          <td colSpan={4} className={styles.peerErrorRow}>
            ↪ {peer.error}
          </td>
        </tr>
      )}
    </>
  );
}

function badgeColor(
  state: string,
): "success" | "warning" | "danger" | "informative" {
  const intent = peerStatePillIntent(state as never);
  switch (intent) {
    case "success":
      return "success";
    case "warning":
      return "warning";
    case "error":
      return "danger";
    case "info":
      return "informative";
  }
}
