import { useState } from "react";
import {
  Badge,
  Button,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  MessageBarTitle,
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
  Search24Regular,
} from "@fluentui/react-icons";
import { Link, useNavigate } from "react-router";
import { useSearchVertices } from "~/lib/client/usecase/search-vertices/use-search-vertices";
import {
  formatScore,
  selectCaption,
} from "~/lib/client/usecase/search-vertices/selectors";
import { ValueCell } from "~/components/browse-vertices/ValueCell/ValueCell";
import { ExpirationCell } from "~/components/browse-vertices/ExpirationCell/ExpirationCell";
import styles from "./SearchPage.module.css";

/**
 * Vertex content-search screen (#627). Runs the server's BM25 keyword
 * search over indexed string/bytes content, hydrates the ranked hits into
 * full vertices, and lets the operator pivot a result into the Illuminate
 * neighborhood — the same seed handoff Browse Vertices uses.
 *
 * Content search is enabled by default (opt-out). When a server has the
 * index turned off, the screen renders a calm "not enabled" notice rather
 * than an error.
 */
export function SearchPage() {
  const [query, setQuery] = useState("");
  const search = useSearchVertices(query);
  const navigate = useNavigate();

  const { status, results } = search.state;
  const caption = selectCaption(search.state);
  const showTable =
    query.length > 0 && status !== "disabled" && status !== "error";
  const showEmpty = status === "ready" && results.length === 0;
  const showLoading = status === "loading" && results.length === 0;

  return (
    <div className={styles.root}>
      <header className={styles.header}>
        <h1 className={styles.title}>Search</h1>
        <p className={styles.lead}>
          Full-text search over indexed vertex content, ranked by relevance.
          Select a result to explore its neighborhood in Illuminate.
        </p>
      </header>

      <section className={styles.controls}>
        <Field label="Query" className={styles.queryField}>
          <Input
            value={query}
            onChange={(_, data) => setQuery(data.value)}
            placeholder="e.g. distributed systems"
            contentBefore={<Search24Regular />}
            data-testid="search-query-input"
          />
        </Field>
        <div className={styles.controlsMeta}>
          {status === "ready" && results.length > 0 ? (
            <Badge
              appearance="tint"
              shape="rounded"
              data-testid="search-count-badge"
            >
              {results.length} {results.length === 1 ? "result" : "results"}
            </Badge>
          ) : null}
          <Button
            appearance="subtle"
            icon={<ArrowClockwise20Regular />}
            onClick={search.retry}
            disabled={query.length === 0 || status === "loading"}
            data-testid="search-refresh"
          >
            Refresh
          </Button>
        </div>
      </section>

      <p className={styles.caption} data-testid="search-caption">
        {caption}
      </p>

      {status === "disabled" ? (
        <MessageBar
          intent="info"
          className={styles.alert}
          data-testid="search-disabled"
        >
          <MessageBarBody>
            <MessageBarTitle>Content search is not enabled</MessageBarTitle>
            This server has the keyword index turned off. Enable it by starting
            the server with content search on (it is on by default; the operator
            may have set <code>LANTERN_SEARCH_ENABLED=false</code>).
          </MessageBarBody>
        </MessageBar>
      ) : status === "error" ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>
            {search.state.error ?? "Search failed."}
          </MessageBarBody>
        </MessageBar>
      ) : null}

      {query.length === 0 ? (
        <div className={styles.placeholder} data-testid="search-idle">
          <Search24Regular />
          <p>Type a query to search vertex content.</p>
        </div>
      ) : null}

      {showTable ? (
        <div className={styles.tableWrapper}>
          <Table
            aria-label="Search results"
            sortable={false}
            data-testid="search-results-table"
            className={styles.table}
          >
            <TableHeader>
              <TableRow>
                <TableHeaderCell className={styles.colKey}>Key</TableHeaderCell>
                <TableHeaderCell>Value</TableHeaderCell>
                <TableHeaderCell className={styles.colScore}>
                  Score
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
              {results.map((row) => (
                <TableRow key={row.key}>
                  <TableCell className={styles.colKey}>
                    <TableCellLayout>
                      <Link
                        to={`/vertices/${encodeURIComponent(row.key)}`}
                        className={styles.keyLink}
                      >
                        {row.key}
                      </Link>
                    </TableCellLayout>
                  </TableCell>
                  <TableCell>
                    {row.vertex ? (
                      <ValueCell vertex={row.vertex} />
                    ) : (
                      <span className={styles.expired}>expired</span>
                    )}
                  </TableCell>
                  <TableCell className={styles.colScore}>
                    <span className={styles.score} data-testid="search-score">
                      {formatScore(row.score)}
                    </span>
                  </TableCell>
                  <TableCell className={styles.colExp}>
                    <ExpirationCell expiration={row.vertex?.expiration} />
                  </TableCell>
                  <TableCell className={styles.colActions}>
                    <Button
                      appearance="subtle"
                      size="small"
                      icon={<LightbulbFilament20Regular />}
                      onClick={() =>
                        navigate(
                          `/illuminate?seed=${encodeURIComponent(row.key)}`,
                        )
                      }
                      aria-label={`Illuminate from ${row.key}`}
                    >
                      Illuminate
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          {showLoading ? (
            <div className={styles.placeholder} data-testid="search-loading">
              <Spinner size="tiny" label="Searching…" />
            </div>
          ) : null}
          {showEmpty ? (
            <div className={styles.placeholder} data-testid="search-empty">
              <p>No vertices match this query.</p>
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}
