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
	Long: `Run a bounded breadth-first walk from <seed> and emit the visited subgraph
as JSON.

PARAMETERS (positional or flag; a bare positional fills step then fan_out)
  step      <uint32>   walk depth (hops) from the seed        (default 5)
  fan_out   <uint32>   max neighbours visited per hop (top-k) (default 3)

FLAGS
  --step <uint32>       walk depth from the seed (default 5)
  --fan-out <uint32>    per-hop top-k prune (default 3)
  --reduction <mode>    post-traversal tree view: none|mst|spt (default none)
  --objective <dir>     min|max — steers per-hop top-k pruning AND the reduction
                        direction (default max: strongest edges win)
  --weighting <mode>    edge-weight transform before the walk: raw|tfidf|bm25
                        (default raw)
  --prefix <string>     restrict the walk frontier to vertices with this key
                        prefix; the seed is always kept as the anchor. Empty
                        (default) = no filter (case-sensitive).

GRAMMAR PARITY (#672, #975)
  bfs also accepts the REPL positional grammar so the prompt and the shell share
  one grammar:

    lantern-cli bfs <seed> [step] [fan_out] [reduction=none|mst|spt] \
            [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]

  The positional form kicks in whenever more than the bare <seed> is supplied;
  a bare "lantern-cli bfs alice" uses the flag defaults (step=5, fan_out=3).

EXAMPLES
  lantern-cli bfs alice                       # depth-5, per-hop top-3
  lantern-cli bfs alice --step 2 --fan-out 5
  lantern-cli bfs alice 3 20 reduction=mst objective=min
  lantern-cli bfs alice --step 2 --fan-out 5 --weighting tfidf
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) > 1 {
			return forwardFamilyPositional(cmd, "bfs", args)
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
