package cmd

import (
	"strings"
	"testing"
)

func TestScopedHelpText_SearchDocumentsEveryParameter(t *testing.T) {
	text := scopedHelpText("search")
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

func TestScopedHelpText_RejectsUnknownTopic(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("scopedHelpText(unknown) did not panic")
		}
	}()
	_ = scopedHelpText("unknown")
}
