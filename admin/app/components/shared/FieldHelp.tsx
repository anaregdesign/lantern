import styles from "./FieldHelp.module.css";

/** Visible guidance, with optional details available without hover. */
export function FieldHelp({
  meaning,
  example,
  details,
}: {
  meaning: string;
  example?: string;
  details?: string;
}) {
  return (
    <div className={styles.help}>
      <p>{meaning}</p>
      {example && <p>Example: {example}</p>}
      {details && (
        <details>
          <summary>Allowed values and details</summary>
          <p>{details}</p>
        </details>
      )}
    </div>
  );
}
