package cmd

import (
	"fmt"

	"github.com/anaregdesign/lantern/cli/service"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	bfsStep         uint32
	bfsFanOut       uint32
	bfsReductionStr string
	bfsObjectiveStr string
	bfsWeightingStr string
	bfsPrefixStr    string
)

var bfsCmd = &cobra.Command{
	Use:   "bfs <seed>",
	Short: "Greedy per-hop top-k breadth-first walk from <seed>",
	Long:  familyHelpText("bfs"),
	Args:  cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := rejectMixedFamilyGrammar(cmd, args); err != nil {
			return err
		}
		if len(args) > 1 {
			return forwardFamilyPositional(cmd, "bfs", args)
		}
		if err := validatePositiveUint32Flag("step", bfsStep); err != nil {
			return err
		}
		if err := validatePositiveUint32Flag("fan-out", bfsFanOut); err != nil {
			return err
		}
		obj, ok := objectiveByName[bfsObjectiveStr]
		if !ok {
			return fmt.Errorf("unknown --objective %q (want min|max)", bfsObjectiveStr)
		}
		red, ok := reductionByName[bfsReductionStr]
		if !ok {
			return fmt.Errorf("unknown --reduction %q (want none|mst|spt)", bfsReductionStr)
		}
		w, ok := weightingByName[bfsWeightingStr]
		if !ok {
			return fmt.Errorf("unknown --weighting %q (want raw|tfidf|bm25)", bfsWeightingStr)
		}
		opts := []client.IlluminateOption{
			service.BfsOption(bfsStep, bfsFanOut, red, obj),
			client.WithWeighting(w),
		}
		if bfsPrefixStr != "" {
			opts = append(opts, client.WithVertexPrefix(bfsPrefixStr))
		}
		return runFamilyFlagPath(cmd, args[0], opts)
	},
}

func init() {
	bfsCmd.Flags().Uint32Var(&bfsStep, "step", 5, "walk depth (hops) from the seed")
	bfsCmd.Flags().Uint32Var(&bfsFanOut, "fan-out", 3, "max neighbours visited per hop (top-k prune)")
	bfsCmd.Flags().StringVar(&bfsReductionStr, "reduction", "none", "post-traversal tree view: none|mst|spt")
	bfsCmd.Flags().StringVar(&bfsObjectiveStr, "objective", "max", "optimisation direction: min|max (per-hop prune + reduction)")
	bfsCmd.Flags().StringVar(&bfsWeightingStr, "weighting", "raw", "edge-weight transform before walk: raw|tfidf|bm25")
	bfsCmd.Flags().StringVar(&bfsPrefixStr, "prefix", "", "restrict walk frontier to vertices with this key prefix; seed always kept")
	rootCmd.AddCommand(bfsCmd)
}
