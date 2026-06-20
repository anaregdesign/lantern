import { Fragment, useMemo } from "react";

import { tokenizeJson, type JsonTokenKind } from "./json";
import styles from "./StringValueView.module.css";

export interface JsonCodeBlockProps {
  /** Pretty-printed JSON to render with syntax highlighting. */
  code: string;
  "data-testid"?: string;
}

const TOKEN_CLASS: Record<JsonTokenKind, string | undefined> = {
  key: styles.jsonKey,
  string: styles.jsonString,
  number: styles.jsonNumber,
  boolean: styles.jsonKeyword,
  null: styles.jsonKeyword,
  punctuation: styles.jsonPunct,
  whitespace: undefined,
};

/**
 * Renders linted JSON as a syntax-highlighted code block. The hand-rolled
 * {@link tokenizeJson} classifies each run of source text and we wrap it in a
 * theme-aware coloured span; whitespace passes through untouched so the
 * two-space indentation is preserved exactly inside the `<pre>`.
 */
export function JsonCodeBlock({
  code,
  "data-testid": testId,
}: JsonCodeBlockProps) {
  const tokens = useMemo(() => tokenizeJson(code), [code]);
  return (
    <pre className={styles.json} data-testid={testId}>
      <code>
        {tokens.map((token, index) => {
          const className = TOKEN_CLASS[token.kind];
          return className ? (
            <span key={index} className={className}>
              {token.text}
            </span>
          ) : (
            <Fragment key={index}>{token.text}</Fragment>
          );
        })}
      </code>
    </pre>
  );
}
