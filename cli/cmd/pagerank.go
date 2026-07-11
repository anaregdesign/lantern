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
	Long:  familyHelpText("pagerank"),
	Args:  cobra.MinimumNArgs(1),
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
