package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"time"

	"github.com/anaregdesign/lantern/server/provider"
)

const securityRenewalHelp = `Usage: lantern renew-security --operator-directory DIRECTORY --output DIRECTORY [--publisher-config FILE] [--valid-for 720h]

Prepare certificate and next-window candidates from retained operator custody.
--operator-directory selects the existing private operator bundle; --output
must be a new absolute private directory. Existing trust, certificates, native
state, signed membership targets and the publisher journal are not overwritten.

--operator-directory DIRECTORY  Existing private operator signing custody
--output DIRECTORY              New private renewal candidate directory
--publisher-config FILE         Current exact publisher config (default: operator-directory/publisher.json)
--valid-for DURATION            Next finite operator window (default 720h)

Review publication.json and require COMPLETE before maintenance. Drain and stop
nodes to CLEAN, stop the publisher, then install reviewed candidates at their
original paths. Advance the retained publisher journal with advance-window,
resume the publisher and restart nodes against unchanged trust and state.
Root trust, workload keys, identities, topology and state bindings do not change.
There is no hot reload or state reset. Keep all operator keys outside Server.
See docs/live-membership-renewal.md for the complete maintenance sequence.
`

func parseSecurityRenewal(args []string) (provider.SecurityRenewOptions, bool, error) {
	var options provider.SecurityRenewOptions
	flags := flag.NewFlagSet("renew-security", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.OutputDirectory, "output", "", "new absolute renewal candidate directory")
	flags.StringVar(&options.OperatorDirectory, "operator-directory", "", "existing absolute operator custody directory")
	flags.StringVar(&options.PublisherConfig, "publisher-config", "", "current exact publisher configuration")
	flags.DurationVar(&options.ValidFor, "valid-for", 30*24*time.Hour, "next finite operator maintenance window")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return options, true, nil
	}
	if err != nil || flags.NArg() != 0 || options.OutputDirectory == "" || options.OperatorDirectory == "" || options.ValidFor <= 0 {
		return provider.SecurityRenewOptions{}, false, errors.New("invalid renew-security arguments; use --help")
	}
	return options, false, nil
}

func renewSecurity(ctx context.Context, args []string, out io.Writer) error {
	options, help, err := parseSecurityRenewal(args)
	if err != nil {
		return err
	}
	if help {
		_, err = io.WriteString(out, securityRenewalHelp)
		return err
	}
	if err := provider.RenewSecurity(ctx, options); err != nil {
		return fmt.Errorf("renew security: %w", err)
	}
	return nil
}
