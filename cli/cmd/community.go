package cmd

import (
	"fmt"

	"github.com/anaregdesign/lantern/cli/service"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	communityMaxSize      uint32
	communityRestartProb  float32
	communityEpsilon      float32
	communityReductionStr string
	communityObjectiveStr string
	communityWeightingStr string
	communityPrefixStr    string
)

var communityCmd = &cobra.Command{
	Use:   "community <seed>",
	Short: "Conductance-optimal local community around <seed>",
	Long:  scopedHelpText("community"),
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rejectMixedFamilyGrammar(cmd, args); err != nil {
			return err
		}
		if len(args) > 1 {
			return forwardFamilyPositional(cmd, "community", args)
		}
		if err := validateExplicitRestartProbFlag(cmd, "restart-prob", communityRestartProb); err != nil {
			return err
		}
		if err := validateExplicitPositiveFloat32Flag(cmd, "epsilon", communityEpsilon); err != nil {
			return err
		}
		obj, ok := objectiveByName[communityObjectiveStr]
		if !ok {
			return fmt.Errorf("unknown --objective %q (want min|max)", communityObjectiveStr)
		}
		red, ok := reductionByName[communityReductionStr]
		if !ok {
			return fmt.Errorf("unknown --reduction %q (want none|mst|spt)", communityReductionStr)
		}
		w, ok := weightingByName[communityWeightingStr]
		if !ok {
			return fmt.Errorf("unknown --weighting %q (want raw|tfidf|bm25)", communityWeightingStr)
		}
		opts := []client.IlluminateOption{
			service.CommunityOption(communityMaxSize, red, obj, communityRestartProb, communityEpsilon),
			client.WithWeighting(w),
		}
		if communityPrefixStr != "" {
			opts = append(opts, client.WithVertexPrefix(communityPrefixStr))
		}
		return runFamilyFlagPath(cmd, args[0], opts)
	},
}

func init() {
	communityCmd.Flags().Uint32Var(&communityMaxSize, "max-size", 0, "community size upper bound (0 = the conductance sweep decides)")
	communityCmd.Flags().Float32Var(&communityRestartProb, "restart-prob", 0, "restart prob α in (0,1); 0 = server default 0.15")
	communityCmd.Flags().Float32Var(&communityEpsilon, "epsilon", 0, "residual threshold ε > 0; 0 = server default 1e-4")
	communityCmd.Flags().StringVar(&communityReductionStr, "reduction", "none", "tree view of the community: none|mst|spt")
	communityCmd.Flags().StringVar(&communityObjectiveStr, "objective", "max", "reduction direction: min|max (ignored when reduction=none)")
	communityCmd.Flags().StringVar(&communityWeightingStr, "weighting", "raw", "edge-weight transform: raw|tfidf|bm25")
	communityCmd.Flags().StringVar(&communityPrefixStr, "prefix", "", "restrict frontier to vertices with this key prefix; seed always kept")
	rootCmd.AddCommand(communityCmd)
}
