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
	Short: "Interactive prompt (legacy)",
	Long: `Launch the legacy interactive prompt.

The REPL accepts whitespace-delimited verbs that mirror the legacy
lantern-cli grammar:

  get vertex <key>
  put vertex <key> <value> [ttl_seconds]
  delete vertex <key>
  get edge <tail> <head>
  add edge <tail> <head> <weight> [ttl_seconds]
  put edge <tail> <head> <weight> [ttl_seconds]
  delete edge <tail> <head>
  illuminate { neighbor | spt_relevance | spt_cost | mst_relevance | mst_cost } <seed> <step> <k> <tfidf>
  exit

NOTE
  The REPL grammar is frozen for backward compatibility. New features
  (batch RPCs, gzip, TLS flags, value typing) are only available via the
  scriptable subcommands.

EXAMPLE
  $ lantern repl
  > put vertex alice "Alice"
  OK (1.4ms)
  > get vertex alice
  "Alice"
  OK (0.8ms)
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
			case service.ErrIlluminate:
				fmt.Println("Usage: illuminate { neighbor | spt_relevance | spt_cost | mst_relevance | mst_cost } <seed: string> <step: int> <k: int> <tfidf: bool>")
			case service.ErrInvalidVerb:
				fmt.Println("Usage: { get | put | delete | add | illuminate } ...")
			case service.ErrInvalidObjective:
				fmt.Println("{ get { vertex | edge } | put { vertex | edge } | delete { vertex | edge } | add edge | illuminate {...} } ...")
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
