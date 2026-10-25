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

const securityProvisioningHelp = `Usage: lantern provision-security --operator-directory DIRECTORY [--valid-for 720h]

Read the required operator intent from LANTERN_SECURITY_CONFIG_JSON with
LANTERN_AUTH_MODE=oidc. Generate and validate immutable trust for the explicit
target_platform and fixed node topology. material_root in the JSON selects the
serving material directory. It and the operator directory must be new,
absolute private paths. Existing or interrupted output is never overwritten.
This command does not acquire native time, initialize state or start listeners.

--operator-directory DIRECTORY  Separate new operator signing-custody directory
--valid-for DURATION            Finite operator maintenance window (default 720h)

Keep operator signing custody outside every serving runtime. Start its generated
membershippublisher configuration and run init-security on each actual target once.
Supply the same complete JSON for initialization and every ordinary startup.
Startup resumes intact keys and state and uses the supplied Issuers and runtime
settings; it never recovers configuration from generated material.
See deploy/examples/auth/README.md.
`

func parseSecurityProvisioning(args []string) (provider.SecurityProvisionOptions, bool, error) {
	var options provider.SecurityProvisionOptions
	flags := flag.NewFlagSet("provision-security", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&options.OperatorDirectory, "operator-directory", "", "new absolute operator custody directory")
	flags.DurationVar(&options.ValidFor, "valid-for", 30*24*time.Hour, "finite operator maintenance window")
	err := flags.Parse(args)
	if errors.Is(err, flag.ErrHelp) {
		return options, true, nil
	}
	if err != nil || flags.NArg() != 0 || options.OperatorDirectory == "" || options.ValidFor <= 0 {
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

Without a command, resume the configured server. Every OIDC startup requires
the complete LANTERN_SECURITY_CONFIG_JSON operator configuration and intact
independent material and initialized state; it never initializes missing state.

Commands:
  provision-security  Generate fixed-topology identity material from operator intent
  init-security       Initialize one new node on its actual target platform
  renew-security      Prepare certificate and next-window maintenance candidates

Use provision-security --help or renew-security --help for options. Supply the same
complete JSON with LANTERN_AUTH_MODE=oidc for provisioning, initialization and every
startup. material_root locates durable keys and trust; node selects one declared
member (optional only for a single node). Supplied Issuers are reconciled before
public serving. Configuration is never loaded from a persisted setup copy.
init-security accepts no arguments. Never place credentials in command arguments.
`)
	return err
}
