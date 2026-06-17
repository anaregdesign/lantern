package cmd

import (
	"github.com/anaregdesign/lantern/cli/service"
	"github.com/spf13/cobra"
)

// This file wires the REPL grammar (verb-first, whitespace-delimited) as
// top-level one-shot commands so that everything you can type at the
// `lantern repl` prompt also works as a single shell invocation (#672):
//
//	lantern get    vertex <key>
//	lantern put    vertex <key> <value> [ttl_seconds]
//	lantern delete vertex <key>
//	lantern get    edge   <tail> <head>
//	lantern add    edge   <tail> <head> <weight> [ttl_seconds]
//	lantern put    edge   <tail> <head> <weight> [ttl_seconds]
//	lantern delete edge   <tail> <head>
//	lantern scan   vertices <prefix> [limit]
//	lantern scan   edges    <tail-prefix> [limit]
//
// These commands intentionally share ONE grammar and ONE dispatcher with
// the REPL: each forwards its argv to service.CLIService.RunArgs, the same
// entry point the REPL uses per typed line. They are additive — the
// noun-first `vertex` / `edge` subcommands remain for typed values, batch
// writes, scan pagination, NDJSON bulk load, and the TLS/gzip flags that
// the REPL grammar deliberately does not cover.
//
// Flag-parsing note: each verb sets SetInterspersed(false) so a value
// beginning with '-' (e.g. a negative edge weight, `add edge a b -1.5`) is
// taken verbatim as a positional token instead of being mis-parsed as a
// flag. The trade-off is that global connection flags must come BEFORE the
// verb, e.g. `lantern --address host:6380 get vertex alice`.

// runGrammarLine dials the server and dispatches a REPL-grammar token
// stream (the verb plus its arguments) through the same CLIService the
// REPL uses, so the one-liner and the prompt accept byte-identical grammar.
func runGrammarLine(cmd *cobra.Command, verb string, args []string) error {
	if len(args) == 0 {
		return cmd.Help()
	}
	cli, err := dial()
	if err != nil {
		return err
	}
	defer func() { _ = cli.Close() }()
	return service.NewCLIService(cli).RunArgs(cmd.Context(), append([]string{verb}, args...))
}

var grammarGetCmd = &cobra.Command{
	Use:   "get { vertex <key> | edge <tail> <head> }",
	Short: "Read a vertex or edge (REPL grammar)",
	Long: `Read a vertex or edge using the same verb-first grammar as the REPL:

  lantern get vertex <key>
  lantern get edge <tail> <head>

"lantern get vertex alice" is identical to typing "get vertex alice" at the
"lantern repl" prompt. The vertex value prints as JSON; the edge weight prints
as a float. For typed output fields and a NotFound exit code, the noun-first
"lantern vertex get" / "lantern edge get" commands remain available.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "get", args)
	},
}

var grammarPutCmd = &cobra.Command{
	Use:   "put { vertex <key> <value> | edge <tail> <head> <weight> } [ttl_seconds]",
	Short: "Upsert a vertex or replace an edge weight (REPL grammar)",
	Long: `Write a vertex or edge using the same verb-first grammar as the REPL:

  lantern put vertex <key> <value> [ttl_seconds]
  lantern put edge <tail> <head> <weight> [ttl_seconds]

The optional trailing ttl_seconds is a positional integer (the REPL form),
NOT the --ttl duration flag of "lantern vertex put". Omit it for a permanent
(no-decay) write. The vertex value is auto-typed (int, float, bool, RFC3339
datetime, else string), exactly as in the REPL; for forced typing (JSON,
leading-zero strings, durations) use "lantern vertex put --value-type".

put edge REPLACES the weight (idempotent); use "add edge" to accumulate.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "put", args)
	},
}

var grammarAddCmd = &cobra.Command{
	Use:   "add edge <tail> <head> <weight> [ttl_seconds]",
	Short: "Additive edge write: sum weight onto (tail,head) (REPL grammar)",
	Long: `Accumulate an edge weight using the same verb-first grammar as the REPL:

  lantern add edge <tail> <head> <weight> [ttl_seconds]

add edge is ADDITIVE: repeated calls on the same (tail, head) pair sum their
weights server-side ("add edge a b 1.5" then "add edge a b 0.5" leaves 2.0).
Use "put edge" when the weight is a measured property to replace wholesale.

The optional trailing ttl_seconds is a positional integer (the REPL form);
omit it for a permanent (no-decay) edge.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "add", args)
	},
}

var grammarDeleteCmd = &cobra.Command{
	Use:   "delete { vertex <key> | edge <tail> <head> }",
	Short: "Delete one vertex or edge (REPL grammar)",
	Long: `Delete a vertex or edge using the same verb-first grammar as the REPL:

  lantern delete vertex <key>
  lantern delete edge <tail> <head>

This is the single-target REPL form. For batch deletes (DeleteVertices /
DeleteEdges), prefix deletes, or piping keys from a file, use the noun-first
"lantern vertex delete" / "lantern edge delete" commands, which accept
multiple keys/pairs.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "delete", args)
	},
}

var grammarScanCmd = &cobra.Command{
	Use:   "scan { vertices <prefix> | edges <tail-prefix> } [limit]",
	Short: "Enumerate vertices or edges by prefix (REPL grammar)",
	Long: `Scan vertices or edges using the same verb-first grammar as the REPL:

  lantern scan vertices <prefix> [limit]
  lantern scan edges <tail-prefix> [limit]

The optional trailing limit is a positional integer (the REPL form). Results
print as a JSON array. For cursor pagination, --all streaming, NDJSON output,
or a --head-prefix on edges, use the noun-first "lantern vertex scan" /
"lantern edge scan" commands.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "scan", args)
	},
}

func init() {
	// SetInterspersed(false): stop flag parsing at the first positional so
	// leading-dash values (negative weights/values) pass through verbatim;
	// global connection flags must therefore precede the verb.
	for _, c := range []*cobra.Command{
		grammarGetCmd,
		grammarPutCmd,
		grammarAddCmd,
		grammarDeleteCmd,
		grammarScanCmd,
	} {
		c.Flags().SetInterspersed(false)
		rootCmd.AddCommand(c)
	}
}
