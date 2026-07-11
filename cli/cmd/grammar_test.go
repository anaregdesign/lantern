package cmd

import (
	"io"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
	"github.com/spf13/cobra"
)

// The verb-first one-liners (#672) must stay wired to rootCmd and must keep
// flag interspersing OFF, so a leading-dash value (a negative edge weight or
// vertex value) passes through as a positional token rather than being
// mis-parsed as an unknown flag.
func TestGrammarVerbsRegistered(t *testing.T) {
	want := map[string]bool{"get": true, "put": true, "add": true, "delete": true, "scan": true, "keys": true}
	found := map[string]*cobra.Command{}
	for _, c := range rootCmd.Commands() {
		if want[c.Name()] {
			found[c.Name()] = c
		}
	}
	for name := range want {
		c, ok := found[name]
		if !ok {
			t.Errorf("top-level verb %q is not registered on rootCmd", name)
			continue
		}
		// Behavioural assertion that SetInterspersed(false) is in effect:
		// once the first positional ("edge") is seen, a later "-0.5" must be
		// retained as a positional, not rejected as an unknown shorthand
		// flag. With interspersing ON this Parse would error.
		fs := c.Flags()
		fs.SetOutput(io.Discard)
		if err := fs.Parse([]string{"edge", "a", "b", "-0.5"}); err != nil {
			t.Errorf("verb %q: Parse with a negative value = %v, want nil (interspersing must be off)", name, err)
		}
	}
}

// Drift guard: every top-level verb exposed as a one-liner must be a verb the
// shared REPL grammar actually accepts, so the two surfaces cannot diverge.
func TestGrammarVerbsAcceptedByParser(t *testing.T) {
	for _, verb := range []string{"get", "put", "add", "delete", "scan", "keys"} {
		accepted := false
		for _, v := range parser.Verbs {
			if v == verb {
				accepted = true
				break
			}
		}
		if !accepted {
			t.Errorf("verb %q exposed as a one-liner but not in parser.Verbs", verb)
		}
	}
}

// Forward completeness drift guard: every verb the shared REPL grammar accepts
// (parser.Verbs — the single source of truth) must also be reachable as a
// one-shot one-liner, so the prompt and the shell never diverge. The only
// exceptions are the interactive-session meta verbs help (cobra answers it
// natively) and exit (terminates the REPL loop — meaningless as a one-liner).
func TestEveryREPLVerbIsAvailableAsOneLiner(t *testing.T) {
	replOnly := map[string]bool{"help": true, "exit": true}
	registered := map[string]*cobra.Command{}
	for _, c := range rootCmd.Commands() {
		registered[c.Name()] = c
	}
	for _, verb := range parser.Verbs {
		if replOnly[verb] {
			if verb == "exit" && registered[verb] != nil {
				t.Errorf("REPL-only meta verb %q must not be a top-level one-liner", verb)
			}
			continue
		}
		c, ok := registered[verb]
		if !ok {
			t.Errorf("REPL verb %q has no top-level one-liner command on rootCmd", verb)
			continue
		}
		if c.RunE == nil && c.Run == nil {
			t.Errorf("one-liner command %q is registered but not runnable", verb)
		}
	}
}

// runGrammarLine must print help (and not dial) when invoked with no
// arguments, so `lantern-cli get` with nothing is a friendly no-op rather than a
// connection attempt.
func TestRunGrammarLineEmptyArgsShowsHelp(t *testing.T) {
	c := &cobra.Command{Use: "get"}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := runGrammarLine(c, "get", nil); err != nil {
		t.Errorf("runGrammarLine(empty) = %v, want nil (help shown)", err)
	}
}

