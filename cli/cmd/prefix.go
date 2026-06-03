package cmd

import (
	"encoding/json"
	"fmt"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

// -- vertex scan ------------------------------------------------------------

var (
	vertexScanLimit  uint32
	vertexScanCursor string
	vertexScanAll    bool
)

var vertexScanCmd = &cobra.Command{
	Use:   "scan <prefix>",
	Short: "Enumerate vertices whose key begins with <prefix>",
	Long: `Walk the prefix index and emit each live vertex as one NDJSON line on
stdout (same per-vertex schema as "vertex get").

PAGINATION
  Without --all the server returns at most --limit vertices and an opaque
  next-cursor token. If a next page exists, the token is printed to stderr
  as "next-cursor: <token>"; re-run with "--cursor <token>" to resume.

  With --all the CLI iterates every page through the SDK's iter.Seq2
  helper and streams vertices as they arrive — output is bounded only by
  how many vertices match. Use --limit to tune the per-page request size.

CURSOR
  Cursors are opaque base64 tokens managed by the server; do not hand-craft
  them. They are scoped to a single (prefix, server version) pair.

OUTPUT
  One JSON object per line on stdout (NDJSON), safe for piping into jq or
  xargs. The cursor (if any, single-page mode only) goes to stderr.

EXAMPLES
  lantern vertex scan users/
  lantern vertex scan users/ --limit 100 | jq -r .key
  lantern vertex scan users/ --all > snapshot.ndjson
  lantern vertex scan users/ --limit 50                    # then read stderr
  lantern vertex scan users/ --limit 50 --cursor "<token>" # resume
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		prefix := args[0]
		out := cmd.OutOrStdout()
		errw := cmd.ErrOrStderr()
		enc := json.NewEncoder(out)

		if vertexScanAll {
			for batch, err := range cli.ScanVerticesAll(cmd.Context(), prefix, vertexScanLimit) {
				if err != nil {
					return err
				}
				for _, v := range batch {
					if err := enc.Encode(v); err != nil {
						return err
					}
				}
			}
			return nil
		}

		opts := []client.ScanOption{}
		if vertexScanLimit > 0 {
			opts = append(opts, client.WithScanLimit(vertexScanLimit))
		}
		if vertexScanCursor != "" {
			opts = append(opts, client.WithScanCursor([]byte(vertexScanCursor)))
		}
		vs, next, err := cli.ScanVertices(cmd.Context(), prefix, opts...)
		if err != nil {
			return err
		}
		for _, v := range vs {
			if err := enc.Encode(v); err != nil {
				return err
			}
		}
		if len(next) > 0 {
			_, _ = fmt.Fprintf(errw, "next-cursor: %s\n", string(next))
		}
		return nil
	},
}

// -- vertex count -----------------------------------------------------------

var vertexCountCmd = &cobra.Command{
	Use:   "count <prefix>",
	Short: "Count vertices whose key begins with <prefix>",
	Long: `Return the number of keys in the prefix index for <prefix>.

CAVEAT
  This count is taken from the radix index and is NOT cross-checked
  against per-vertex liveness. A vertex that has expired but not yet been
  reaped by the GC loop is still counted here even though "vertex get"
  and "vertex scan" will skip it. Use "vertex scan --all | wc -l" if you
  need the strictly-live count.

OUTPUT
  Single decimal integer on stdout, no newline-padded JSON.

EXAMPLES
  lantern vertex count users/
  lantern vertex count orders/2025/
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()
		n, err := cli.CountVerticesByPrefix(cmd.Context(), args[0])
		if err != nil {
			return err
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%d\n", n)
		return nil
	},
}

// -- vertex delete-prefix ---------------------------------------------------

var (
	vertexDeletePrefixLimit  uint32
	vertexDeletePrefixDryRun bool
	vertexDeletePrefixYes    bool
)

// errPrefixDeleteUnconfirmed is returned by "vertex delete-prefix" when
// neither --dry-run nor --yes was supplied. Surfaced separately so tests
// can assert the safety gate without depending on the human-readable text.
var errPrefixDeleteUnconfirmed = fmt.Errorf("refusing to delete by prefix without --dry-run or --yes")

// prefixDeleteConfirmed reports whether the supplied flag combination is
// enough to authorise a destructive prefix delete. Either --dry-run (which
// mutates nothing) or --yes (explicit confirmation) is sufficient.
func prefixDeleteConfirmed(dryRun, yes bool) bool { return dryRun || yes }

var vertexDeletePrefixCmd = &cobra.Command{
	Use:   "delete-prefix <prefix>",
	Short: "Bulk-delete vertices whose key begins with <prefix> (DESTRUCTIVE)",
	Long: `Remove every live vertex whose key starts with <prefix>, up to --limit
matches per call. Edges incident to deleted vertices are NOT eagerly
removed — they are reaped lazily with their own TTL.

SAFETY GATE
  Running without either --dry-run or --yes is REFUSED. The command will
  instead print the current count from "vertex count" and the two
  recommended next steps, then exit non-zero. This is intentional: bulk
  delete is irreversible and trivially typo-able.

  --dry-run  count what would be deleted; mutate nothing.
  --yes      perform the delete.

OUTPUT
  Dry-run: "would delete <n>".
  Real:    "deleted <n>".

EXAMPLES
  lantern vertex delete-prefix tmp/                         # refused, prints suggestion
  lantern vertex delete-prefix tmp/ --dry-run
  lantern vertex delete-prefix tmp/ --yes
  lantern vertex delete-prefix tmp/ --yes --limit 500       # cap per-call
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prefix := args[0]
		if !prefixDeleteConfirmed(vertexDeletePrefixDryRun, vertexDeletePrefixYes) {
			cli, err := dial()
			if err != nil {
				return err
			}
			defer func() { _ = cli.Close() }()
			n, cerr := cli.CountVerticesByPrefix(cmd.Context(), prefix)
			errw := cmd.ErrOrStderr()
			if cerr == nil {
				_, _ = fmt.Fprintf(errw, "prefix %q matches %d vertices\n", prefix, n)
			}
			_, _ = fmt.Fprintf(errw, "  preview: lantern vertex delete-prefix %q --dry-run\n", prefix)
			_, _ = fmt.Fprintf(errw, "  execute: lantern vertex delete-prefix %q --yes\n", prefix)
			return errPrefixDeleteUnconfirmed
		}

		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		opts := []client.DeleteByPrefixOption{}
		if vertexDeletePrefixLimit > 0 {
			opts = append(opts, client.WithDeleteByPrefixLimit(vertexDeletePrefixLimit))
		}
		if vertexDeletePrefixDryRun {
			opts = append(opts, client.WithDryRun())
		}
		n, err := cli.DeleteVerticesByPrefix(cmd.Context(), prefix, opts...)
		if err != nil {
			return err
		}
		verb := "deleted"
		if vertexDeletePrefixDryRun {
			verb = "would delete"
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "%s %d\n", verb, n)
		return nil
	},
}

func init() {
	vertexScanCmd.Flags().Uint32Var(&vertexScanLimit, "limit", 0, "per-page request size (0 = server default)")
	vertexScanCmd.Flags().StringVar(&vertexScanCursor, "cursor", "", "opaque resume token printed by a previous --limit page")
	vertexScanCmd.Flags().BoolVar(&vertexScanAll, "all", false, "iterate every page through the SDK helper")

	vertexDeletePrefixCmd.Flags().Uint32Var(&vertexDeletePrefixLimit, "limit", 0, "max vertices to delete this call (0 = server default)")
	vertexDeletePrefixCmd.Flags().BoolVar(&vertexDeletePrefixDryRun, "dry-run", false, "count what would be deleted; mutate nothing")
	vertexDeletePrefixCmd.Flags().BoolVar(&vertexDeletePrefixYes, "yes", false, "confirm destructive delete")

	vertexCmd.AddCommand(vertexScanCmd, vertexCountCmd, vertexDeletePrefixCmd)
}
