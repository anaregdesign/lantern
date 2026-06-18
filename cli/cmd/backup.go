package cmd

import (
	"fmt"
	"io"
	"os"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	dumpFormat    string
	dumpPrefix    string
	restoreFormat string
)

// parseBackupFormat maps the --format flag onto a client.Format.
func parseBackupFormat(s string) (client.Format, error) {
	switch s {
	case "proto", "":
		return client.FormatProto, nil
	case "ndjson":
		return client.FormatNDJSON, nil
	default:
		return 0, fmt.Errorf("unknown --format %q (want proto|ndjson)", s)
	}
}

// nopWriteCloser adapts an io.Writer (os.Stdout) to io.WriteCloser so dump
// can treat a file and stdout uniformly without closing stdout.
type nopWriteCloser struct{ io.Writer }

func (nopWriteCloser) Close() error { return nil }

// openOutput returns the destination for dump: stdout for "-"/"" else a file.
func openOutput(path string) (io.WriteCloser, error) {
	if path == "-" || path == "" {
		return nopWriteCloser{os.Stdout}, nil
	}
	return os.Create(path)
}

var dumpCmd = &cobra.Command{
	Use:   "dump <file|->",
	Short: "Dump the whole graph to a file (backup)",
	Long: `Stream a whole-graph, point-in-time backup to a file or stdout.

The dump is a single consistent snapshot — every live vertex and edge taken
under one server-side lock. Reload it with ` + "`lantern-cli restore`" + `.

The destination is "-" or omitted for stdout, else a filesystem path. The
default --format is length-delimited protobuf ("proto"); --format ndjson
writes one human-readable JSON object per line. --prefix restricts the dump
to the induced subgraph over vertices whose key has that prefix (an edge is
kept only when both endpoints match).

A summary ("dumped <V> vertices, <E> edges") is printed to stderr, so stdout
can carry the binary protobuf stream.

EXAMPLES
  lantern-cli dump graph.lbk
  lantern-cli dump - > graph.lbk
  lantern-cli dump graph.ndjson --format ndjson
  lantern-cli dump users.lbk --prefix user:
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := parseBackupFormat(dumpFormat)
		if err != nil {
			return err
		}
		w, err := openOutput(args[0])
		if err != nil {
			return err
		}

		cli, err := dial()
		if err != nil {
			_ = w.Close()
			return err
		}
		defer func() { _ = cli.Close() }()

		stats, err := cli.Backup(cmd.Context(), w, client.WithBackupFormat(format), client.WithBackupPrefix(dumpPrefix))
		// Close the destination before reporting so a write/flush error on
		// the final Close surfaces instead of a spurious success.
		if cerr := w.Close(); err == nil {
			err = cerr
		}
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "dumped %d vertices, %d edges\n", stats.Vertices, stats.Edges)
		return nil
	},
}

var restoreCmd = &cobra.Command{
	Use:   "restore <file|->",
	Short: "Restore a graph from a dump file (load)",
	Long: `Load a backup produced by ` + "`lantern-cli dump`" + ` back into Lantern.

The source is "-" or omitted for stdin, else a filesystem path. --format must
match the dump's format (default "proto"). Records are replayed via
PutVertices / PutEdges in batches of --chunk-size; Put is idempotent, so
re-running a restore is safe.

A malformed record aborts with exit code 1; already-sent batches are NOT
rolled back (Lantern has no transactions). "OK <total>" is printed to stdout
when the load finishes.

EXAMPLES
  lantern-cli restore graph.lbk
  cat graph.lbk | lantern-cli restore -
  lantern-cli restore graph.ndjson --format ndjson
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		format, err := parseBackupFormat(restoreFormat)
		if err != nil {
			return err
		}
		r, err := openInput(args[0])
		if err != nil {
			return err
		}
		defer func() { _ = r.Close() }()

		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		stats, err := cli.Restore(cmd.Context(), r, client.WithRestoreFormat(format), client.WithRestoreChunkSize(flagChunkSize))
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", stats.Vertices+stats.Edges)
		return nil
	},
}

func init() {
	dumpCmd.Flags().StringVar(&dumpFormat, "format", "proto", `output format: "proto" or "ndjson"`)
	dumpCmd.Flags().StringVar(&dumpPrefix, "prefix", "", "restrict to vertices with this key prefix (empty = whole graph)")
	restoreCmd.Flags().StringVar(&restoreFormat, "format", "proto", `input format: "proto" or "ndjson"`)
	rootCmd.AddCommand(dumpCmd, restoreCmd)
}
