package cmd

import (
	"encoding/json"
	"fmt"

	client "github.com/anaregdesign/lantern/sdks/go"
	"github.com/spf13/cobra"
)

var (
	edgeScanTailPrefix string
	edgeScanHeadPrefix string
	edgeScanLimit      uint32
	edgeScanCursor     string
	edgeScanAll        bool
)

var edgeScanCmd = &cobra.Command{
	Use:   "scan",
	Short: "Enumerate edges filtered by tail and/or head prefix",
	Long: `Walk live edges in ascending (tail, head) order and emit each as one
NDJSON line on stdout (same per-edge schema as "edge get").

PREFIXES
  --tail-prefix and --head-prefix each narrow the result set on their
  endpoint. Either may be omitted to leave that dimension unconstrained;
  omitting both scans every live edge.

  The server walks the vertex-side prefix index for the tail dimension
  and, for each matching tail, probes a per-tail head radix to honour
  --head-prefix as an index lookup (not a post-filter). A head-only
  scan still has to iterate every tail because no global head->tails
  reverse index exists, so combining both prefixes remains the most
  efficient shape.

PAGINATION
  Without --all the server returns at most --limit edges and an opaque
  next-cursor token. If a next page exists, the token is printed to
  stderr as "next-cursor: <token>"; re-run with "--cursor <token>" to
  resume.

  With --all the CLI iterates every page through the SDK's iter.Seq2
  helper and streams edges as they arrive.

CURSOR
  Edge cursors are opaque and intentionally wire-incompatible with
  vertex-scan cursors. Do not interchange them.

OUTPUT
  One JSON object per line on stdout (NDJSON), safe for piping into jq.
  The cursor (if any, single-page mode only) goes to stderr.

EXAMPLES
  lantern edge scan --tail-prefix user:
  lantern edge scan --tail-prefix user: --head-prefix post: --limit 100
  lantern edge scan --head-prefix post: --all > posts.ndjson
`,
	Args: cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		out := cmd.OutOrStdout()
		errw := cmd.ErrOrStderr()
		enc := json.NewEncoder(out)

		baseOpts := []client.EdgeScanOption{}
		if edgeScanTailPrefix != "" {
			baseOpts = append(baseOpts, client.WithEdgeScanTailPrefix(edgeScanTailPrefix))
		}
		if edgeScanHeadPrefix != "" {
			baseOpts = append(baseOpts, client.WithEdgeScanHeadPrefix(edgeScanHeadPrefix))
		}

		if edgeScanAll {
			for batch, err := range cli.ScanEdgesAll(cmd.Context(), edgeScanLimit, baseOpts...) {
				if err != nil {
					return err
				}
				for _, e := range batch {
					if err := enc.Encode(e); err != nil {
						return err
					}
				}
			}
			return nil
		}

		opts := append([]client.EdgeScanOption{}, baseOpts...)
		if edgeScanLimit > 0 {
			opts = append(opts, client.WithEdgeScanLimit(edgeScanLimit))
		}
		if edgeScanCursor != "" {
			opts = append(opts, client.WithEdgeScanCursor([]byte(edgeScanCursor)))
		}
		es, next, err := cli.ScanEdges(cmd.Context(), opts...)
		if err != nil {
			return err
		}
		for _, e := range es {
			if err := enc.Encode(e); err != nil {
				return err
			}
		}
		if len(next) > 0 {
			_, _ = fmt.Fprintf(errw, "next-cursor: %s\n", string(next))
		}
		return nil
	},
}

func init() {
	edgeScanCmd.Flags().StringVar(&edgeScanTailPrefix, "tail-prefix", "", "restrict to edges whose tail starts with this prefix")
	edgeScanCmd.Flags().StringVar(&edgeScanHeadPrefix, "head-prefix", "", "restrict to edges whose head starts with this prefix")
	edgeScanCmd.Flags().Uint32Var(&edgeScanLimit, "limit", 0, "per-page request size (0 = server default)")
	edgeScanCmd.Flags().StringVar(&edgeScanCursor, "cursor", "", "opaque resume token printed by a previous --limit page")
	edgeScanCmd.Flags().BoolVar(&edgeScanAll, "all", false, "iterate every page through the SDK helper")

	edgeCmd.AddCommand(edgeScanCmd)
}
