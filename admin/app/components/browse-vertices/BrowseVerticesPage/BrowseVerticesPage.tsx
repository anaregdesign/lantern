import { useEffect, useState } from "react";
import {
  Badge,
  Button,
  Dropdown,
  Field,
  Input,
  MessageBar,
  MessageBarBody,
  MessageBarTitle,
  Option,
  Spinner,
  Switch,
  Tab,
  TabList,
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
  Search20Regular,
} from "@fluentui/react-icons";
import { Link, useNavigate } from "react-router";
import {
  useBrowseVertices,
  DEFAULT_VERTEX_PAGE_SIZE,
} from "~/lib/client/usecase/browse-vertices/use-browse-vertices";
import { useSearchVertices } from "~/lib/client/usecase/search-vertices/use-search-vertices";
import { selectTotalPages } from "~/lib/client/usecase/browse-vertices/selectors";
import {
  formatScore,
  selectCaption,
} from "~/lib/client/usecase/search-vertices/selectors";
import type {
  SearchFuzziness,
  SearchMatchMode,
} from "~/lib/client/usecase/search-vertices/state";
import type { Vertex } from "~/lib/client/infrastructure/api/types";
import { getServerStatus } from "~/lib/client/infrastructure/api/get-server-status";
import { useLanternClient } from "~/lib/client/infrastructure/api/use-lantern-client";
import { ValueCell } from "../ValueCell/ValueCell";
import { ExpirationCell } from "../ExpirationCell/ExpirationCell";
import { Pager } from "../Pager/Pager";
import styles from "./BrowseVerticesPage.module.css";

/**
 * How the operator locates a vertex on the Data surface:
 *  - `prefix`: cursor-paged scan by key prefix.
 *  - `search`: BM25 content search over indexed vertex values. #650 folds
 *    the former standalone Search screen in here as a second find mode so
 *    a prefix scan and a content query land in the same table.
 */
type FindMode = "prefix" | "search";

/** Human labels for the content-search match modes (#892). */
const MATCH_MODE_LABELS: Record<SearchMatchMode, string> = {
  server: "Server default",
  any: "Any word (OR)",
  all: "All words (AND)",
  "min-should": "At least N words",
};

/** A vertex row normalised across both find modes. */
interface VertexRow {
  key: string;
  vertex: Vertex | null;
  /** BM25 relevance score; present only for content-search hits. */
  score?: number;
}

/**
 * Vertex Browse screen — the vertex half of the unified **Data** surface
 * (#650). Locates vertices either by key-prefix scan (cursor pagination,
 * count badge) or by BM25 content search (ranked hits with a relevance
 * score), then offers the same per-row Edit + Illuminate handoffs. Edges
 * are the sibling half, reachable via the Vertices / Edges sub-nav.
 */
