// membershippublisher owns only bounded local publication. It neither starts
// servers nor changes trust, Roles, certificates, identities or credential TTLs.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("membershippublisher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := flags.String("config", "", "absolute private operator configuration")
	state := flags.String("state", "", "absolute persistent operator journal")
	mode := flags.String("mode", "", "init or resume (required)")
	if flags.Parse(args) != nil || flags.NArg() != 0 {
		return membershippublisher.ErrState
	}
	p, err := membershippublisher.Open(*config, *state, *mode)
	if err != nil {
		return membershippublisher.ErrState
	}
	err = p.Run(ctx)
	err = errors.Join(err, p.Close())
	if err == nil {
		_, err = fmt.Fprintln(out, "declared publication budget completed")
	}
	return err
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		// Fixed category only: errors never print private paths or raw inputs.
		fmt.Fprintln(os.Stderr, "membership publication stopped; retain operator state and inspect before explicit resume")
		os.Exit(1)
	}
}
