package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/anaregdesign/lantern/client"
	"github.com/spf13/cobra"
)

var vertexCmd = &cobra.Command{
	Use:   "vertex",
	Short: "Get, put, or delete vertices",
	Long: `Vertex operations.

A "vertex" in Lantern is a key (string) plus an arbitrary value plus an
expiration time. Values are typed: the client sends int, float, bool,
string, datetime (RFC3339), or JSON. Use --value-type to override the
auto-detected type at "vertex put".

Subcommands:
  get     fetch one vertex by key (errors with NotFound if absent)
  put     upsert one vertex with a relative TTL
  delete  delete one or more vertices (batch path used when more than one key)
`,
}

// -- vertex get -------------------------------------------------------------

var vertexGetCmd = &cobra.Command{
	Use:   "get <key>",
	Short: "Fetch a vertex by key",
	Long: `Fetch the vertex stored at <key>.

OUTPUT
  JSON object on stdout with the fields:
    {
      "key":        "<key>",
      "type":       "string"|"int64"|"float64"|"bool"|"bytes"|"timestamp"|"duration"|"nil"|...,
      "value":      <typed value: string|number|bool|base64|null>,
      "expiration": "<RFC3339Nano timestamp>"
    }

ERRORS
  Exit code 2 with "rpc error: code = NotFound" when the key is absent or
  has already expired. Use this to distinguish "missing" from "broken".

EXAMPLES
  lantern vertex get alice
  lantern vertex get alice | jq .value
`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		v, err := cli.GetVertex(cmd.Context(), args[0])
		if err != nil {
			if errors.Is(err, client.ErrNotFound) {
				return fmt.Errorf("vertex %q: %w", args[0], err)
			}
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(v)
	},
}

// -- vertex put -------------------------------------------------------------

var (
	vertexPutTTL       time.Duration
	vertexPutValueType string
)

var vertexPutCmd = &cobra.Command{
	Use:   "put <key> <value>",
	Short: "Upsert a vertex with a relative TTL",
	Long: `Upsert the vertex at <key> with the given <value>.

VALUE TYPING
  By default --value-type=auto tries (in order) int, float, bool, RFC3339
  datetime, then falls back to string. Pass --value-type explicitly when
  the value is ambiguous:

    --value-type=string   force string ("123" stays a string)
    --value-type=int      base-10 integer
    --value-type=float    IEEE 754 double
    --value-type=bool     true / false / 1 / 0
    --value-type=datetime RFC3339, e.g. 2025-01-01T00:00:00Z
    --value-type=duration Go duration, e.g. 30s, 5m, 1h30m
    --value-type=json     parse <value> as JSON (object, array, scalar)

TTL SEMANTICS
  --ttl is a Go duration relative to the server's "now" at receipt
  (e.g. 30s, 5m, 1h, 24h, 168h). The vertex is reaped lazily after it
  expires. Default 24h.

OUTPUT
  Prints "OK" on success. Errors go to stderr (exit code 2 for server
  errors, 1 for local parse errors).

EXAMPLES
  lantern vertex put alice "Alice Smith"                 # string
  lantern vertex put count  42                            # int (auto)
  lantern vertex put price  19.99                         # float (auto)
  lantern vertex put alive  true                          # bool (auto)
  lantern vertex put alice '{"age":30}' --value-type=json --ttl 1h
  lantern vertex put zipcode "01234" --value-type=string  # keep leading zero
`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		val, err := parseValue(args[1], vertexPutValueType)
		if err != nil {
			return err
		}
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()
		if err := cli.PutVertex(cmd.Context(), args[0], val, vertexPutTTL); err != nil {
			return err
		}
		_, _ = fmt.Fprintln(cmd.OutOrStdout(), "OK")
		return nil
	},
}

// -- vertex delete ----------------------------------------------------------

var vertexDeleteCmd = &cobra.Command{
	Use:   "delete <key> [<key>...]",
	Short: "Delete one or more vertices",
	Long: `Delete the vertices at the given keys.

When a single key is given the single-key RPC (DeleteVertex) is used.
When more than one key is given the batch RPC (DeleteVertices) is used,
which the client chunks at --chunk-size (default 1000) to stay below
the server's MaxBatchSize cap.

EDGE CLEANUP
  Edges incident to a removed vertex are NOT eagerly deleted — they are
  reaped lazily by the server GC loop along with their own TTL. A GetEdge
  immediately after DeleteVertex may still return the edge briefly.

OUTPUT
  Prints "OK <n>" on success, where <n> is the number of keys submitted.

EXAMPLES
  lantern vertex delete alice                       # single
  lantern vertex delete alice bob carol             # batch (DeleteVertices)
  cat keys.txt | xargs lantern vertex delete         # batch from a file
`,
	Args: cobra.MinimumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() { _ = cli.Close() }()

		if len(args) == 1 {
			if err := cli.DeleteVertex(cmd.Context(), args[0]); err != nil {
				return err
			}
		} else {
			if err := cli.DeleteVertices(cmd.Context(), args); err != nil {
				return err
			}
		}
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", len(args))
		return nil
	},
}

func init() {
	vertexPutCmd.Flags().DurationVar(&vertexPutTTL, "ttl", 24*time.Hour, "TTL relative to now (e.g. 30s, 5m, 24h)")
	vertexPutCmd.Flags().StringVar(&vertexPutValueType, "value-type", "auto", "type of <value>: auto|string|int|float|bool|datetime|duration|json")

	vertexCmd.AddCommand(vertexGetCmd, vertexPutCmd, vertexDeleteCmd)
	rootCmd.AddCommand(vertexCmd)
}
