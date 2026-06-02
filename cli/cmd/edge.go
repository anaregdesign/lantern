package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/anaregdesign/lantern/client"
	"github.com/spf13/cobra"
)

var edgeCmd = &cobra.Command{
	Use:   "edge",
	Short: "Get, add, put, or delete edges",
	Long: `Edge operations.

An edge is a directed (tail -> head) pair with a float32 weight and a TTL.
There are two write verbs and they are NOT equivalent:

  add   AddEdge is ADDITIVE. Repeated calls with the same (tail, head)
        sum their weights server-side. AddEdge is not idempotent and is
        deliberately excluded from the client's retry policy because
        retrying would double-count weight.

  put   PutEdge REPLACES the edge weight wholesale. PutEdge is idempotent
        and IS in the client retry policy.

Both write verbs implicitly upsert the tail and head vertices with the same
TTL if they do not yet exist (the server materialises endpoints with empty
values to maintain referential integrity).

Subcommands:
  get     fetch one edge weight + expiration
  add     additive write (sums weight)
  put     idempotent write (replaces weight)
  delete  delete one or more edges (batch path used when more than one pair)
`,
}

// -- edge get ---------------------------------------------------------------

var edgeGetCmd = &cobra.Command{
	Use:   "get <tail> <head>",
	Short: "Fetch the weight of one edge",
	Long: `Fetch the edge from <tail> to <head>.

OUTPUT
  {
    "tail":       "<tail>",
    "head":       "<head>",
    "weight":     <float>,
    "expiration": "<RFC3339 timestamp>"
  }

ERRORS
  Exit code 2 with "rpc error: code = NotFound" when the edge does not
  exist (either it was never created, was deleted, or its TTL expired).

EXAMPLES
  lantern edge get alice bob
  lantern edge get alice bob | jq .weight
`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer cli.Close()

		e, err := cli.GetEdge(cmd.Context(), args[0], args[1])
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				return fmt.Errorf("edge %s->%s: %w", args[0], args[1], err)
			}
			return err
		}
		out := map[string]any{
			"tail":       args[0],
			"head":       args[1],
			"weight":     e.Weight,
			"expiration": e.ExpirationTime().Format(time.RFC3339),
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(out)
	},
}

// -- edge add / put ---------------------------------------------------------

var (
	edgeAddTTL time.Duration
	edgePutTTL time.Duration
)

var edgeAddCmd = &cobra.Command{
	Use:   "add <tail> <head> <weight>",
	Short: "ADDITIVE write: sum <weight> onto (tail,head)",
	Long: `Accumulate <weight> onto the (tail, head) edge.

SEMANTICS
  AddEdge is additive: ` + "`add a b 1.5`" + ` followed by ` + "`add a b 0.5`" + ` leaves the
  weight at 2.0. Use this when you are streaming counts, scores, or
  decaying interaction signal.

  Because AddEdge is NOT idempotent, the client's default retry policy
  excludes it. A transient UNAVAILABLE will surface to you so you can
  decide whether to retry.

TTL
  --ttl bumps the edge's expiration to now+ttl on every call. There is no
  "extend by" mode — each call sets a fresh absolute expiration. Default 24h.

WEIGHT
  Parsed as float32 (the wire type). NaN and ±Inf are rejected server-side.

OUTPUT
  Prints "OK" on success.

EXAMPLES
  lantern edge add alice bob 1.5           # weight 1.5
  lantern edge add alice bob 0.5           # weight 2.0
  lantern edge add alice bob 0.1 --ttl 30m # weight 2.1, TTL reset to 30m
`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := strconv.ParseFloat(args[2], 32)
		if err != nil {
			return fmt.Errorf("weight: %w", err)
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer cli.Close()
		if err := cli.AddEdge(cmd.Context(), args[0], args[1], float32(w), edgeAddTTL); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "OK")
		return nil
	},
}

