package parser

import (
	"strings"
	"testing"
)

// HelpText is consumed by `lantern-cli repl`'s `help` verb (#436). It must
// stay in lockstep with the TS port at admin/app/lib/cli/verbs.ts
// `HELP_TEXT`. These tests guard the documented contract: each family verb
// (bfs / pagerank / community) enumerates its knobs' valid values and
// defaults, and every verb in the dispatch table appears in the text (#975).

func TestHelpText_EnumeratesFamilyKwargs(t *testing.T) {
	for _, kw := range []string{
		"step=", "fan_out=", "top_n=", "max_size=",
		"reduction=", "objective=", "weighting=", "prefix=",
		"restart_prob=", "epsilon=",
	} {
		if !strings.Contains(HelpText, kw) {
			t.Errorf("HelpText missing family kwarg %q", kw)
		}
	}
}

func TestHelpText_EnumeratesFamilyKwargValues(t *testing.T) {
	for _, v := range []string{"none", "mst", "spt", "min", "max", "raw", "tfidf", "bm25"} {
		if !strings.Contains(HelpText, v) {
			t.Errorf("HelpText missing family kwarg value %q", v)
		}
	}
}

func TestHelpText_DocumentsFamilyDefaults(t *testing.T) {
	for _, d := range []string{"default=none", "default=max", "default=raw", "step=5", "fan_out=3", "top_n=10"} {
		if !strings.Contains(HelpText, d) {
			t.Errorf("HelpText missing family default %q", d)
		}
	}
}

func TestHelpText_ListsEveryVerb(t *testing.T) {
	for _, verb := range Verbs {
		if !strings.Contains(HelpText, verb) {
			t.Errorf("HelpText missing verb %q", verb)
		}
	}
}

// Validate accepts the bare `help` verb (and silently tolerates extra
// args, mirroring the TS parser at admin/app/lib/cli/parser.ts).
func TestValidate_HelpVerb(t *testing.T) {
	for _, in := range []string{"help", "HELP", "help me", "  help  "} {
		if err := Validate(in); err != nil {
			t.Errorf("Validate(%q) returned %v; want nil", in, err)
		}
	}
}
