package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"

	"github.com/anaregdesign/lantern/cli/service"
	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// objectiveByName / reductionByName / weightingByName map the human-friendly
// flag values shared by the family subcommands (bfs / pagerank / community) to
// the SDK enums. The traversal family is the verb itself, so there is no
// algorithm flag (#975) — reworked from the former single illuminate command.
var objectiveByName = map[string]client.Objective{
	"":         client.ObjectiveUnspecified,
	"min":      client.ObjectiveMinimize,
	"minimize": client.ObjectiveMinimize,
	"max":      client.ObjectiveMaximize,
	"maximize": client.ObjectiveMaximize,
}

var reductionByName = map[string]client.Reduction{
	"":     client.ReductionNone,
	"none": client.ReductionNone,
	"mst":  client.ReductionMinimumSpanningTree,
	"spt":  client.ReductionShortestPathTree,
}

var weightingByName = map[string]client.Weighting{
	"":      client.WeightingUnspecified,
	"raw":   client.WeightingRaw,
	"tfidf": client.WeightingTFIDF,
	"bm25":  client.WeightingBM25,
}

// forwardFamilyPositional dials the server and forwards the family verb plus its
// positional/keyword argv through the shared REPL dispatcher, so
// `lantern-cli bfs alice 2 5 reduction=mst` behaves exactly like typing it at
// the `lantern-cli repl` prompt (#672, #975). Used whenever more than the bare
// <seed> is supplied.
func forwardFamilyPositional(cmd *cobra.Command, verb string, args []string) error {
	cli, err := dial()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	return service.NewCLIService(cli).RunArgs(cmd.Context(), append([]string{verb}, args...))
}

func rejectMixedFamilyGrammar(cmd *cobra.Command, args []string) error {
	// Persistent connection flags (--address, --timeout, …) apply to both
	// grammar forms. Only a family command's own flags conflict with its
	// positional arguments.
	var hasChangedFamilyFlag bool
	cmd.Flags().Visit(func(flag *pflag.Flag) {
		if cmd.InheritedFlags().Lookup(flag.Name) == nil {
			hasChangedFamilyFlag = true
		}
	})
	if len(args) > 1 && hasChangedFamilyFlag {
		return errors.New("cannot mix flags with the positional grammar; use one or the other")
	}
	return nil
}

func validatePositiveUint32Flag(name string, value uint32) error {
	if value == 0 {
		return fmt.Errorf("--%s must be a positive integer", name)
	}
	return nil
}

func validateExplicitRestartProbFlag(cmd *cobra.Command, name string, value float32) error {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	f := float64(value)
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 || f >= 1 {
		return fmt.Errorf("--%s must be a float in (0,1)", name)
	}
	return nil
}

func validateExplicitPositiveFloat32Flag(cmd *cobra.Command, name string, value float32) error {
	if !cmd.Flags().Changed(name) {
		return nil
	}
	f := float64(value)
	if math.IsNaN(f) || math.IsInf(f, 0) || f <= 0 {
		return fmt.Errorf("--%s must be a positive float", name)
	}
	return nil
}

// runFamilyFlagPath executes a flag-driven family walk (the bare-<seed> form
// that supplies each family's flag defaults) and prints the visited subgraph as
// indented JSON. Shared by the three family subcommands.
func runFamilyFlagPath(cmd *cobra.Command, seed string, opts []client.IlluminateOption) error {
	cli, err := dial()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	g, err := cli.Illuminate(cmd.Context(), seed, opts...)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(cmd.OutOrStdout())
	enc.SetIndent("", "  ")
	return enc.Encode(g)
}
