package cmd

import (
	"fmt"
	"runtime/debug"

	"github.com/spf13/cobra"
)

// version is overridden at build time via -ldflags "-X .../cli/cmd.version=..."
// and falls back to the embedded module version when unset.
var version = ""

var versionCmd = &cobra.Command{
	Use:   "version",
	Short: "Print client version",
	Long: `Print the CLI client version.

When built with ` + "`-ldflags '-X github.com/anaregdesign/lantern/cli/cmd.version=vX.Y.Z'`" + `
the injected string is printed. Otherwise the version embedded by the Go
toolchain (debug.ReadBuildInfo) is used, which for ` + "`go install`" + ` builds at a
tagged commit is the tag string and for plain ` + "`go build`" + ` is "(devel)".`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, args []string) error {
		v := version
		if v == "" {
			if info, ok := debug.ReadBuildInfo(); ok {
				v = info.Main.Version
			}
		}
		if v == "" {
			v = "(unknown)"
		}
		fmt.Fprintln(cmd.OutOrStdout(), v)
		return nil
	},
}

func init() {
	rootCmd.AddCommand(versionCmd)
}
