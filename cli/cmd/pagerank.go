package cmd

import (
	"fmt"

	"github.com/anaregdesign/lantern/cli/service"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	pagerankTopN         uint32
	pagerankRestartProb  float32
	pagerankEpsilon      float32
	pagerankWeightingStr string
	pagerankPrefixStr    string
)

var pagerankCmd = &cobra.Command{
	Use:   "pagerank <seed>",
	Short: "Personalized PageRank relevance star from <seed>",
	Long: `Rank vertices by their Personalized PageRank relevance to <seed> and emit the
resulting relevance star (each seed→v edge carries v's PPR mass) as JSON.

PARAMETERS (positional or flag)
  top_n   <uint32>   cap the star to the top-N vertices by mass
                     (default 10; 0 = every positive-mass vertex)

FLAGS
  --top-n <uint32>       cap to the top-N by PPR mass (default 10; 0 = all)
  --restart-prob <f>     restart (teleport-to-seed) probability α in (0,1).
                         Higher α keeps relevance tighter around the seed.
                         0 (default) defers to the server default 0.15.
  --epsilon <f>          forward-push residual threshold ε (> 0). Smaller ε
                         reaches more vertices (higher recall, more work).
                         0 (default) defers to the server default 1e-4.
  --weighting <mode>     edge-weight transform before the walk: raw|tfidf|bm25
                         (default raw)
  --prefix <string>      restrict the walk frontier to vertices with this key
                         prefix; the seed is always kept. Empty = no filter.

Personalized PageRank returns a relevance star, which is already a tree, so
pagerank has NO reduction or objective knob (unlike bfs / community).

GRAMMAR PARITY (#672, #975)
    lantern-cli pagerank <seed> [top_n] [restart_prob=<float>] [epsilon=<float>] \
            [weighting=raw|tfidf|bm25] [prefix=<string>]

  A bare "lantern-cli pagerank alice" uses the flag defaults (top_n=10; α/ε
  resolved by the server).

EXAMPLES
  lantern-cli pagerank alice
  lantern-cli pagerank alice --top-n 15 --restart-prob 0.25
  lantern-cli pagerank alice 15 restart_prob=0.25 epsilon=0.001
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rejectMixedFamilyGrammar(cmd, args); err != nil {
			return err
		}
		if len(args) > 1 {
			return forwardFamilyPositional(cmd, "pagerank", args)
		}
		if err := validateExplicitRestartProbFlag(cmd, "restart-prob", pagerankRestartProb); err != nil {
			return err
		}
		if err := validateExplicitPositiveFloat32Flag(cmd, "epsilon", pagerankEpsilon); err != nil {
			return err
		}
		w, ok := weightingByName[pagerankWeightingStr]
		if !ok {
			return fmt.Errorf("unknown --weighting %q (want raw|tfidf|bm25)", pagerankWeightingStr)
		}
		opts := []client.IlluminateOption{
			service.PagerankOption(pagerankTopN, pagerankRestartProb, pagerankEpsilon),
			client.WithWeighting(w),
		}
		if pagerankPrefixStr != "" {
			opts = append(opts, client.WithVertexPrefix(pagerankPrefixStr))
		}
		return runFamilyFlagPath(cmd, args[0], opts)
	},
}

func init() {
	pagerankCmd.Flags().Uint32Var(&pagerankTopN, "top-n", 10, "cap the star to the top-N vertices by PPR mass (0 = all)")
	pagerankCmd.Flags().Float32Var(&pagerankRestartProb, "restart-prob", 0, "restart prob α in (0,1); 0 = server default 0.15")
	pagerankCmd.Flags().Float32Var(&pagerankEpsilon, "epsilon", 0, "residual threshold ε > 0; 0 = server default 1e-4")
	pagerankCmd.Flags().StringVar(&pagerankWeightingStr, "weighting", "raw", "edge-weight transform before walk: raw|tfidf|bm25")
	pagerankCmd.Flags().StringVar(&pagerankPrefixStr, "prefix", "", "restrict walk frontier to vertices with this key prefix; seed always kept")
	rootCmd.AddCommand(pagerankCmd)
}
