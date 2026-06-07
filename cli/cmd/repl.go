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

// replCmd preserves the legacy promptui-based interactive shell, now scoped
// behind an explicit subcommand. New scripted use should prefer the
// dedicated subcommands (vertex, edge, illuminate, bulk).
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
  put edge <tail> <head> <weight> [ttl_seconds]
  delete edge <tail> <head>
  scan vertices <prefix> [limit]
  scan edges <tail-prefix> [limit]
  illuminate <seed> <step> <k> [algorithm=none|mst|spt] [objective=min|max] [weighting=raw|tfidf]
  exit

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

The illuminate verb exposes the orthogonal axes introduced in #410:
algorithm selects the post-traversal reduction, objective picks the
direction (minimise/maximise), and weighting toggles RAW vs TF-IDF edge
weights. The three keyword arguments may appear in any order; each
defaults to the server's UNSPECIFIED resolution (algorithm=none,
objective=min, weighting=raw).

EXAMPLE
  $ lantern repl
  > put vertex alice "Alice"
  OK (1.4ms)
  > get vertex alice
  "Alice"
  OK (0.8ms)
  > illuminate alice 2 5 algorithm=spt objective=max weighting=tfidf
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
			case service.ErrScan:
				fmt.Println("Usage: scan { vertices <prefix: string> [<limit: int>] | edges <tail-prefix: string> [<limit: int>] }")
			case service.ErrIlluminate:
				fmt.Println("Usage: illuminate <seed: string> <step: int> <k: int> [algorithm=none|mst|spt] [objective=min|max] [weighting=raw|tfidf]")
			case service.ErrInvalidVerb:
				fmt.Println("Usage: { get | put | delete | add | scan | illuminate } ...")
			case service.ErrInvalidObjective:
				fmt.Println("{ get { vertex | edge } | put { vertex | edge } | delete { vertex | edge } | add edge | scan { vertices | edges } | illuminate {...} } ...")
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
