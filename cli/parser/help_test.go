package parser

import (
	"strings"
	"testing"
)

// HelpText is consumed by `lantern repl`'s `help` verb (#436). It must
// stay in lockstep with the TS port at admin/app/lib/cli/verbs.ts
// `HELP_TEXT`. These tests guard the documented contract from issue
// #436's acceptance criteria: the illuminate entry must enumerate
// `algorithm`, `objective`, `weighting` valid values and defaults, and
// every verb in the dispatch table must appear in the text.

func TestHelpText_EnumeratesIlluminateKwargs(t *testing.T) {
	for _, kw := range []string{"algorithm=", "objective=", "weighting="} {
		if !strings.Contains(HelpText, kw) {
			t.Errorf("HelpText missing illuminate kwarg %q", kw)
		}
	}
}

func TestHelpText_EnumeratesIlluminateKwargValues(t *testing.T) {
	for _, v := range []string{"none", "mst", "spt", "min", "max", "raw", "tfidf"} {
		if !strings.Contains(HelpText, v) {
			t.Errorf("HelpText missing illuminate kwarg value %q", v)
		}
	}
}

func TestHelpText_DocumentsIlluminateDefaults(t *testing.T) {
	for _, d := range []string{"default=none", "default=min", "default=raw"} {
		if !strings.Contains(HelpText, d) {
			t.Errorf("HelpText missing illuminate default %q", d)
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