export function BrowseVerticesPage() {
  const [mode, setMode] = useState<FindMode>("prefix");
  const [prefix, setPrefix] = useState("");
  const [query, setQuery] = useState("");
  const [searchPrefix, setSearchPrefix] = useState("");
  const [matchMode, setMatchMode] = useState<SearchMatchMode>("server");
  const [minShouldMatch, setMinShouldMatch] = useState(2);
  const [phrase, setPhrase] = useState(false);
  const [fuzziness, setFuzziness] = useState<SearchFuzziness>(0);
  const [prefixTerms, setPrefixTerms] = useState(false);
  // null = loading, undefined = capability lookup failed.
  const [positionsEnabled, setPositionsEnabled] = useState<
    boolean | null | undefined
  >(null);
  const client = useLanternClient();
  const browse = useBrowseVertices(prefix, {
    pageSize: DEFAULT_VERTEX_PAGE_SIZE,
  });
  const search = useSearchVertices(client, query, {
    prefix: searchPrefix,
    matchMode,
    minShouldMatch,
    phrase,
    fuzziness,
    prefixTerms,
  });
  const navigate = useNavigate();

  useEffect(() => {
    const controller = new AbortController();
    setPositionsEnabled(null);
    setPhrase(false);
    void getServerStatus(client, { signal: controller.signal })
      .then((status) => {
        setPositionsEnabled(status.search.positionsEnabled);
        if (!status.search.positionsEnabled) setPhrase(false);
      })
      .catch(() => {
        if (!controller.signal.aborted) setPositionsEnabled(undefined);
      });
    return () => controller.abort();
  }, [client]);

  const searching = mode === "search";
  const searchStatus = search.state.status;
  const phraseCompatible =
    matchMode === "server" && fuzziness === 0 && !prefixTerms;

  // Normalise both find modes into a single row list so the table markup
  // (and the Edit + Illuminate handoffs) stays a single code path.
  const rows: VertexRow[] = searching
    ? search.state.results.map((hit) => ({
        key: hit.key,
        vertex: hit.vertex,
        score: hit.score,
      }))
    : browse.vertices.map((vertex) => ({
        key: vertex.key ?? "",
        vertex,
      }));

  const showSearchTable =
    query.length > 0 && searchStatus !== "disabled" && searchStatus !== "error";
  const showTable = searching ? showSearchTable : true;
  const showLoading = searching
    ? searchStatus === "loading" && rows.length === 0
    : browse.state.status === "loading" && rows.length === 0;
  const showEmpty = searching
    ? searchStatus === "ready" && rows.length === 0
    : browse.state.status === "ready" && rows.length === 0;

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
          {searching
            ? "Full-text search over indexed vertex content, ranked by relevance."
            : `Scan vertices by key prefix. Page size is ${DEFAULT_VERTEX_PAGE_SIZE}; expired or expiring rows are highlighted.`}
        </p>
      </header>

      <TabList
        selectedValue={mode}
        onTabSelect={(_, data) => setMode(data.value as FindMode)}
        data-testid="vertex-find-mode"
      >
        <Tab value="prefix">Key prefix</Tab>
        <Tab value="search">Content search</Tab>
      </TabList>

      <section className={styles.controls}>
        {searching ? (
          <Field label="Query" className={styles.prefixField}>
            <Input
              value={query}
              onChange={(_, data) => setQuery(data.value)}
              placeholder="e.g. distributed systems"
              contentBefore={<Search20Regular />}
              data-testid="search-query-input"
            />
          </Field>
        ) : (
          <Field label="Key prefix" className={styles.prefixField}>
            <Input
              value={prefix}
              onChange={(_, data) => setPrefix(data.value)}
              placeholder="e.g. user:"
              data-testid="vertex-prefix-input"
            />
          </Field>
        )}
        <div className={styles.controlsMeta}>
          {searching ? (
            searchStatus === "ready" && rows.length > 0 ? (
              <Badge
                appearance="tint"
                shape="rounded"
                data-testid="search-count-badge"
              >
                {search.state.truncated
                  ? `Top ${rows.length} shown`
                  : `${rows.length} ${rows.length === 1 ? "result" : "results"}`}
              </Badge>
            ) : null
          ) : browse.count !== null ? (
            <Badge
              appearance="tint"
              shape="rounded"
              data-testid="vertex-count-badge"
            >
              {browse.count.toLocaleString()} vertices
            </Badge>
          ) : null}
          {searching ? (
            <Button
              appearance="subtle"
              icon={<ArrowClockwise20Regular />}
              onClick={search.retry}
              disabled={query.length === 0 || searchStatus === "loading"}
              data-testid="search-refresh"
            >
              Refresh
            </Button>
          ) : (
            <Button
              appearance="subtle"
              icon={<ArrowClockwise20Regular />}
              onClick={browse.retry}
              disabled={browse.state.status === "loading"}
              data-testid="vertex-refresh"
            >
              Refresh
            </Button>
          )}
        </div>
      </section>

      {searching ? (
        <section className={styles.searchOptions} data-testid="search-options">
          <Field label="Match">
            <Dropdown
              className={styles.searchOptionsMode}
              selectedOptions={[matchMode]}
              value={MATCH_MODE_LABELS[matchMode]}
              disabled={phrase}
              onOptionSelect={(_, data) =>
                setMatchMode((data.optionValue as SearchMatchMode) ?? "server")
              }
              data-testid="search-mode"
            >
              <Option value="server" text="Server default">
                Server default
              </Option>
              <Option value="any" text="Any word (OR)">
                Any word (OR)
              </Option>
              <Option value="all" text="All words (AND)">
                All words (AND)
              </Option>
              <Option value="min-should" text="At least N words">
                At least N words
              </Option>
            </Dropdown>
          </Field>
          {matchMode === "min-should" ? (
            <Field label="Minimum words">
              <Input
                type="number"
                min={1}
                step={1}
                value={String(minShouldMatch)}
                disabled={phrase}
                onChange={(_, data) => {
                  const value = Number(data.value);
                  if (Number.isInteger(value) && value > 0) {
                    setMinShouldMatch(value);
                  }
                }}
                data-testid="search-min-should"
              />
            </Field>
          ) : null}
          <Field label="Key namespace prefix">
            <Input
              value={searchPrefix}
              onChange={(_, data) => setSearchPrefix(data.value)}
              placeholder="e.g. user:"
              data-testid="search-prefix"
            />
          </Field>
          <Field label="Fuzziness">
            <Dropdown
              selectedOptions={[String(fuzziness)]}
              value={String(fuzziness)}
              disabled={phrase}
              onOptionSelect={(_, data) =>
                setFuzziness(Number(data.optionValue ?? 0) as SearchFuzziness)
              }
              data-testid="search-fuzzy"
            >
              <Option value="0">0 (exact)</Option>
              <Option value="1">1 edit</Option>
              <Option value="2">2 edits</Option>
            </Dropdown>
          </Field>
          <Switch
            label="Prefix terms"
            checked={prefixTerms}
            disabled={phrase}
            onChange={(_, data) => setPrefixTerms(data.checked)}
            data-testid="search-prefix-terms"
          />
          <div>
            <Switch
              label="Phrase"
              checked={phrase}
              disabled={positionsEnabled !== true || !phraseCompatible}
              onChange={(_, data) => setPhrase(data.checked)}
              data-testid="search-phrase"
            />
            {positionsEnabled !== true || !phraseCompatible ? (
              <small data-testid="search-phrase-unavailable">
                {!phraseCompatible
                  ? "Choose Server default, fuzziness 0, and disable prefix terms to use phrase search."
                  : positionsEnabled === false
                    ? "Unavailable: this server does not store positional postings."
                    : positionsEnabled === undefined
                      ? "Unavailable: server phrase capabilities could not be loaded."
                      : "Checking server phrase capabilities…"}
              </small>
            ) : null}
          </div>
        </section>
      ) : null}

      {searching ? (
        <p className={styles.caption} data-testid="search-caption">
          {selectCaption(search.state)}
        </p>
      ) : null}

      {searching && searchStatus === "disabled" ? (
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
      ) : null}

      {searching && searchStatus === "error" ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>
            {search.state.error ?? "Search failed."}
          </MessageBarBody>
        </MessageBar>
      ) : null}

      {searching && search.state.loadMoreError ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>{search.state.loadMoreError}</MessageBarBody>
        </MessageBar>
      ) : null}

      {!searching && browse.state.error ? (
        <MessageBar intent="error" className={styles.alert}>
          <MessageBarBody>{browse.state.error}</MessageBarBody>
        </MessageBar>
      ) : null}

      {searching && query.length === 0 ? (
        <div className={styles.placeholder} data-testid="search-idle">
          <Search20Regular />
          <p>Type a query to search vertex content.</p>
        </div>
      ) : null}

      {showTable ? (
        <div className={styles.tableWrapper}>
          <Table
            aria-label={searching ? "Search results" : "Vertices"}
            sortable={false}
            data-testid={searching ? "search-results-table" : "vertices-table"}
            className={styles.table}
          >
            <TableHeader>
              <TableRow>
                <TableHeaderCell className={styles.colKey}>Key</TableHeaderCell>
                <TableHeaderCell>Value</TableHeaderCell>
                {searching ? (
                  <TableHeaderCell className={styles.colScore}>
                    Score
                  </TableHeaderCell>
                ) : null}
                <TableHeaderCell className={styles.colExp}>
                  Expires
                </TableHeaderCell>
                <TableHeaderCell className={styles.colActions}>
                  Actions
                </TableHeaderCell>
              </TableRow>
            </TableHeader>
            <TableBody>
              {rows.map((row) => (
                <TableRow key={row.key || "(unknown)"}>
                  <TableCell className={styles.colKey}>
                    <TableCellLayout>
                      <Link
                        to={`/vertices/${encodeURIComponent(row.key)}`}
                        className={styles.keyLink}
                        title={row.key}
                      >
                        {row.key || "—"}
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
                  {searching ? (
                    <TableCell className={styles.colScore}>
                      <span className={styles.score} data-testid="search-score">
                        {formatScore(row.score ?? 0)}
                      </span>
                    </TableCell>
                  ) : null}
                  <TableCell className={styles.colExp}>
                    <ExpirationCell expiration={row.vertex?.expiration} />
                  </TableCell>
                  <TableCell className={styles.colActions}>
                    {row.vertex ? (
                      <Button
                        appearance="subtle"
                        size="small"
                        icon={<Edit20Regular />}
                        onClick={() =>
                          navigate(
                            `/vertices/${encodeURIComponent(row.key)}?edit=1`,
                          )
                        }
                        aria-label={`Edit vertex ${row.key || "vertex"}`}
                        data-testid="vertex-row-edit"
                      >
                        Edit
                      </Button>
                    ) : null}
                    <Button
                      appearance="subtle"
                      size="small"
                      icon={<LightbulbFilament20Regular />}
                      onClick={() =>
                        navigate(`/cli?seed=${encodeURIComponent(row.key)}`)
                      }
                      aria-label={`Illuminate from ${row.key || "vertex"}`}
                    >
                      Illuminate
                    </Button>
                  </TableCell>
                </TableRow>
              ))}
            </TableBody>
          </Table>

          {showLoading ? (
            <div
              className={styles.placeholder}
              data-testid={searching ? "search-loading" : "vertices-loading"}
            >
              <Spinner
                size="tiny"
                label={searching ? "Searching…" : "Loading vertices…"}
              />
            </div>
          ) : null}
          {showEmpty ? (
            <div
              className={styles.placeholder}
              data-testid={searching ? "search-empty" : "vertices-empty"}
            >
              <p>
                {searching
                  ? "No vertices match this query."
                  : "No vertices match this prefix."}
              </p>
            </div>
          ) : null}
        </div>
      ) : null}

      {!searching ? (
        <Pager
          pageNumber={browse.pageNumber}
          totalPages={selectTotalPages(browse.state, browse.pageSize)}
          canGoPrevious={browse.canGoPrevious}
          canGoNext={browse.canGoNext}
          loading={browse.state.status === "loading"}
          onPrevious={browse.goPrevious}
          onNext={browse.goNext}
        />
      ) : search.state.nextCursor !== null ? (
        <div className={styles.searchPager}>
          <Button
            appearance="secondary"
            onClick={search.loadMore}
            disabled={!search.canLoadMore}
            data-testid="search-load-more"
          >
            {search.state.loadingMore ? "Loading…" : "Load more"}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
<Option value="server" text="Server default">
  Server default
</Option>;
