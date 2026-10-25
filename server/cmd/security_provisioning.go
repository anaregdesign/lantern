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

const securityProvisioningHelp = `Usage: lantern provision-security --output DIRECTORY --operator-directory DIRECTORY [--valid-for 720h]

Read the small setup intent from LANTERN_SECURITY_CONFIG_JSON with
LANTERN_AUTH_MODE=oidc. Generate and validate immutable trust for the explicit
target_platform and fixed node topology. Both output directories must be new,
absolute private paths. Existing or interrupted output is never overwritten.
This command does not acquire native time, initialize state or start listeners.

--output DIRECTORY              New serving provisioning directory
--operator-directory DIRECTORY  Separate new operator signing-custody directory
--valid-for DURATION            Finite operator maintenance window (default 720h)

Keep operator signing custody outside every serving runtime. Start its generated
membershippublisher configuration, run init-security on each actual target once,
then use ordinary startup to resume intact state. See deploy/examples/auth/README.md.
`

func parseSecurityProvisioning(args []string) (provider.SecurityProvisionOptions, bool, error) {
	var options provider.SecurityProvisionOptions
	flags := flag.NewFlagSet("provision-security", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.OutputDirectory, "output", "", "new absolute serving provisioning directory")
	flags.StringVar(&options.OperatorDirectory, "operator-directory", "", "new absolute operator custody directory")
	flags.DurationVar(&options.ValidFor, "valid-for", 30*24*time.Hour, "finite operator maintenance window")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return options, true, nil
	}
	if err != nil || flags.NArg() != 0 || options.OutputDirectory == "" || options.OperatorDirectory == "" || options.ValidFor <= 0 {
		return provider.SecurityProvisionOptions{}, false, errors.New("invalid provision-security arguments; use --help")
	}
	return options, false, nil
}

func provisionSecurity(ctx context.Context, args []string, out io.Writer) error {
	options, help, err := parseSecurityProvisioning(args)
	if err != nil {
		return err
	}
	if help {
		_, err = io.WriteString(out, securityProvisioningHelp)
		return err
	}
	if err := provider.ProvisionSecurity(ctx, options); err != nil {
		return fmt.Errorf("provision security: %w", err)
	}
	return nil
}

func writeServerHelp(out io.Writer) error {
	_, err := io.WriteString(out, `Usage: lantern [COMMAND]

Without a command, resume the configured server. OIDC startup requires intact
provisioned trust and previously initialized state; it never initializes state.

Commands:
  provision-security  Generate fixed-topology trust from small setup intent
  init-security       Initialize one new node on its actual target platform
  renew-security      Prepare certificate and next-window maintenance candidates

Use provision-security --help or renew-security --help for options. Select the node for
initialization and startup with LANTERN_AUTH_MODE=oidc and
LANTERN_SECURITY_CONFIG_JSON={"provisioned_file":"/absolute/node/security.json"}.
init-security accepts no arguments. Never place credentials in command arguments.
`)
	return err
}
