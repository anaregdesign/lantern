import { useState } from "react";
import {
  Badge,
  Button,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  Spinner,
  Table,
  TableBody,
  TableCell,
  TableCellLayout,
  TableHeader,
  TableHeaderCell,
  TableRow,
} from "@fluentui/react-components";
import {
  ArrowClockwise20Regular,
  LightbulbFilament20Regular,
} from "@fluentui/react-icons";
import { Link, useNavigate } from "react-router";
import {
  useBrowseVertices,
  DEFAULT_VERTEX_PAGE_SIZE,
} from "~/lib/client/usecase/browse-vertices/use-browse-vertices";
import { ValueCell } from "../ValueCell/ValueCell";
import { ExpirationCell } from "../ExpirationCell/ExpirationCell";
import { Pager } from "../Pager/Pager";
import styles from "./BrowseVerticesPage.module.css";

/**
 * Vertex Browse screen — prefix scan, cursor pagination, TTL highlighting.
 * Per-row affordances open the (future) F3 vertex detail and the (future)
 * F4 Illuminate neighborhood. Edges have their own sibling screen so the
 * mental model stays single-entity at a time.
 */
export function BrowseVerticesPage() {
  const [prefix, setPrefix] = useState("");
  const browse = useBrowseVertices(prefix, {
    pageSize: DEFAULT_VERTEX_PAGE_SIZE,
  });
  const navigate = useNavigate();

  const showEmpty =
    browse.state.status === "ready" && browse.vertices.length === 0;

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>Vertices</h1>
          <nav className={styles.subNav} aria-label="Browse sections">
            <Link
              to="/vertices"
              className={`${styles.tab} ${styles.tabActive}`}
            >
              Vertices
            </Link>
            <Link to="/edges" className={styles.tab}>
              Edges
            </Link>
          </nav>
        </div>
        <p className={styles.lead}>
          Scan vertices by key prefix. Page size is {DEFAULT_VERTEX_PAGE_SIZE};
          expired or expiring rows are highlighted.
        </p>
      </header>

      <section className={styles.controls}>
        <Field label="Key prefix" className={styles.prefixField}>
          <Input
            value={prefix}
            onChange={(_, data) => setPrefix(data.value)}
            placeholder="e.g. user:"
            data-testid="vertex-prefix-input"
          />
        </Field>
        <div className={styles.controlsMeta}>
          {browse.count !== null ? (
            <Badge
              appearance="tint"
              shape="rounded"
              data-testid="vertex-count-badge"
            >
              {browse.count.toLocaleString()} vertices
            </Badge>
          ) : null}
          <Button
            appearance="subtle"
            icon={<ArrowClockwise20Regular />}
            onClick={browse.retry}
            disabled={browse.state.status === "loading"}
            data-testid="vertex-refresh"
          >
            Refresh
          </Button>
        </div>
      </section>

      {browse.state.error ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>{browse.state.error}</MessageBarBody>
        </MessageBar>
      ) : null}

      <div className={styles.tableWrapper}>
        <Table
          aria-label="Vertices"
          sortable={false}
          data-testid="vertices-table"
        >
          <TableHeader>
            <TableRow>
              <TableHeaderCell className={styles.colKey}>Key</TableHeaderCell>
              <TableHeaderCell>Value</TableHeaderCell>
              <TableHeaderCell className={styles.colExp}>
                Expires
              </TableHeaderCell>
              <TableHeaderCell className={styles.colActions}>
                Actions
              </TableHeaderCell>
            </TableRow>
          </TableHeader>
          <TableBody>
            {browse.vertices.map((vertex) => (
              <TableRow key={vertex.key ?? "(unknown)"}>
                <TableCell className={styles.colKey}>
                  <TableCellLayout>
                    <Link
                      to={`/vertices/${encodeURIComponent(vertex.key ?? "")}`}
                      className={styles.keyLink}
                    >
                      {vertex.key ?? "—"}
                    </Link>
                  </TableCellLayout>
                </TableCell>
                <TableCell>
                  <ValueCell vertex={vertex} />
                </TableCell>
                <TableCell className={styles.colExp}>
                  <ExpirationCell expiration={vertex.expiration} />
                </TableCell>
                <TableCell className={styles.colActions}>
                  <Button
                    appearance="subtle"
                    size="small"
                    icon={<LightbulbFilament20Regular />}
                    onClick={() =>
                      navigate(
                        `/illuminate?seed=${encodeURIComponent(vertex.key ?? "")}`,
                      )
                    }
                    aria-label={`Illuminate from ${vertex.key ?? "vertex"}`}
                  >
                    Illuminate
                  </Button>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>

        {browse.state.status === "loading" && browse.vertices.length === 0 ? (
          <div className={styles.placeholder} data-testid="vertices-loading">
            <Spinner size="tiny" label="Loading vertices…" />
          </div>
        ) : null}
        {showEmpty ? (
          <div className={styles.placeholder} data-testid="vertices-empty">
            <p>No vertices match this prefix.</p>
          </div>
        ) : null}
      </div>

      <Pager
        pageNumber={browse.pageNumber}
        canGoPrevious={browse.canGoPrevious}
        canGoNext={browse.canGoNext}
        loading={browse.state.status === "loading"}
        onPrevious={browse.goPrevious}
        onNext={browse.goNext}
      />
    </div>
  );
}
