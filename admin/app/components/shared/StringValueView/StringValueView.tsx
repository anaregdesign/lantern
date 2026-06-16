import { useCallback, useEffect, useRef, useState } from "react";
import { Button, Switch, Tooltip } from "@fluentui/react-components";
import { Copy16Regular } from "@fluentui/react-icons";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import styles from "./StringValueView.module.css";

export interface StringValueViewProps {
  /** The raw string payload carried on a `string` vertex. */
  value: string;
}

const COPIED_RESET_MS = 1_200;

/**
 * Detail-surface renderer for `string` vertex values (#644). Unlike the
 * compact, single-line {@link ValueCell} used in tables, this shows the
 * full payload multi-line (`pre-wrap`) inside a bounded, scrollable box so
 * large blobs (e.g. agent-memory notes) stay readable without blowing out
 * the layout. A Raw ⇄ Markdown `Switch` (default Raw) renders the value as
 * GitHub-flavored Markdown on demand.
 *
 * Security: Markdown renders an arbitrary stored string, so this is an
 * XSS-sensitive path. We rely on `react-markdown`'s safe defaults — raw
 * HTML is NOT passed through (no `rehype-raw`/`allowDangerousHtml`) and the
 * built-in `urlTransform` neutralizes `javascript:` URLs. Rendered links
 * additionally open in a new tab with `rel="noopener noreferrer"`.
 */
export function StringValueView({ value }: StringValueViewProps) {
  const [markdown, setMarkdown] = useState(false);
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

  return (
    <div className={styles.root} data-testid="vertex-string-view">
      <div className={styles.toolbar}>
        <Switch
          checked={markdown}
          onChange={(_, data) => setMarkdown(data.checked)}
          label="Markdown"
          data-testid="vertex-string-markdown-toggle"
        />
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
