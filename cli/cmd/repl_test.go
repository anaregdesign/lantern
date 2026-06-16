package cmd

import (
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
)

// TestReplBanner guards the startup banner the REPL prints before entering
// its prompt loop (#647). promptui renders only a bare ">" prompt, so this
// banner is the sole cue that tells a first-time user the shell is
// self-describing.
func TestReplBanner(t *testing.T) {
	t.Run("advertises help and exit", func(t *testing.T) {
		for _, want := range []string{"help", "exit"} {
			if !strings.Contains(replBanner, want) {
				t.Errorf("replBanner must mention %q so users can discover it; got:\n%s", want, replBanner)
			}
		}
	})

	t.Run("references the shared grammar", func(t *testing.T) {
		if !strings.Contains(replBanner, "grammar") {
			t.Errorf("replBanner should point at the shared grammar; got:\n%s", replBanner)
		}
	})

	// Bind the banner to reality: a verb is only worth advertising if the
	// parser actually accepts it. This fails loudly if the banner ever
	// drifts to suggest a verb the grammar rejects.
	t.Run("advertised verbs are accepted by the parser", func(t *testing.T) {
		for _, verb := range []string{"help", "exit"} {
			if err := parser.Validate(verb); err != nil {
				t.Errorf("parser.Validate(%q) = %v, want nil (banner advertises it)", verb, err)
			}
		}
	})
}
