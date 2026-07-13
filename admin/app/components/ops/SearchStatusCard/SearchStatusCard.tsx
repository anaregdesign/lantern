import {
  Badge,
  Card,
  MessageBar,
  MessageBarBody,
  Spinner,
} from "@fluentui/react-components";

import type { ServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import {
  searchHealthIntent,
  searchStatusSummary,
} from "~/lib/client/usecase/ops/selectors";
import styles from "./SearchStatusCard.module.css";

interface SearchStatusCardProps {
  status: "idle" | "loading" | "ready" | "error";
  data: ServerStatus | null;
  error: string | null;
}

export function SearchStatusCard({
  status,
  data,
  error,
}: SearchStatusCardProps) {
  const health = data?.search.index.health;
  const intent = health ? searchHealthIntent(health) : "warning";
  const badgeColor =
    intent === "success"
      ? "success"
      : intent === "error"
        ? "danger"
        : intent === "info"
          ? "informative"
          : "warning";
  return (
    <Card className={styles.card} data-testid="ops-search-card">
      <div className={styles.headingRow}>
        <h2 className={styles.title}>Search</h2>
        {status === "ready" && health && (
          <Badge
            appearance="filled"
            color={badgeColor}
            data-testid="ops-search-health"
          >
            {health}
          </Badge>
        )}
      </div>
      <p className={styles.lead}>
        Capability, budgets, and current index health from{" "}
        <code>GetServerStatus</code>.{" "}
        <a
          href="https://github.com/anaregdesign/lantern/blob/main/docs/search.md"
          target="_blank"
          rel="noreferrer"
          data-testid="ops-search-contract-link"
        >
          Search contract
        </a>
      </p>
      {status === "loading" && <Spinner size="tiny" label="Loading…" />}
      {status === "error" && error && (
        <MessageBar intent="error" data-testid="ops-search-error">
          <MessageBarBody>GetServerStatus failed: {error}</MessageBarBody>
        </MessageBar>
      )}
      {status === "ready" && data && (
        <dl className={styles.definitionList}>
          {searchStatusSummary(data.search).map(([label, value]) => (
            <div className={styles.row} key={label}>
              <dt>{label}</dt>
              <dd>{value}</dd>
            </div>
          ))}
        </dl>
      )}
    </Card>
  );
}