func TestFamilyCobraHelpUsesScopedRegistry(t *testing.T) {
	for _, tc := range []struct {
		name string
		cmd  *cobra.Command
	}{
		{"bfs", bfsCmd},
		{"pagerank", pagerankCmd},
		{"community", communityCmd},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, want := range []string{"Signature", "Defaults", "Domains", "Meaning", "Examples"} {
				if !strings.Contains(tc.cmd.Long, want) {
					t.Errorf("%s Cobra help missing %q:\n%s", tc.name, want, tc.cmd.Long)
				}
			}
			if !strings.Contains(tc.cmd.Long, tc.name+" <seed>") {
				t.Errorf("%s Cobra help has wrong signature:\n%s", tc.name, tc.cmd.Long)
			}
		})
	}
}

func TestFamilyCommandsRejectMixedFlagsAndPositionalGrammar(t *testing.T) {
	for _, tc := range []struct {
		name  string
		cmd   *cobra.Command
		flag  string
		value string
		args  []string
	}{
		{name: "Bfs", cmd: bfsCmd, flag: "step", value: "2", args: []string{"alice", "3"}},
		{name: "Pagerank", cmd: pagerankCmd, flag: "top-n", value: "5", args: []string{"alice", "3"}},
		{name: "Community", cmd: communityCmd, flag: "max-size", value: "5", args: []string{"alice", "3"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withChangedFlag(t, tc.cmd, tc.flag, tc.value)
			err := tc.cmd.RunE(tc.cmd, tc.args)
			if err == nil || !strings.Contains(err.Error(), "cannot mix flags with the positional grammar") {
				t.Fatalf("RunE(%v with --%s) = %v, want mixed-grammar error", tc.args, tc.flag, err)
			}
		})
	}
}

func TestFamilyCommandsValidateFlagDomainsBeforeDial(t *testing.T) {
	for _, tc := range []struct {
		name     string
		cmd      *cobra.Command
		flag     string
		value    string
		wantText string
	}{
		{name: "BfsZeroStep", cmd: bfsCmd, flag: "step", value: "0", wantText: "--step must be a positive integer"},
		{name: "BfsZeroFanOut", cmd: bfsCmd, flag: "fan-out", value: "0", wantText: "--fan-out must be a positive integer"},
		{name: "PagerankZeroRestartProb", cmd: pagerankCmd, flag: "restart-prob", value: "0", wantText: "--restart-prob must be a float in (0,1)"},
		{name: "PagerankOneRestartProb", cmd: pagerankCmd, flag: "restart-prob", value: "1", wantText: "--restart-prob must be a float in (0,1)"},
		{name: "PagerankZeroEpsilon", cmd: pagerankCmd, flag: "epsilon", value: "0", wantText: "--epsilon must be a positive float"},
		{name: "CommunityZeroRestartProb", cmd: communityCmd, flag: "restart-prob", value: "0", wantText: "--restart-prob must be a float in (0,1)"},
		{name: "CommunityZeroEpsilon", cmd: communityCmd, flag: "epsilon", value: "0", wantText: "--epsilon must be a positive float"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			withChangedFlag(t, tc.cmd, tc.flag, tc.value)
			err := tc.cmd.RunE(tc.cmd, []string{"alice"})
			if err == nil || !strings.Contains(err.Error(), tc.wantText) {
				t.Fatalf("RunE(alice with --%s=%s) = %v, want %q", tc.flag, tc.value, err, tc.wantText)
			}
		})
	}
}

func withChangedFlag(t *testing.T, cmd *cobra.Command, name, value string) {
	t.Helper()
	flag := cmd.Flags().Lookup(name)
	if flag == nil {
		t.Fatalf("missing flag %q on %s", name, cmd.Name())
	}
	oldValue := flag.Value.String()
	oldChanged := flag.Changed
	if err := cmd.Flags().Set(name, value); err != nil {
		t.Fatalf("set --%s=%s: %v", name, value, err)
	}
	t.Cleanup(func() {
		if err := cmd.Flags().Set(name, oldValue); err != nil {
			t.Fatalf("restore --%s=%s: %v", name, oldValue, err)
		}
		flag.Changed = oldChanged
	})
}
