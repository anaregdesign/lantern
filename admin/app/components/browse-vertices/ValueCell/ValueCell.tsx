import type { Vertex } from "~/lib/client/infrastructure/api/scan-vertices";
import styles from "./ValueCell.module.css";

export interface ValueCellProps {
  vertex: Vertex;
}

const MAX_INLINE_LENGTH = 48;

interface RenderedValue {
  variant: string;
  display: string;
  title?: string;
}

/**
 * Compact, read-only renderer for the value carried on a `v1Vertex`. The
 * proto schema models the value as a `oneof` over 12 variants — this cell
 * picks the populated field and emits a short string suitable for a table
 * row, plus a tooltip with the full value when it has to be truncated.
 *
 * The exhaustive editor with per-variant inputs lands in F3.
 */
export function ValueCell({ vertex }: ValueCellProps) {
  const rendered = describe(vertex);
  return (
    <span className={styles.cell} title={rendered.title}>
      <span className={styles.variant} aria-label="value variant">
        {rendered.variant}
      </span>
      <span className={styles.display}>{rendered.display}</span>
    </span>
  );
}

function describe(vertex: Vertex): RenderedValue {
  if (vertex.bool !== undefined) {
    return { variant: "bool", display: vertex.bool ? "true" : "false" };
  }
  if (vertex.int32 !== undefined) {
    return { variant: "int32", display: String(vertex.int32) };
  }
  if (vertex.int64 !== undefined) {
    return { variant: "int64", display: vertex.int64 };
  }
  if (vertex.uint32 !== undefined) {
    return { variant: "uint32", display: String(vertex.uint32) };
  }
  if (vertex.uint64 !== undefined) {
    return { variant: "uint64", display: vertex.uint64 };
  }
  if (vertex.float32 !== undefined) {
    return { variant: "float32", display: String(vertex.float32) };
  }
  if (vertex.float64 !== undefined) {
    return { variant: "float64", display: String(vertex.float64) };
  }
  if (vertex.string !== undefined) {
    const full = JSON.stringify(vertex.string);
    return {
      variant: "string",
      display: truncate(full),
      title: full.length > MAX_INLINE_LENGTH ? vertex.string : undefined,
    };
  }
  if (vertex.bytes !== undefined) {
    const hex = bytesToHex(vertex.bytes);
    return {
      variant: "bytes",
      display: truncate(`0x${hex}`),
      title: hex.length > MAX_INLINE_LENGTH ? `0x${hex}` : undefined,
    };
  }
  if (vertex.timestamp !== undefined) {
    return { variant: "timestamp", display: vertex.timestamp };
  }
  if (vertex.duration !== undefined) {
    return { variant: "duration", display: vertex.duration };
  }
  if (vertex.nil) {
    return { variant: "nil", display: "∅" };
  }
  return { variant: "—", display: "—" };
}

function truncate(value: string): string {
  if (value.length <= MAX_INLINE_LENGTH) {
    return value;
  }
  return `${value.slice(0, MAX_INLINE_LENGTH - 1)}…`;
}

function bytesToHex(base64: string): string {
  if (typeof atob !== "function") {
    return base64;
  }
  try {
    const binary = atob(base64);
    let hex = "";
    for (let i = 0; i < binary.length; i++) {
      hex += binary.charCodeAt(i).toString(16).padStart(2, "0");
    }
    return hex;
  } catch {
    return base64;
  }
}
