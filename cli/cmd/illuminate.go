package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	illuminateStep   uint32
	illuminateK      uint32
	illuminateTfidf  bool
	illuminateOptStr string
)

// optimizationByName maps human-friendly --optimize values to the server enum.
var optimizationByName = map[string]client.Optimization{
	"none":             client.OptimizationUnspecified,
	"":                 client.OptimizationUnspecified,
	"mst":              client.OptimizationMinimumSpanningTree,
	"max-st":           client.OptimizationMaximumSpanningTree,
	"maximum":          client.OptimizationMaximumSpanningTree,
	"spt":              client.OptimizationShortestPathTree,
	"shortest":         client.OptimizationShortestPathTree,
	"inverse-spt":      client.OptimizationShortestPathTreeInverse,
	"shortest-inverse": client.OptimizationShortestPathTreeInverse,
}

var illuminateCmd = &cobra.Command{
	Use:   "illuminate <seed>",
	Short: "Walk the graph from <seed> and return the visited subgraph",
	Long: `Run a bounded breadth-first walk from <seed> and emit the visited subgraph
as JSON.

PARAMETERS
  --step <uint32>   maximum walk depth from the seed (default 1)
  --k <uint32>      max neighbours visited per node (default 10)
  --tfidf           re-rank neighbours by TF-IDF over edge weights before
                    truncating to top-k (default false)

OPTIMIZATION (server-side post-processing)
  --optimize <mode> apply a graph operator to the discovered subgraph before
                    returning it:

    none          (default) return the raw discovered subgraph
    mst           minimum spanning tree
    max-st        maximum spanning tree
    spt           shortest path tree from seed (edge weight = cost)
    inverse-spt   shortest path tree from seed using 1/weight as cost
                  (use this when weight encodes RELEVANCE, not cost)

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

  # 2-hop reachability ranked by TF-IDF, top-5 per node
  lantern illuminate alice --step 2 --k 5 --tfidf

  # 3-hop MST rooted at alice (smallest-weight connecting tree)
  lantern illuminate alice --step 3 --k 20 --optimize mst

  # 3-hop relevance-weighted shortest path tree
  lantern illuminate alice --step 3 --k 20 --optimize inverse-spt
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		opt, ok := optimizationByName[illuminateOptStr]
		if !ok {
			return fmt.Errorf("unknown --optimize %q (want none|mst|max-st|spt|inverse-spt)", illuminateOptStr)
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		g, err := cli.Illuminate(
			cmd.Context(), args[0],
			client.WithStep(illuminateStep),
			client.WithK(illuminateK),
			client.WithTFIDF(illuminateTfidf),
			client.WithOptimization(opt),
		)
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
	illuminateCmd.Flags().BoolVar(&illuminateTfidf, "tfidf", false, "re-rank neighbours by TF-IDF over edge weights before truncating")
	illuminateCmd.Flags().StringVar(&illuminateOptStr, "optimize", "none", "server-side post-processing: none|mst|max-st|spt|inverse-spt")
	rootCmd.AddCommand(illuminateCmd)
}
