import type { GraphView } from "~/lib/client/usecase/illuminate/selectors";
import type { TraversalResultMetadata } from "./state";

export interface TraversalSummaryItem {
  label: string;
  value: string;
}

export interface TraversalVertexRow {
  key: string;
  hop: string;
}

export interface TraversalEdgeRow {
  source: string;
  target: string;
  weight: number;
}

export interface PageRankRow {
  rank: number;
  key: string;
  mass: number;
}

export interface TraversalResultModel {
  familyLabel: string;
  summary: TraversalSummaryItem[];
  vertices: TraversalVertexRow[];
  edges: TraversalEdgeRow[];
  pageRank: PageRankRow[];
}

/**
 * Builds an AT-friendly model from the exact result-producing command and
 * the canvas projection. The executed command remains independent from the
 * next-click picker, which may have changed after the result arrived.
 */
export function buildTraversalResultModel(
  traversal: TraversalResultMetadata,
  view: GraphView,
): TraversalResultModel {
  const vertices = [...view.nodes]
    .sort((a, b) => a.id.localeCompare(b.id))
    .map((node) => ({
      key: node.id,
      hop: Number.isFinite(node.hopDistance)
        ? String(node.hopDistance)
        : "not applicable",
    }));
  const edges = [...view.edges]
    .sort(
      (a, b) =>
        a.source.localeCompare(b.source) || a.target.localeCompare(b.target),
    )
    .map((edge) => ({
      source: edge.source,
      target: edge.target,
      weight: edge.weight,
    }));
  const common: TraversalSummaryItem[] = [
    { label: "Seed", value: traversal.command.seed },
    { label: "Members", value: String(vertices.length) },
    { label: "Edges", value: String(edges.length) },
    { label: "Weighting", value: traversal.command.weighting },
  ];
  if (traversal.command.vertexPrefix !== "") {
    common.push({
      label: "Vertex prefix",
      value: traversal.command.vertexPrefix,
    });
  }

  switch (traversal.command.verb) {
    case "bfs": {
      const hops = view.nodes
        .map((node) => node.hopDistance)
        .filter(Number.isFinite);
      return {
        familyLabel: "BFS",
        summary: [
          ...common,
          {
            label: "Hops returned",
            value: hops.length === 0 ? "0" : String(Math.max(...hops)),
          },
          { label: "Step", value: String(traversal.command.step) },
          { label: "Fan-out", value: String(traversal.command.fanOut) },
          { label: "Reduction", value: traversal.command.reduction },
          { label: "Objective", value: traversal.command.objective },
        ],
        vertices,
        edges,
        pageRank: [],
      };
    }
    case "pagerank": {
      const pageRank = view.edges
        .filter((edge) => edge.source === traversal.command.seed)
        .map((edge) => ({ key: edge.target, mass: edge.weight }))
        .sort((a, b) => b.mass - a.mass || a.key.localeCompare(b.key))
        .map((row, index) => ({ ...row, rank: index + 1 }));
      return {
        familyLabel: "Personalized PageRank",
        summary: [
          ...common,
          {
            label: "Top N",
            value:
              traversal.command.topN === 0
                ? "all positive mass"
                : String(traversal.command.topN),
          },
          {
            label: "Restart probability",
            value: formatPushKnob(traversal.command.restartProb),
          },
          {
            label: "Epsilon",
            value: formatPushKnob(traversal.command.epsilon),
          },
        ],
        vertices,
        edges,
        pageRank,
      };
    }
    case "community":
      return {
        familyLabel: "Local community",
        summary: [
          ...common,
          {
            label: "Max size",
            value:
              traversal.command.maxSize === 0
                ? "unbounded"
                : String(traversal.command.maxSize),
          },
          {
            label: "Restart probability",
            value: formatPushKnob(traversal.command.restartProb),
          },
          {
            label: "Epsilon",
            value: formatPushKnob(traversal.command.epsilon),
          },
          { label: "Reduction", value: traversal.command.reduction },
          { label: "Objective", value: traversal.command.objective },
        ],
        vertices,
        edges,
        pageRank: [],
      };
  }
}

function formatPushKnob(value: number): string {
  return value === 0 ? "server default" : String(value);
}
