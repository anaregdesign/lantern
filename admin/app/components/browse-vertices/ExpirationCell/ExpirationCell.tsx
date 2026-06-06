import { Tooltip } from "@fluentui/react-components";
import styles from "./ExpirationCell.module.css";

export interface ExpirationCellProps {
  expiration?: string;
  /** Threshold (ms from now) under which a row is flagged as "expiring soon". */
  warnWithinMs?: number;
}

const DEFAULT_WARN_WITHIN_MS = 60_000;

/**
 * Renders a vertex/edge expiration timestamp as a relative-time chip with
 * the absolute ISO string surfaced as a tooltip. Rows that fall inside the
 * warning window (default: 60s) are tinted so reviewers can spot TTL churn
 * at a glance.
 */
export function ExpirationCell({
  expiration,
  warnWithinMs = DEFAULT_WARN_WITHIN_MS,
}: ExpirationCellProps) {
  if (!expiration) {
    return <span className={styles.empty}>never</span>;
  }
  const expiresAt = Date.parse(expiration);
  if (!Number.isFinite(expiresAt)) {
    return <span className={styles.empty}>{expiration}</span>;
  }
  const now = Date.now();
  const deltaMs = expiresAt - now;
  const expired = deltaMs <= 0;
  const warning = !expired && deltaMs <= warnWithinMs;
  const className = [
    styles.chip,
    expired ? styles.expired : "",
    warning ? styles.warning : "",
  ]
    .filter(Boolean)
    .join(" ");
  return (
    <Tooltip content={expiration} relationship="label" withArrow>
      <span className={className} data-testid="expiration-chip">
        {formatRelative(deltaMs)}
      </span>
    </Tooltip>
  );
}

function formatRelative(deltaMs: number): string {
  const abs = Math.abs(deltaMs);
  const suffix = deltaMs >= 0 ? "" : " ago";
  const prefix = deltaMs >= 0 ? "in " : "";
  if (abs < 1_000) {
    return `${prefix}${Math.round(abs)}ms${suffix}`;
  }
  if (abs < 60_000) {
    return `${prefix}${Math.round(abs / 1_000)}s${suffix}`;
  }
  if (abs < 3_600_000) {
    return `${prefix}${Math.round(abs / 60_000)}m${suffix}`;
  }
  if (abs < 86_400_000) {
    return `${prefix}${Math.round(abs / 3_600_000)}h${suffix}`;
  }
  return `${prefix}${Math.round(abs / 86_400_000)}d${suffix}`;
}
