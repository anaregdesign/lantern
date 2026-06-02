package cmd

import (
	"context"
	"fmt"
	"github.com/anaregdesign/lantern/cli/parser"
	"github.com/anaregdesign/lantern/cli/service"
	"github.com/manifoldco/promptui"
	"log"
	"net"
	"os"
	"strconv"
	"time"

	"github.com/anaregdesign/lantern/client"
	"github.com/spf13/cobra"
)

var (
	host string
	port int
)

// rootCmd represents the base command when called without any subcommands
var rootCmd = &cobra.Command{
	Use:   "lantern-cli",
	Short: "A CLI for Lantern",
	Long:  `A CLI for Lantern. `,

	RunE: func(cmd *cobra.Command, args []string) error {
		ctx := context.Background()
		cli, err := client.NewLantern(net.JoinHostPort(host, strconv.Itoa(port)))
		srv := service.NewCLIService(cli)
		if err != nil {
			return err
		}
		defer func() {
			err := cli.Close()
			if err != nil {
				log.Fatal(err)
			}
		}()

		template := &promptui.PromptTemplates{
			Prompt:  "{{ . }} ",
			Valid:   "{{ . | green }} ",
			Invalid: "{{ . | red }} ",
			Success: "{{ . | bold }} ",
		}
		prompt := promptui.Prompt{
			Label:     ">",
			Validate:  parser.Validate,
			Templates: template,
		}

		for {
			result, err := prompt.Run()
			if err != nil {
				return err
			}

			switch result {
			case "exit":
				return nil

			default:
				start := time.Now()
				err := srv.Run(ctx, result)
				end := time.Now()
				switch err {
				case nil:
					fmt.Printf("OK (%v)\n", end.Sub(start))
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
					fmt.Println("Usage: illuminate { neighbor | spt_relevance | spt_cost | msp_relevance | msp_cost } <seed: string> <step: int> <k: int> <tfidf: bool>")

				case service.ErrInvalidVerb:
					fmt.Println("Usage: { get | put | delete | add | illuminate } ...")

				case service.ErrInvalidObjective:
					fmt.Println("{\n\tget { vertex | edge } | \n\tput { vertex | edge } | \n\tdelete { vertex | edge } | \n\tadd edge | \n\tilluminate { neighbor | spt_relevance | spt_cost | msp_relevance | msp_cost }\n} ...\nspt: shortest path tree\nmsp: minimum spanning tree")

				case service.ErrConnection:
					fmt.Println("server error")

				default:
					fmt.Println(err)
				}
			}
		}
	},
}

func Execute() {
	err := rootCmd.Execute()
	if err != nil {
		os.Exit(1)
	}
}

func init() {
	rootCmd.Flags().StringVarP(&host, "host", "H", "localhost", "host")
	rootCmd.Flags().IntVarP(&port, "port", "p", 6380, "port")
}