var edgePutCmd = &cobra.Command{
	Use:   "put <tail> <head> <weight>",
	Short: "IDEMPOTENT write: replace (tail,head) weight",
	Long: `Set the (tail, head) edge weight to <weight>, replacing any previous value.

SEMANTICS
  PutEdge is idempotent: ` + "`put a b 1.5`" + ` followed by ` + "`put a b 0.5`" + ` leaves the
  weight at 0.5. Use this when the weight is a measured property of the
  edge (similarity, distance, capacity) rather than an accumulated signal.

  Because PutEdge is idempotent, the client's default retry policy retries
  it on UNAVAILABLE / RESOURCE_EXHAUSTED.

TTL
  --ttl sets the edge's expiration to now+ttl. Default 24h.

WEIGHT
  Parsed as float32. NaN and ±Inf are rejected server-side.

OUTPUT
  Prints "OK" on success.

EXAMPLES
  lantern edge put alice bob 1.5            # weight 1.5
  lantern edge put alice bob 0.5            # weight 0.5 (overwritten)
  lantern edge put alice bob 0.5 --ttl 1h
`,
	Args: cobra.ExactArgs(3),
	RunE: func(cmd *cobra.Command, args []string) error {
		w, err := strconv.ParseFloat(args[2], 32)
		if err != nil {
			return fmt.Errorf("weight: %w", err)
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer cli.Close()
		if err := cli.PutEdge(cmd.Context(), args[0], args[1], float32(w), edgePutTTL); err != nil {
			return err
		}
		fmt.Fprintln(cmd.OutOrStdout(), "OK")
		return nil
	},
}

// -- edge delete ------------------------------------------------------------

var edgeDeleteCmd = &cobra.Command{
	Use:   "delete <tail>:<head> [<tail>:<head>...]",
	Short: "Delete one or more edges by tail:head pair",
	Long: `Delete edges identified by "tail:head" pairs.

Each positional argument is a single "tail:head" pair separated by a literal
colon — this avoids the ambiguity of positional pairs of two strings and
makes shell scripting (xargs, jq -r '"\(.tail):\(.head)"') straightforward.

If both endpoints contain colons, use the alternate "tail|head" syntax
by passing --separator '|'.

When more than one pair is given the batch RPC (DeleteEdges) is used,
chunked at --chunk-size (default 1000).

OUTPUT
  Prints "OK <n>" on success, where <n> is the number of pairs submitted.

EXAMPLES
  lantern edge delete alice:bob
  lantern edge delete alice:bob bob:carol carol:dave
  jq -r '"\(.tail):\(.head)"' edges.json | xargs lantern edge delete
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		sep, _ := cmd.Flags().GetString("separator")
		if sep == "" {
			sep = ":"
		}
		refs := make([]client.EdgeRef, 0, len(args))
		for _, a := range args {
			t, h, ok := strings.Cut(a, sep)
			if !ok || t == "" || h == "" {
				return fmt.Errorf("invalid pair %q (want %q-separated tail and head)", a, sep)
			}
			refs = append(refs, client.EdgeRef{Tail: t, Head: h})
		}

		cli, err := dial()
		if err != nil {
			return err
		}
		defer cli.Close()

		if len(refs) == 1 {
			if err := cli.DeleteEdge(cmd.Context(), refs[0].Tail, refs[0].Head); err != nil {
				return err
			}
		} else {
			if err := cli.DeleteEdges(cmd.Context(), refs); err != nil {
				return err
			}
		}
		fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", len(refs))
		return nil
	},
}

func init() {
	edgeAddCmd.Flags().DurationVar(&edgeAddTTL, "ttl", 24*time.Hour, "TTL relative to now (e.g. 30s, 5m, 24h)")
	edgePutCmd.Flags().DurationVar(&edgePutTTL, "ttl", 24*time.Hour, "TTL relative to now (e.g. 30s, 5m, 24h)")
	edgeDeleteCmd.Flags().String("separator", ":", "delimiter inside each tail:head positional argument")

	edgeCmd.AddCommand(edgeGetCmd, edgeAddCmd, edgePutCmd, edgeDeleteCmd)
	rootCmd.AddCommand(edgeCmd)
}
