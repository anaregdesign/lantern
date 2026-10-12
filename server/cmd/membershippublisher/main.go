// membershippublisher owns only bounded local publication. It neither starts
// servers nor changes trust, Roles, certificates, identities or credential TTLs.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/anaregdesign/lantern/server/internal/membershippublisher"
)

func run(ctx context.Context, args []string, out io.Writer) error {
	flags := flag.NewFlagSet("membershippublisher", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	config := flags.String("config", "", "absolute private operator configuration")
	state := flags.String("state", "", "absolute persistent operator journal")
	mode := flags.String("mode", "", "init, resume or advance-window (required)")
	next := flags.String("next-config", "", "retained immutable next maintenance-window configuration")
	lead := flags.Duration("maintenance-warning", 7*24*time.Hour, "operator maintenance warning lead (minimum renewal and cleanup budget enforced)")
	if flags.Parse(args) != nil || flags.NArg() != 0 || *lead <= 0 {
		return membershippublisher.ErrState
	}
	if *mode == "advance-window" {
		if *next == "" {
			return membershippublisher.ErrState
		}
		if err := membershippublisher.AdvanceWindow(*config, *next, *state); err != nil {
			return err
		}
		return json.NewEncoder(out).Encode(map[string]string{"phase": "window_advanced", "next_action": "explicit_resume_new_config"})
	}
	if *next != "" {
		return membershippublisher.ErrState
	}
	p, err := membershippublisher.Open(*config, *state, *mode)
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	err = p.RunWithStatus(ctx, func(s membershippublisher.Status) error { return encoder.Encode(s) }, *lead)
	if err == context.Canceled {
		err = nil // Supported stop still requires Close/lease release to succeed.
	}
	return errors.Join(err, p.Close())
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout); err != nil {
		// Fixed category only: errors never print private paths or raw inputs.
		fmt.Fprintf(os.Stderr, "membership publication stopped: %s; retain state and inspect before explicit resume\n", membershippublisher.FailureCategory(err))
		os.Exit(1)
	}
}
