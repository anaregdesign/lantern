import { useState } from "react";
import {
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
  Edit20Regular,
  LightbulbFilament20Regular,
} from "@fluentui/react-icons";
import { Link, useNavigate } from "react-router";
import {
  useBrowseEdges,
  DEFAULT_EDGE_PAGE_SIZE,
} from "~/lib/client/usecase/browse-edges/use-browse-edges";
import { ExpirationCell } from "../ExpirationCell/ExpirationCell";
import { Pager } from "../Pager/Pager";
import styles from "./BrowseEdgesPage.module.css";

/**
 * Edge Browse screen — independent tail / head prefix filters, cursor
 * pagination, TTL highlighting. Weight is rendered raw for now; F3 will
 * add inline editing.
 */
export function BrowseEdgesPage() {
  const [tailPrefix, setTailPrefix] = useState("");
  const [headPrefix, setHeadPrefix] = useState("");
  const browse = useBrowseEdges(tailPrefix, headPrefix, {
    pageSize: DEFAULT_EDGE_PAGE_SIZE,
  });
  const navigate = useNavigate();

  const showEmpty =
    browse.state.status === "ready" && browse.edges.length === 0;

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <div className={styles.titleRow}>
          <h1 className={styles.title}>Edges</h1>
          <nav className={styles.subNav} aria-label="Browse sections">
            <Link to="/vertices" className={styles.tab}>
              Vertices
            </Link>
            <Link to="/edges" className={`${styles.tab} ${styles.tabActive}`}>
              Edges
            </Link>
          </nav>
        </div>
        <p className={styles.lead}>
          Scan edges by tail and/or head key prefix. Page size is{" "}
          {DEFAULT_EDGE_PAGE_SIZE}; expired or expiring rows are highlighted.
        </p>
      </header>

      <section className={styles.controls}>
        <Field label="Tail prefix" className={styles.prefixField}>
          <Input
            value={tailPrefix}
            onChange={(_, data) => setTailPrefix(data.value)}
            placeholder="e.g. user:"
            data-testid="edge-tail-prefix-input"
          />
        </Field>
        <Field label="Head prefix" className={styles.prefixField}>
          <Input
            value={headPrefix}
            onChange={(_, data) => setHeadPrefix(data.value)}
            placeholder="e.g. post:"
            data-testid="edge-head-prefix-input"
          />
        </Field>
        <div className={styles.controlsMeta}>
          <Button
            appearance="subtle"
            icon={<ArrowClockwise20Regular />}
            onClick={browse.retry}
            disabled={browse.state.status === "loading"}
            data-testid="edge-refresh"
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
        <Table aria-label="Edges" sortable={false} data-testid="edges-table">
          <TableHeader>
            <TableRow>
              <TableHeaderCell className={styles.colKey}>Tail</TableHeaderCell>
              <TableHeaderCell className={styles.colKey}>Head</TableHeaderCell>
              <TableHeaderCell className={styles.colWeight}>
                Weight
              </TableHeaderCell>
              <TableHeaderCell className={styles.colExp}>
                Expires
              </TableHeaderCell>
              <TableHeaderCell className={styles.colActions}>
                Actions
              </TableHeaderCell>
            </TableRow>
          </TableHeader>
          <TableBody>
            {browse.edges.map((edge) => {
              const tail = edge.tail ?? "";
              const head = edge.head ?? "";
              return (
                <TableRow key={`${tail}\u0000${head}`}>
                  <TableCell className={styles.colKey}>
                    <TableCellLayout>
                      <Link
                        to={`/vertices/${encodeURIComponent(tail)}`}
                        className={styles.keyLink}
                      >
                        {tail || "—"}
                      </Link>
                    </TableCellLayout>
                  </TableCell>
                  <TableCell className={styles.colKey}>
                    <TableCellLayout>
                      <Link
                        to={`/vertices/${encodeURIComponent(head)}`}
                        className={styles.keyLink}
                      >
                        {head || "—"}
                      </Link>
                    </TableCellLayout>
                  </TableCell>
                  <TableCell className={styles.colWeight}>
                    <span className={styles.weight}>
                      {edge.weight !== undefined ? edge.weight : "—"}
                    </span>
                  </TableCell>
                  <TableCell className={styles.colExp}>
                    <ExpirationCell expiration={edge.expiration} />
                  </TableCell>
                  <TableCell className={styles.colActions}>
                    <Button
                      appearance="subtle"
                      size="small"
                      icon={<Edit20Regular />}
                      onClick={() =>
                        navigate(
                          `/edges/${encodeURIComponent(tail)}/${encodeURIComponent(head)}`,
                        )
                      }
                      aria-label={`Edit edge ${tail || "tail"} → ${head || "head"}`}
                      data-testid="edge-row-edit"
                    >
                      Edit
                    </Button>
                    <Button
                      appearance="subtle"
                      size="small"
                      icon={<LightbulbFilament20Regular />}
                      onClick={() =>
                        navigate(`/illuminate?seed=${encodeURIComponent(tail)}`)
                      }
                      aria-label={`Illuminate from ${tail || "tail"}`}
                    >
                      Illuminate
                    </Button>
                  </TableCell>
                </TableRow>
              );
            })}
          </TableBody>
        </Table>

        {browse.state.status === "loading" && browse.edges.length === 0 ? (
          <div className={styles.placeholder} data-testid="edges-loading">
            <Spinner size="tiny" label="Loading edges…" />
          </div>
        ) : null}
        {showEmpty ? (
          <div className={styles.placeholder} data-testid="edges-empty">
            <p>No edges match these prefixes.</p>
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
