package cmd

import (
	"encoding/json"
	"fmt"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	illuminateStep         uint32
	illuminateK            uint32
	illuminateAlgorithmStr string
	illuminateObjectiveStr string
	illuminateWeightingStr string
	illuminatePrefixStr    string
)

// algorithmByName, objectiveByName, weightingByName map human-friendly
// flag values to the SDK enums. The Illuminate API is the orthogonal
// (algorithm, objective, weighting) triple introduced in #410.
var algorithmByName = map[string]client.Algorithm{
	"none": client.AlgorithmUnspecified,
	"":     client.AlgorithmUnspecified,
	"mst":  client.AlgorithmMinimumSpanningTree,
	"spt":  client.AlgorithmShortestPathTree,
}

var objectiveByName = map[string]client.Objective{
	"":         client.ObjectiveUnspecified,
	"min":      client.ObjectiveMinimize,
	"minimize": client.ObjectiveMinimize,
	"max":      client.ObjectiveMaximize,
	"maximize": client.ObjectiveMaximize,
}

var weightingByName = map[string]client.Weighting{
	"":      client.WeightingUnspecified,
	"raw":   client.WeightingRaw,
	"tfidf": client.WeightingTFIDF,
}

var illuminateCmd = &cobra.Command{
	Use:   "illuminate <seed>",
	Short: "Walk the graph from <seed> and return the visited subgraph",
	Long: `Run a bounded breadth-first walk from <seed> and emit the visited subgraph
as JSON.

PARAMETERS
  --step <uint32>       maximum walk depth from the seed (default 1)
  --k <uint32>          max neighbours visited per node (default 10)

ORTHOGONAL ILLUMINATE AXES (#410)
  --algorithm <mode>    post-traversal subgraph reduction:
                          none  (default) return the raw discovered subgraph
                          mst   minimum or maximum spanning tree
                          spt   shortest-path tree rooted at the seed
  --objective <dir>     direction of the weight-sensitive optimisation;
                        governs BOTH the per-hop top-k pruning and the
                        algorithm-driven reduction (#560):
                          max   (default) largest-weight edges win (use when
                                weight encodes RELEVANCE)
                          min   smallest-weight edges win (use when weight
                                encodes COST)
  --weighting <mode>    edge-weight transform applied BEFORE the walk:
                          raw   (default) edge.weight as stored
                          tfidf re-score using TF-IDF over the per-vertex
                                out-edge distribution

FRONTIER FILTER (#604)
  --prefix <string>     restrict the walk frontier to vertices whose key
                        has this prefix. The seed is always kept as the
                        anchor even if it does not match. Empty (default)
                        = no filter. The value is case-sensitive. Applied
                        server-side BEFORE per-hop top-k and any --algorithm
                        reduction, so --prefix with --algorithm mst|spt
                        yields a tree over the prefix-induced subgraph, NOT
                        a shortest path in the full graph.

OUTPUT
  JSON object on stdout:
    {
      "vertices": { "<key>": { ...vertex... }, ... },
      "edges":    { "<tail>": { "<head>": <weight>, ... }, ... }
    }

NOTES
  Illuminate is read-only and idempotent. It is in the client's default
  retry policy.

  The walk runs server-side over a snapshot of the cache, so results are
  consistent within one call but may differ between calls as TTLs expire.

EXAMPLES
  # raw 1-hop neighbourhood, top-10 by weight
  lantern illuminate alice

  # 2-hop reachability re-ranked by TF-IDF, top-5 per node
  lantern illuminate alice --step 2 --k 5 --weighting tfidf

  # 3-hop MST rooted at alice (smallest-weight connecting tree)
  lantern illuminate alice --step 3 --k 20 --algorithm mst --objective min

  # 3-hop relevance-weighted SPT (formerly the "inverse-SPT" enum value)
  lantern illuminate alice --step 3 --k 20 --algorithm spt --objective max

  # 2-hop walk restricted to the users/ keyspace (seed always kept)
  lantern illuminate alice --step 2 --k 5 --prefix users/
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		algo, ok := algorithmByName[illuminateAlgorithmStr]
		if !ok {
			return fmt.Errorf("unknown --algorithm %q (want none|mst|spt)", illuminateAlgorithmStr)
		}
		obj, ok := objectiveByName[illuminateObjectiveStr]
		if !ok {
			return fmt.Errorf("unknown --objective %q (want min|max)", illuminateObjectiveStr)
		}
		w, ok := weightingByName[illuminateWeightingStr]
		if !ok {
			return fmt.Errorf("unknown --weighting %q (want raw|tfidf)", illuminateWeightingStr)
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		opts := []client.IlluminateOption{
			client.WithStep(illuminateStep),
			client.WithK(illuminateK),
			client.WithAlgorithm(algo),
			client.WithObjective(obj),
			client.WithWeighting(w),
		}
		if illuminatePrefixStr != "" {
			opts = append(opts, client.WithVertexPrefix(illuminatePrefixStr))
		}
		g, err := cli.Illuminate(cmd.Context(), args[0], opts...)
		if err != nil {
			return err
		}
		enc := json.NewEncoder(cmd.OutOrStdout())
		enc.SetIndent("", "  ")
		return enc.Encode(g)
	},
}

func init() {
	illuminateCmd.Flags().Uint32Var(&illuminateStep, "step", 1, "maximum walk depth from the seed")
	illuminateCmd.Flags().Uint32Var(&illuminateK, "k", 10, "max neighbours visited per node")
	illuminateCmd.Flags().StringVar(&illuminateAlgorithmStr, "algorithm", "none", "post-traversal reduction: none|mst|spt (#410)")
	illuminateCmd.Flags().StringVar(&illuminateObjectiveStr, "objective", "max", "optimisation direction: min|max; governs per-hop top-k pruning AND reduction (#560)")
	illuminateCmd.Flags().StringVar(&illuminateWeightingStr, "weighting", "raw", "edge-weight transform before walk: raw|tfidf (#410)")
	illuminateCmd.Flags().StringVar(&illuminatePrefixStr, "prefix", "", "restrict walk frontier to vertices with this key prefix; seed always kept, empty = no filter (#604)")
	rootCmd.AddCommand(illuminateCmd)
}
