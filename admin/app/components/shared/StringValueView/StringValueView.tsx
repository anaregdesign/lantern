import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  Button,
  Switch,
  Tab,
  TabList,
  Tooltip,
} from "@fluentui/react-components";
import { Copy16Regular } from "@fluentui/react-icons";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { formatJson } from "./json";
import { JsonCodeBlock } from "./JsonCodeBlock";
import styles from "./StringValueView.module.css";

export interface StringValueViewProps {
  /** The raw string payload carried on a `string` vertex. */
  value: string;
}

const COPIED_RESET_MS = 1_200;

/** Which body the JSON renderer shows: the highlighted view or the raw text. */
type JsonView = "formatted" | "raw";

/**
 * Detail-surface renderer for `string` vertex values (#644, #759). Unlike the
 * compact, single-line {@link ValueCell} used in tables, this shows the full
 * payload inside a bounded, scrollable box so large blobs (e.g. agent-memory
 * notes) stay readable without blowing out the layout.
 *
 * When the value parses as a JSON object or array the renderer leads with a
 * linted (pretty-printed), syntax-highlighted code view and a JSON ⇄ Raw
 * `TabList`. Otherwise it keeps the prose-oriented Raw ⇄ Markdown `Switch`
 * (default Raw) that renders the value as GitHub-flavored Markdown on demand.
 *
 * Security: Markdown renders an arbitrary stored string, so this is an
 * XSS-sensitive path. We rely on `react-markdown`'s safe defaults — raw
 * HTML is NOT passed through (no `rehype-raw`/`allowDangerousHtml`) and the
 * built-in `urlTransform` neutralizes `javascript:` URLs. Rendered links
 * additionally open in a new tab with `rel="noopener noreferrer"`. The JSON
 * highlighter renders only plain text spans, never HTML.
 */
export function StringValueView({ value }: StringValueViewProps) {
  const json = useMemo(() => formatJson(value), [value]);
  const [markdown, setMarkdown] = useState(false);
  const [jsonView, setJsonView] = useState<JsonView>("formatted");
  const [copied, setCopied] = useState(false);
  const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(
    () => () => {
      if (copiedTimer.current) clearTimeout(copiedTimer.current);
    },
    [],
  );

  const copy = useCallback(async () => {
    try {
      await navigator.clipboard?.writeText(value);
      setCopied(true);
      if (copiedTimer.current) clearTimeout(copiedTimer.current);
      copiedTimer.current = setTimeout(() => {
        setCopied(false);
        copiedTimer.current = null;
      }, COPIED_RESET_MS);
    } catch {
      // Clipboard can reject in insecure contexts / when permission is
      // denied. The value is still visible, so there's nothing to recover.
    }
  }, [value]);

  if (value === "") {
    return (
      <span className={styles.empty} data-testid="vertex-string-empty">
        (empty string)
      </span>
    );
  }

  const copyButton = (
    <Tooltip
      content={copied ? "Copied" : "Copy value"}
      relationship="label"
      withArrow
    >
      <Button
        appearance="subtle"
        size="small"
        icon={<Copy16Regular />}
        aria-label="Copy string value"
        data-testid="vertex-string-copy"
        onClick={() => void copy()}
      />
    </Tooltip>
  );

  if (json) {
    return (
      <div className={styles.root} data-testid="vertex-string-view">
        <div className={styles.toolbar}>
          <TabList
            selectedValue={jsonView}
            onTabSelect={(_, data) => setJsonView(data.value as JsonView)}
            size="small"
            data-testid="vertex-string-json-tabs"
          >
            <Tab value="formatted" data-testid="vertex-string-json-tab">
              JSON
            </Tab>
            <Tab value="raw" data-testid="vertex-string-raw-tab">
              Raw
            </Tab>
          </TabList>
          {copyButton}
        </div>
        {jsonView === "formatted" ? (
          <JsonCodeBlock
            code={json.formatted}
            data-testid="vertex-string-json"
          />
        ) : (
          <pre className={styles.raw} data-testid="vertex-string-raw">
            {value}
          </pre>
        )}
      </div>
    );
  }

  return (
    <div className={styles.root} data-testid="vertex-string-view">
      <div className={styles.toolbar}>
        <Switch
          checked={markdown}
          onChange={(_, data) => setMarkdown(data.checked)}
          label="Markdown"
          data-testid="vertex-string-markdown-toggle"
        />
        {copyButton}
      </div>
      {markdown ? (
        <div className={styles.markdown} data-testid="vertex-string-markdown">
          <Markdown
            remarkPlugins={[remarkGfm]}
            components={{
              a: ({ node: _node, ...props }) => (
                <a {...props} target="_blank" rel="noopener noreferrer" />
              ),
            }}
          >
            {value}
          </Markdown>
        </div>
      ) : (
        <pre className={styles.raw} data-testid="vertex-string-raw">
          {value}
        </pre>
      )}
    </div>
  );
}
