package cmd

import (
	"encoding/json"
	"fmt"

	"github.com/anaregdesign/lantern/cli/service"
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
	illuminateRestartProb  float32
	illuminateEpsilon      float32
)

// objectiveByName, weightingByName map human-friendly flag values to the
// SDK enums. The algorithm flag is translated by
// service.IlluminateFamilyOption into the typed per-family option (#846).
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
	"bm25":  client.WeightingBM25,
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
  --algorithm <mode>    Illuminate algorithm:
                          none  (default) return the raw discovered subgraph
                          mst   minimum or maximum spanning tree
                          spt   shortest-path tree rooted at the seed
                          ppr   Personalized PageRank from the seed (#801):
                                a distinct traversal (NOT a reduction) that
                                returns a relevance star (seed→v = v's PPR
                                mass), capped at the top --k vertices; tuned
                                by --restart-prob and --epsilon
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
                          bm25  re-score using Okapi BM25 (k1=1.2, b=0.75)
                                over the same distribution; adds IDF
                                saturation + out-degree length-normalisation

PERSONALIZED PAGERANK KNOBS (#801, --algorithm ppr only)
  --restart-prob <f>    restart (teleport-to-seed) probability α in (0,1).
                        Higher α keeps relevance tighter around the seed;
                        lower α wanders farther. 0 (default) = server's 0.15.
  --epsilon <f>         forward-push residual threshold ε (> 0). Smaller ε
                        reaches more vertices (higher recall, more work);
                        larger ε stops sooner. 0 (default) = server's 1e-4.

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
  lantern-cli illuminate alice

  # 2-hop reachability re-ranked by TF-IDF, top-5 per node
  lantern-cli illuminate alice --step 2 --k 5 --weighting tfidf

  # 2-hop reachability re-ranked by Okapi BM25, top-5 per node
  lantern-cli illuminate alice --step 2 --k 5 --weighting bm25

  # 3-hop MST rooted at alice (smallest-weight connecting tree)
  lantern-cli illuminate alice --step 3 --k 20 --algorithm mst --objective min

  # 3-hop relevance-weighted SPT (formerly the "inverse-SPT" enum value)
  lantern-cli illuminate alice --step 3 --k 20 --algorithm spt --objective max

  # Personalized PageRank from alice, top-15 by PPR mass, tighter locality
  lantern-cli illuminate alice --k 15 --algorithm ppr --restart-prob 0.25

  # 2-hop walk restricted to the users/ keyspace (seed always kept)
  lantern-cli illuminate alice --step 2 --k 5 --prefix users/

REPL GRAMMAR (one-liner parity, #672)
  In addition to the flags above, illuminate accepts the REPL's positional
  form so the prompt and the shell share one grammar:

    lantern-cli illuminate <seed> <step> <k> [algorithm=none|mst|spt|ppr|community] \
            [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>] \
            [restart_prob=<float>] [epsilon=<float>]

  e.g. "lantern-cli illuminate alice 2 5 algorithm=spt objective=max" is
  identical to typing it at "lantern-cli repl". The positional form kicks in
  whenever more than the bare <seed> is supplied; a bare "lantern
  illuminate alice" uses the flag defaults (step=1, k=10).
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		// REPL positional grammar: `illuminate <seed> <step> <k> [key=value …]`.
		// When more than the bare seed is present, forward argv to the shared
		// REPL dispatcher so `lantern-cli illuminate alice 2 5 algorithm=spt`
		// behaves exactly like the prompt (#672). A bare seed keeps the
		// flag-driven path below, which is the only form that supplies the
		// step/k defaults.
		if len(args) > 1 {
			cli, err := dial()
			if err != nil {
				return err
			}
			defer func() { _ = cli.Close() }()
			return service.NewCLIService(cli).RunArgs(cmd.Context(), append([]string{"illuminate"}, args...))
		}

		obj, ok := objectiveByName[illuminateObjectiveStr]
		if !ok {
			return fmt.Errorf("unknown --objective %q (want min|max)", illuminateObjectiveStr)
		}
		w, ok := weightingByName[illuminateWeightingStr]
		if !ok {
			return fmt.Errorf("unknown --weighting %q (want raw|tfidf|bm25)", illuminateWeightingStr)
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		famOpt, ok := service.IlluminateFamilyOption(illuminateAlgorithmStr, illuminateStep, illuminateK, obj, illuminateRestartProb, illuminateEpsilon)
		if !ok {
			return fmt.Errorf("unknown --algorithm %q (want none|mst|spt|ppr|community)", illuminateAlgorithmStr)
		}
		opts := []client.IlluminateOption{
			famOpt,
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
	illuminateCmd.Flags().StringVar(&illuminateAlgorithmStr, "algorithm", "none", "Illuminate algorithm: none|mst|spt|ppr|community (#410, #801, #845)")
	illuminateCmd.Flags().StringVar(&illuminateObjectiveStr, "objective", "max", "optimisation direction: min|max; governs per-hop top-k pruning AND reduction (#560)")
	illuminateCmd.Flags().StringVar(&illuminateWeightingStr, "weighting", "raw", "edge-weight transform before walk: raw|tfidf|bm25 (#410, #800)")
	illuminateCmd.Flags().StringVar(&illuminatePrefixStr, "prefix", "", "restrict walk frontier to vertices with this key prefix; seed always kept, empty = no filter (#604)")
	illuminateCmd.Flags().Float32Var(&illuminateRestartProb, "restart-prob", 0, "Personalized PageRank restart prob α in (0,1); --algorithm ppr only, 0 = server default 0.15 (#801)")
	illuminateCmd.Flags().Float32Var(&illuminateEpsilon, "epsilon", 0, "Personalized PageRank residual threshold ε > 0; --algorithm ppr only, 0 = server default 1e-4 (#801)")
	rootCmd.AddCommand(illuminateCmd)
}
