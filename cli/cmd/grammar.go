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
	Use:   "delete { vertex <key> [<key>...] | edge <tail> <head> [<tail> <head>...] }",
	Short: "Delete vertices or edges (REPL grammar)",
	Long: `Delete vertices or edges using the same verb-first grammar as the REPL:

  lantern delete vertex <key> [<key> ...]
  lantern delete edge <tail> <head> [<tail> <head> ...]

One key/pair deletes a single target; supplying more than one routes to the
batched DeleteVertices / DeleteEdges RPC and prints "OK <n>" with the count
actually removed. To delete every vertex under a key prefix, use
"delete-prefix vertices".`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "delete", args)
	},
}

var grammarScanCmd = &cobra.Command{
	Use:   "scan { vertices <prefix> | edges <tail-prefix> } [limit] [head=<prefix>] [all=true]",
	Short: "Enumerate vertices or edges by prefix (REPL grammar)",
	Long: `Scan vertices or edges using the same verb-first grammar as the REPL:

  lantern scan vertices <prefix> [limit] [all=true]
  lantern scan edges <tail-prefix> [limit] [head=<prefix>] [all=true]

The optional trailing limit is a positional integer (the REPL form); results
print as a JSON array. all=true iterates every page and renders the full
result set; head=<prefix> narrows edges by their head endpoint. To list vertex
keys only (no values), use "keys <prefix>".`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "scan", args)
	},
}

var grammarKeysCmd = &cobra.Command{
	Use:   "keys <prefix> [limit]",
	Short: "List vertex keys under a prefix (Redis-style KEYS)",
	Long: `List vertex keys under a prefix, like Redis KEYS, using the same
verb-first grammar as the REPL:

  lantern keys <prefix> [limit]

Lantern is a prefix-indexed store, so the argument is a key PREFIX, not a
Redis glob — there is no trailing "*" to append. "lantern keys user:" lists
every vertex key under "user:" and is identical to typing "keys user:" at the
"lantern repl" prompt. Matching keys print one per line (like redis-cli), so
the output pipes cleanly into xargs/jq.

The optional trailing limit caps the page (mirroring "scan vertices"); a
prefix is required (the server rejects an empty prefix). This is the key-only
counterpart to "lantern scan vertices <prefix>", which returns whole vertex
objects.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "keys", args)
	},
}

var grammarCountCmd = &cobra.Command{
	Use:   "count vertices <prefix>",
	Short: "Count vertices under a key prefix (REPL grammar)",
	Long: `Count the vertices whose key starts with <prefix>, using the same
verb-first grammar as the REPL:

  lantern count vertices <prefix>

Prints a single decimal integer. The count is read from the radix index and is
not cross-checked against per-vertex liveness, so a vertex that has expired but
not yet been reaped is still counted; pipe "scan vertices <prefix> all=true"
through a line counter for the strictly-live count.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "count", args)
	},
}

var grammarDeletePrefixCmd = &cobra.Command{
	Use:   "delete-prefix vertices <prefix> [limit=<int>] [confirm=yes|dry_run=true]",
	Short: "Bulk-delete vertices under a key prefix (DESTRUCTIVE, REPL grammar)",
	Long: `Remove every live vertex whose key starts with <prefix>, using the same
verb-first grammar as the REPL:

  lantern delete-prefix vertices <prefix> dry_run=true   # preview, mutates nothing
  lantern delete-prefix vertices <prefix> confirm=yes    # perform the delete
  lantern delete-prefix vertices <prefix> confirm=yes limit=500

SAFETY GATE: exactly one of confirm=yes or dry_run=true is REQUIRED — a bare
"delete-prefix vertices <prefix>" is refused, because a bulk prefix delete is
irreversible and trivially typo-able. dry_run prints "would delete <n>";
confirm prints "deleted <n>". The optional limit caps the deletes per call.
Edges incident to deleted vertices are reaped lazily with their own TTL.`,
	RunE: func(cmd *cobra.Command, args []string) error {
		return runGrammarLine(cmd, "delete-prefix", args)
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
		grammarKeysCmd,
		grammarCountCmd,
		grammarDeletePrefixCmd,
	} {
		c.Flags().SetInterspersed(false)
		rootCmd.AddCommand(c)
	}
}
