import { useMemo } from "react";
import {
  tokenizeJson,
  type JsonTokenKind,
} from "~/lib/client/usecase/cli/json-syntax";
import styles from "./JsonView.module.css";

export interface JsonViewProps {
  /** A JSON document (typically `JSON.stringify(value, replacer, 2)`). */
  source: string;
}

const KIND_CLASS: Record<JsonTokenKind, string | undefined> = {
  key: styles.key,
  string: styles.string,
  number: styles.number,
  boolean: styles.boolean,
  null: styles.null,
  punctuation: styles.punctuation,
  whitespace: undefined,
};

/**
 * Render a JSON string with lightweight, theme-aware syntax colouring
 * (#512). Pairs with the in-house {@link tokenizeJson} lexer so the
 * admin bundle stays free of a highlighting dependency.
 *
 * Render-only: all lexing lives in the pure `json-syntax` module. The
 * visible text is identical to the raw JSON — only per-token colour is
 * added — so the `pre-wrap` scrollback keeps the pretty-printed
 * indentation and `toContainText` assertions keep matching.
 */
export function JsonView({ source }: JsonViewProps) {
  const spans = useMemo(() => {
    let offset = 0;
    return tokenizeJson(source).map((token) => {
      const key = offset;
      offset += token.text.length;
      return (
        <span key={key} className={KIND_CLASS[token.kind]}>
          {token.text}
        </span>
      );
    });
  }, [source]);

  return (
    <code className={styles.json} data-testid="cli-json">
      {spans}
    </code>
  );
}
