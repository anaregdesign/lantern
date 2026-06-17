package cmd

import (
	"io"
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
// arguments, so `lantern get` with nothing is a friendly no-op rather than a
// connection attempt.
func TestRunGrammarLineEmptyArgsShowsHelp(t *testing.T) {
	c := &cobra.Command{Use: "get"}
	c.SetOut(io.Discard)
	c.SetErr(io.Discard)
	if err := runGrammarLine(c, "get", nil); err != nil {
		t.Errorf("runGrammarLine(empty) = %v, want nil (help shown)", err)
	}
}
