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
  onIlluminate: (key: string) => void;
}

/**
 * Accessible companion view for the canvas. Keyboard and screen-reader
 * users get the same neighbourhood data as a flat table; clicking a row
 * pushes that key onto the seed history.
 */
export function IlluminateTable({ nodes, onIlluminate }: IlluminateTableProps) {
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
                {node.isSeed ? <strong>{node.label}</strong> : node.label}
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
                onClick={() => onIlluminate(node.id)}
                disabled={node.isSeed}
                aria-label={`Re-seed from ${node.id}`}
              >
                Re-seed
              </Button>
            </TableCell>
          </TableRow>
        ))}
      </TableBody>
    </Table>
  );
}
