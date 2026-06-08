import {
  Button,
  Table,
  TableBody,
  TableCell,
  TableHeader,
  TableHeaderCell,
  TableRow,
} from "@fluentui/react-components";
import { Link } from "react-router";
import { ValueCell } from "~/components/browse-vertices/ValueCell/ValueCell";
import { ExpirationCell } from "~/components/browse-vertices/ExpirationCell/ExpirationCell";
import type { GraphNode } from "~/lib/client/usecase/illuminate/selectors";
import styles from "./IlluminateTable.module.css";

export interface IlluminateTableProps {
  nodes: GraphNode[];
  /** Fire an Illuminate expansion using this row's key as the origin. */
  onExpand: (key: string) => void;
}

/**
 * Accessible companion view for the canvas. Keyboard and screen-reader
 * users get the same neighbourhood data as a flat table; clicking a
 * row's Expand button fires an additive Illuminate using that key as
 * the origin (#466 D11: idempotent, including for the initial seed).
 */
export function IlluminateTable({ nodes, onExpand }: IlluminateTableProps) {
  if (nodes.length === 0) {
    return null;
  }
  return (
    <Table
      aria-label="Illuminate neighbourhood"
      data-testid="illuminate-table"
      sortable={false}
    >
      <TableHeader>
        <TableRow>
          <TableHeaderCell className={styles.colKey}>Key</TableHeaderCell>
          <TableHeaderCell>Value</TableHeaderCell>
          <TableHeaderCell className={styles.colExp}>Expires</TableHeaderCell>
          <TableHeaderCell className={styles.colActions}>
            Actions
          </TableHeaderCell>
        </TableRow>
      </TableHeader>
      <TableBody>
        {nodes.map((node) => (
          <TableRow key={node.id}>
            <TableCell className={styles.colKey}>
              <Link
                to={`/vertices/${encodeURIComponent(node.id)}`}
                className={styles.keyLink}
              >
                {node.isInitialSeed ? (
                  <strong>{node.label}</strong>
                ) : (
                  node.label
                )}
              </Link>
            </TableCell>
            <TableCell>
              <ValueCell vertex={node.vertex} />
            </TableCell>
            <TableCell className={styles.colExp}>
              <ExpirationCell expiration={node.vertex.expiration} />
            </TableCell>
            <TableCell className={styles.colActions}>
              <Button
                appearance="subtle"
                size="small"
                onClick={() => onExpand(node.id)}
                aria-label={`Expand from ${node.id}`}
              >
                Expand
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
