package cmd

import (
	"fmt"

	"github.com/anaregdesign/lantern/cli/parser"
	"github.com/spf13/cobra"
)

// scopedHelpText gives Cobra the same structured topic rendering the REPL
// prints for `help <topic>`. Cobra adds each command's flag table after this
// text, retaining normal flag discoverability without duplicating signatures,
// defaults, domains, meaning, or examples.
func scopedHelpText(topic string) string {
	text, ok := parser.HelpTextFor(topic)
	if !ok {
		panic("unknown scoped help topic: " + topic)
	}
	return text
}

// helpCmd restores Cobra's conventional `lantern-cli help <command>` entry
// point. It delegates to the target command's Help method, so structured topic
// text and Cobra's generated flag table are both retained.
var helpCmd = &cobra.Command{
	Use:   "help [command]",
	Short: "Help about any command",
	Args:  cobra.MaximumNArgs(1),
	RunE: func(_ *cobra.Command, args []string) error {
		if len(args) == 0 {
			return rootCmd.Help()
		}
		target, _, err := rootCmd.Find(args)
		if err != nil || target == rootCmd || target.Name() == "help" {
			return fmt.Errorf("unknown command %q (try: search, bfs, pagerank, community, or --help)", args[0])
		}
		return target.Help()
	},
}

func init() {
	rootCmd.AddCommand(helpCmd)
}
