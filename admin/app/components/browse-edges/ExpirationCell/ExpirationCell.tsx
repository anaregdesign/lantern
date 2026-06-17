import { Tooltip } from "@fluentui/react-components";
import styles from "./ExpirationCell.module.css";

export interface ExpirationCellProps {
  expiration?: string;
  warnWithinMs?: number;
}

const DEFAULT_WARN_WITHIN_MS = 60_000;

/**
 * Edge-feature copy of the expiration chip. Duplicated from
 * browse-vertices/ExpirationCell per the F2 boundary rule.
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
  if (expiresAt <= 0) {
    // Permanent sentinel: a no-TTL vertex/edge carries the server's zero
    // `Timestamp` (`0001-01-01T00:00:00Z`, or the Unix epoch), i.e. a
    // non-positive instant. Render "never" like an absent field rather
    // than a long-expired chip (mirrors the server's `IsLiveAt` rule).
    return <span className={styles.empty}>never</span>;
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
