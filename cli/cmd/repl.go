package cmd

import (
	"fmt"
	"log"
	"time"

	"github.com/anaregdesign/lantern/cli/parser"
	"github.com/anaregdesign/lantern/cli/service"
	"github.com/manifoldco/promptui"
	"github.com/spf13/cobra"
)

// replBanner is the terminal counterpart to the admin /cli intro banner
// (#647). promptui renders only a bare ">" prompt, so a first-time user
// has no cue that the shell is self-describing. Printing this once at
// startup points them at the two verbs that make the REPL discoverable —
// `help` (full per-verb grammar) and `exit` (quit) — mirroring the wording
// of the web banner so both surfaces feel like one tool.
const replBanner = `Lantern interactive REPL — same grammar as the admin /cli page.
Type a verb and press Enter. Type "help" for the full command reference, "exit" to quit.`

// replCmd preserves the legacy promptui-based interactive shell, now scoped
// behind an explicit subcommand. New scripted use should prefer the
// dedicated subcommands (the family verbs bfs / pagerank / community, and bulk).
var replCmd = &cobra.Command{
	Use:   "repl",
	Short: "Interactive prompt",
	Long: `Launch the interactive prompt.

The REPL accepts whitespace-delimited verbs:

  get vertex <key>
  put vertex <key> <value> [ttl_seconds]
  delete vertex <key>
  get edge <tail> <head>
  add edge <tail> <head> <weight> [ttl_seconds]
  add decaying-edge <tail> <head> <initial_weight> <ratio> <steps> <interval_seconds>
  put edge <tail> <head> <weight> [ttl_seconds]
  delete edge <tail> <head>
  scan vertices <prefix> [limit]
  scan edges <tail-prefix> [limit]
  keys <prefix> [limit]
  bfs <seed> [step] [fan_out] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]
  pagerank <seed> [top_n] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]
  community <seed> [max_size] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]
  help
  exit

Type 'help' at the prompt to print the per-verb grammar with defaults
into the scrollback (#436).

QUOTING (#438)
  Any argument may be wrapped in "double quotes" — C-style escapes
  \" \\ \n \r \t apply — or 'single quotes' (verbatim, no escapes).
  Quotes are only special at token boundaries; embedded quotes inside
  a bareword stay verbatim. Examples:
    put vertex greeting "hello world"
    put vertex code 'console.log("hi")'
    put vertex path "C:\\Users\\hiroki"

CASE (#437)
  Verb and objective tokens are matched case-insensitively
  ('Get VERTEX foo' works). Arguments preserve case verbatim
  ('put vertex CamelKey CamelValue' stores CamelKey / CamelValue).

The traversal family is the verb (#975): bfs is a greedy per-hop top-k walk
(step depth, fan_out per-hop prune), pagerank is Personalized PageRank (top_n by
mass; α/ε locality knobs), and community is conductance-optimal local-community
extraction (max_size upper bound; α/ε knobs). bfs and community take a reduction
axis (none|mst|spt) that renders the result as an MST/SPT tree rooted at the
seed, plus an objective (min|max) that steers BOTH the per-hop top-k pruning and
the reduction direction; pagerank has neither (its relevance star is already a
tree). weighting (raw|tfidf|bm25) toggles the edge-weight transform on all
three. Only <seed> is required — every other argument has a default (bfs
step=5/fan_out=3, pagerank top_n=10, community max_size=0). restart_prob (α) and
epsilon (ε) default to 0, which the server resolves to α=0.15 / ε=1e-4.

EXAMPLE
  $ lantern-cli repl
  > put vertex alice "Alice"
  put vertex "alice" (no ttl)
  OK (1.4ms)
  > put vertex temp "soon" 1
  put vertex "temp" (ttl 1s, expires 2026-06-16T12:34:57Z)
  OK (0.6ms)
  > get vertex alice
  "Alice"
  OK (0.8ms)
  > bfs alice 2 5 reduction=spt objective=max weighting=tfidf
  { ... }
  OK (3.2ms)
  > exit
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cli, err := dial()
		if err != nil {
			return err
		}
		defer func() {
			if err := cli.Close(); err != nil {
				log.Println("close:", err)
			}
		}()
		srv := service.NewCLIService(cli)

		fmt.Println(replBanner)

		tpl := &promptui.PromptTemplates{
			Prompt:  "{{ . }} ",
			Valid:   "{{ . | green }} ",
			Invalid: "{{ . | red }} ",
			Success: "{{ . | bold }} ",
		}
		prompt := promptui.Prompt{
			Label:     ">",
			Validate:  parser.Validate,
			Templates: tpl,
		}

		ctx := cmd.Context()
		for {
			if err := ctx.Err(); err != nil {
				return nil
			}
			result, err := prompt.Run()
			if err != nil {
				return err
			}
			if result == "exit" {
				return nil
			}
			start := time.Now()
			err = srv.Run(ctx, result)
			elapsed := time.Since(start)
			switch err {
			case nil:
				fmt.Printf("OK (%v)\n", elapsed)
			case service.ErrGetVertex:
				fmt.Println("Usage: get vertex <key: string>")
			case service.ErrGetEdge:
				fmt.Println("Usage: get edge <tail: string> <head: string>")
			case service.ErrPutVertex:
				fmt.Println("Usage: put vertex <key: string> <value: string> [<ttl_seconds: int>]")
			case service.ErrPutEdge:
				fmt.Println("Usage: put edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]")
			case service.ErrDeleteVertex:
				fmt.Println("Usage: delete vertex <key: string>")
			case service.ErrDeleteEdge:
				fmt.Println("Usage: delete edge <tail: string> <head: string>")
			case service.ErrAddEdge:
				fmt.Println("Usage: add edge <tail: string> <head: string> <weight: float> [<ttl_seconds: int>]")
			case service.ErrAddDecayingEdge:
				fmt.Println("Usage: add decaying-edge <tail: string> <head: string> <initial_weight: float> <ratio: float> <steps: int> <interval_seconds: int>")
			case service.ErrScan:
				fmt.Println("Usage: scan { vertices <prefix: string> [<limit: int>] | edges <tail-prefix: string> [<limit: int>] }")
			case service.ErrKeys:
				fmt.Println("Usage: keys <prefix: string> [<limit: int>]")
			case service.ErrBFS:
				fmt.Println("Usage: bfs <seed: string> [step: int] [fan_out: int] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]")
			case service.ErrPagerank:
				fmt.Println("Usage: pagerank <seed: string> [top_n: int] [restart_prob=<float>] [epsilon=<float>] [weighting=raw|tfidf|bm25] [prefix=<string>]")
			case service.ErrCommunity:
				fmt.Println("Usage: community <seed: string> [max_size: int] [restart_prob=<float>] [epsilon=<float>] [reduction=none|mst|spt] [objective=min|max] [weighting=raw|tfidf|bm25] [prefix=<string>]")
			case service.ErrInvalidVerb:
				fmt.Println("Usage: { get | put | delete | add | scan | count | delete-prefix | keys | bfs | pagerank | community | help | exit } ...")
			case service.ErrInvalidObjective:
				fmt.Println("{ get { vertex | edge } | put { vertex | edge } | delete { vertex | edge } | add { edge | decaying-edge } | scan { vertices | edges } | count vertices | delete-prefix vertices | keys {...} | bfs {...} | pagerank {...} | community {...} } ...")
			case service.ErrConnection:
				fmt.Println("server error")
			default:
				fmt.Println(err)
			}
		}
	},
}

func init() {
	rootCmd.AddCommand(replCmd)
}
