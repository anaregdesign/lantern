package cmd

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/anaregdesign/lantern/sdks/go"
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

  "ttl" is a Go duration string (e.g. 30s, 5m, 1h, 24h). If omitted, the
  vertex/edge is stored permanently (no decay). "value" may be any JSON
  value (object, array, scalar).

ERRORS
  A malformed line aborts the stream and returns exit code 1. Already-sent
  batches are NOT rolled back — Lantern has no transactions.

EXAMPLES
  lantern-cli bulk vertices vertices.ndjson
  cat edges.ndjson | lantern-cli bulk edges add -
  lantern-cli bulk edges put edges.ndjson --chunk-size 5000
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
		// Omitted "ttl" ⇒ permanent (no decay). expirationFromTTL maps the
		// resulting zero duration to the wire's permanent sentinel (#523).
		return 0, nil
	}
	return time.ParseDuration(s)
}

// expirationFromTTL maps a relative TTL onto the absolute expiration the
// batch RPCs carry. A non-positive ttl ⇒ permanent: it yields the zero
// time.Time, which the SDK serialises as the wire's never-expiring
// sentinel. Mirrors the SDK convenience-method contract so the bulk path
// injects no hidden default expiration (#523).
func expirationFromTTL(ttl time.Duration) time.Time {
	if ttl <= 0 {
		return time.Time{}
	}
	return time.Now().Add(ttl)
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
` + "`ttl`" + ` is stored permanently (no decay) when omitted.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
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
			_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "... %d\n", total)
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
			// Lantern's wire format has no nested value variant. Re-encode
			// object/array values as compact JSON strings so the batch RPC
			// does not reject the whole batch with ErrInvalidType. Scalars
			// pass through.
			value := v.Value
			switch value.(type) {
			case map[string]any, []any:
				b, err := json.Marshal(value)
				if err != nil {
					return fmt.Errorf("line %d: re-encode value: %w", lineNo, err)
				}
				value = string(b)
			}
			batch = append(batch, client.VertexInput{
				Key: v.Key, Value: value, Expiration: expirationFromTTL(ttl),
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
		_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", total)
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
` + "`ttl`" + ` is stored permanently (no decay) when omitted.

Recall the semantic difference: ` + "`add`" + ` SUMS weight onto existing edges
(non-idempotent), ` + "`put`" + ` REPLACES it (idempotent).`,
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
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
				_, _ = fmt.Fprintf(cmd.ErrOrStderr(), "... %d\n", total)
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
					Expiration: expirationFromTTL(ttl),
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
			_, _ = fmt.Fprintf(cmd.OutOrStdout(), "OK %d\n", total)
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
