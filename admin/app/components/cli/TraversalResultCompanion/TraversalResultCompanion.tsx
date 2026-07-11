import { Button } from "@fluentui/react-components";
import { Link } from "react-router";
import type { LatestGraph } from "~/lib/client/usecase/cli/state";
import { buildTraversalResultModel } from "~/lib/client/usecase/cli/traversal-result";
import styles from "./TraversalResultCompanion.module.css";

export interface TraversalResultCompanionProps {
  graph: LatestGraph;
  onRunFromHere: (key: string) => void;
}

/**
 * Keyboard-accessible counterpart to the visual Sigma canvas. It exposes the
 * exact current result's metadata, vertices, edges, and family-specific
 * ranking without relying on colour, hover, or pointer interaction.
 */
export function TraversalResultCompanion({
  graph,
  onRunFromHere,
}: TraversalResultCompanionProps) {
  if (graph.traversal === null) return null;
  const model = buildTraversalResultModel(graph.traversal, graph.view);

  return (
    <section
      className={styles.root}
      aria-label="Traversal result companion"
      data-testid="traversal-result-companion"
    >
      <div className={styles.heading}>
        <div>
          <h2>Current result: {model.familyLabel}</h2>
          <p>
            These are the parameters that produced this result. “Run from here”
            uses the separate Next walk controls in the terminal.
          </p>
        </div>
      </div>

      <dl className={styles.summary} data-testid="traversal-result-summary">
        {model.summary.map((item) => (
          <div key={item.label}>
            <dt>{item.label}</dt>
            <dd>{item.value}</dd>
          </div>
        ))}
      </dl>

      {model.pageRank.length > 0 ? (
        <ResultTable caption="PageRank mass ranking">
          <thead>
            <tr>
              <th scope="col">Rank</th>
              <th scope="col">Vertex</th>
              <th scope="col">Mass</th>
            </tr>
          </thead>
          <tbody>
            {model.pageRank.map((row) => (
              <tr key={row.key}>
                <td>{row.rank}</td>
                <td>{row.key}</td>
                <td>{row.mass}</td>
              </tr>
            ))}
          </tbody>
        </ResultTable>
      ) : null}

      <ResultTable caption={`Vertices (${model.vertices.length})`}>
        <thead>
          <tr>
            <th scope="col">Vertex</th>
            <th scope="col">BFS hop</th>
            <th scope="col">Actions</th>
          </tr>
        </thead>
        <tbody>
          {model.vertices.map((row) => (
            <tr key={row.key}>
              <td>{row.key}</td>
              <td>{row.hop}</td>
              <td className={styles.actions}>
                <Button
                  size="small"
                  onClick={() => onRunFromHere(row.key)}
                  aria-label={`Run from ${row.key} using next-walk controls`}
                >
                  Run from here
                </Button>
                <Link
                  to={`/vertices/${encodeURIComponent(row.key)}`}
                  aria-label={`Inspect ${row.key}`}
                >
                  Inspect
                </Link>
              </td>
            </tr>
          ))}
        </tbody>
      </ResultTable>

      <ResultTable caption={`Edges (${model.edges.length})`}>
        <thead>
          <tr>
            <th scope="col">Tail</th>
            <th scope="col">Head</th>
            <th scope="col">Weight</th>
          </tr>
        </thead>
        <tbody>
          {model.edges.map((row) => (
            <tr key={`${row.source}→${row.target}`}>
              <td>{row.source}</td>
              <td>{row.target}</td>
              <td>{row.weight}</td>
            </tr>
          ))}
        </tbody>
      </ResultTable>
    </section>
  );
}

function ResultTable({
  caption,
  children,
}: {
  caption: string;
  children: React.ReactNode;
}) {
  return (
    <div className={styles.tableWrap}>
      <table>
        <caption>{caption}</caption>
        {children}
      </table>
    </div>
  );
}
