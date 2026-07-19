package parser

import (
	"strings"
	"testing"
)

// HelpText is consumed by bare `help`; HelpTextFor powers command-scoped REPL
// and Cobra help. The TypeScript port mirrors both contracts.

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

func TestValidate_HelpVerb(t *testing.T) {
	for _, in := range []string{"help", "HELP", "help search", "help bfs", "help PAGERANK", "help community", "  help  "} {
		if err := Validate(in); err != nil {
			t.Errorf("Validate(%q) returned %v; want nil", in, err)
		}
	}
	for _, in := range []string{"help me", "help bfs pagerank"} {
		if err := Validate(in); err == nil {
			t.Errorf("Validate(%q) = nil, want useful topic error", in)
		}
	}
}

func TestHelpParamAndScopedText(t *testing.T) {
	for _, tc := range []struct {
		input string
		topic string
	}{
		{"help", ""},
		{"help search", "search"},
		{"help bfs", "bfs"},
		{"help PAGERANK", "pagerank"},
		{"help community", "community"},
	} {
		t.Run(tc.input, func(t *testing.T) {
			s, err := NewSource(tc.input)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := Verb(s); err != nil {
				t.Fatal(err)
			}
			help, err := HelpParam(s)
			if err != nil {
				t.Fatal(err)
			}
			if help.Topic != tc.topic {
				t.Errorf("topic = %q, want %q", help.Topic, tc.topic)
			}
			text, ok := HelpTextFor(help.Topic)
			if !ok {
				t.Fatal("known topic was not renderable")
			}
			if tc.topic != "" {
				for _, heading := range []string{"Signature", "Defaults", "Domains", "Meaning", "Examples"} {
					if !strings.Contains(text, heading) {
						t.Errorf("scoped help missing %q:\n%s", heading, text)
					}
				}
			}
		})
	}
}

func TestHelpTextFor_SearchDocumentsEveryParameter(t *testing.T) {
	text, ok := HelpTextFor("search")
	if !ok {
		t.Fatal("search help topic was not renderable")
	}
	for _, want := range []string{
		"query:", "limit:", "prefix:", "mode:", "min_should:",
		"phrase:", "fuzziness:", "prefix_terms:", "cursor:", "all:",
		"projection:", "format:", "Compatibility:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("search help missing %q:\n%s", want, text)
		}
	}
}
