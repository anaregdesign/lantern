package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/anaregdesign/lantern/client"
	"github.com/spf13/cobra"
)

var bulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "Bulk-load vertices or edges from NDJSON",
	Long: `Stream NDJSON (one JSON object per line) into Lantern using the batch RPCs.

The input file may be a filesystem path or "-" for stdin. The CLI accumulates
lines into batches of --chunk-size (default 1000) and sends each batch via the
appropriate batch RPC. Progress is printed to stderr; "OK <total>" is printed
to stdout when the stream finishes.

INPUT SCHEMAS
  bulk vertices:
    {"key":"alice","value":{"name":"Alice"},"ttl":"1h"}
    {"key":"bob","value":42,"ttl":"30m"}

  bulk edges add  / bulk edges put:
    {"tail":"alice","head":"bob","weight":1.5,"ttl":"1h"}

  "ttl" is a Go duration string (e.g. 30s, 5m, 1h, 24h). If omitted, 24h
  is used. "value" may be any JSON value (object, array, scalar).

ERRORS
  A malformed line aborts the stream and returns exit code 1. Already-sent
  batches are NOT rolled back — Lantern has no transactions.

EXAMPLES
  lantern bulk vertices vertices.ndjson
  cat edges.ndjson | lantern bulk edges add -
  lantern bulk edges put edges.ndjson --chunk-size 5000
`,
}

type vertexLine struct {
	Key   string `json:"key"`
	Value any    `json:"value"`
	TTL   string `json:"ttl,omitempty"`
}

type edgeLine struct {
	Tail   string  `json:"tail"`
	Head   string  `json:"head"`
	Weight float32 `json:"weight"`
	TTL    string  `json:"ttl,omitempty"`
}

func parseTTL(s string) (time.Duration, error) {
	if s == "" {
		return 24 * time.Hour, nil
	}
	return time.ParseDuration(s)
}

func openInput(path string) (io.ReadCloser, error) {
	if path == "-" || path == "" {
		return io.NopCloser(os.Stdin), nil
	}
	return os.Open(path)
}

var bulkVerticesCmd = &cobra.Command{
	Use:   "vertices <file|->",
	Short: "Bulk-upsert vertices from NDJSON",
	Long: `Stream vertex upserts from NDJSON.

Each line:
  {"key":"<string>","value":<any json>,"ttl":"<duration>"}

Lines are accumulated into batches of --chunk-size and sent via PutVertices.
` + "`ttl`" + ` defaults to 24h when omitted.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		r, err := openInput(args[0])
		if err != nil {
			return err
		}
		defer r.Close()

		cli, err := dial()
		if err != nil {
			return err
		}
		defer cli.Close()

		sc := bufio.NewScanner(r)
		sc.Buffer(make([]byte, 64*1024), 8*1024*1024)

		batch := make([]client.VertexInput, 0, flagChunkSize)
		total := 0
		flush := func() error {
			if len(batch) == 0 {
				return nil
			}
			if err := cli.PutVertices(cmd.Context(), batch); err != nil {
				return err
			}
			total += len(batch)
			fmt.Fprintf(cmd.ErrOrStderr(), "... %d\n", total)
			batch = batch[:0]
			return nil
		}

		lineNo := 0
		for sc.Scan() {
			lineNo++
			if len(sc.Bytes()) == 0 {
				continue
			}
			var v vertexLine
			if err := json.Unmarshal(sc.Bytes(), &v); err != nil {
				return fmt.Errorf("line %d: %w", lineNo, err)
			}
			ttl, err := parseTTL(v.TTL)
			if err != nil {
				return fmt.Errorf("line %d: ttl: %w", lineNo, err)
			}
			batch = append(batch, client.VertexInput{
				Key: v.Key, Value: v.Value, Expiration: time.Now().Add(ttl),
			})
			if len(batch) >= flagChunkSize {
				if err := flush(); err != nil {
					return err
				}
			}
		}
		if err := sc.Err(); err != nil {
			return err
		}
		if err := flush(); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", total)
		return nil
	},
}

func newBulkEdgesCmd(verb string) *cobra.Command {
	return &cobra.Command{
		Use:   verb + " <file|->",
		Short: "Bulk-" + verb + " edges from NDJSON",
		Long: `Stream edge ` + verb + `s from NDJSON.

Each line:
  {"tail":"<string>","head":"<string>","weight":<float>,"ttl":"<duration>"}

Lines are accumulated into batches of --chunk-size and sent via ` +
			(map[string]string{"add": "AddEdges", "put": "PutEdges"})[verb] + `.
` + "`ttl`" + ` defaults to 24h when omitted.

Recall the semantic difference: ` + "`add`" + ` SUMS weight onto existing edges
(non-idempotent), ` + "`put`" + ` REPLACES it (idempotent).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			r, err := openInput(args[0])
			if err != nil {
				return err
			}
			defer r.Close()

			cli, err := dial()
			if err != nil {
				return err
			}
			defer cli.Close()

			sc := bufio.NewScanner(r)
			sc.Buffer(make([]byte, 64*1024), 8*1024*1024)

			batch := make([]client.EdgeInput, 0, flagChunkSize)
			total := 0
			flush := func() error {
				if len(batch) == 0 {
					return nil
				}
				var err error
				if verb == "add" {
					err = cli.AddEdges(cmd.Context(), batch)
				} else {
					err = cli.PutEdges(cmd.Context(), batch)
				}
				if err != nil {
					return err
				}
				total += len(batch)
				fmt.Fprintf(cmd.ErrOrStderr(), "... %d\n", total)
				batch = batch[:0]
				return nil
			}

			lineNo := 0
			for sc.Scan() {
				lineNo++
				if len(sc.Bytes()) == 0 {
					continue
				}
				var e edgeLine
				if err := json.Unmarshal(sc.Bytes(), &e); err != nil {
					return fmt.Errorf("line %d: %w", lineNo, err)
				}
				ttl, err := parseTTL(e.TTL)
				if err != nil {
					return fmt.Errorf("line %d: ttl: %w", lineNo, err)
				}
				batch = append(batch, client.EdgeInput{
					Tail: e.Tail, Head: e.Head, Weight: e.Weight,
					Expiration: time.Now().Add(ttl),
				})
				if len(batch) >= flagChunkSize {
					if err := flush(); err != nil {
						return err
					}
				}
			}
			if err := sc.Err(); err != nil {
				return err
			}
			if err := flush(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", total)
			return nil
		},
	}
}

var bulkEdgesCmd = &cobra.Command{
	Use:   "edges",
	Short: "Bulk-write edges from NDJSON",
	Long: `Bulk edge write — choose ` + "`add`" + ` (additive) or ` + "`put`" + ` (idempotent).
See the parent ` + "`bulk`" + ` help and ` + "`edge add`" + ` / ` + "`edge put`" + ` for semantics.`,
}

func init() {
	bulkEdgesCmd.AddCommand(newBulkEdgesCmd("add"), newBulkEdgesCmd("put"))
	bulkCmd.AddCommand(bulkVerticesCmd, bulkEdgesCmd)
	rootCmd.AddCommand(bulkCmd)
}
