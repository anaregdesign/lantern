package cmd

import (
	"reflect"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
)

// TestSearchCommandModelMatchesREPL proves the top-level Cobra flag surface
// produces the same shared model as the REPL grammar. Both paths then call the
// single CLIService.RunSearch request/output implementation (#1068).
func TestSearchCommandModelMatchesREPL(t *testing.T) {
	old := searchCommandModel("")
	t.Cleanup(func() {
		searchLimit = old.Limit
		searchPrefix = old.Prefix
		searchMode = old.Mode
		searchMinShould = old.MinShould
		searchPhrase = old.Phrase
		searchFuzziness = old.Fuzziness
		searchPrefixTerms = old.PrefixTerms
		searchCursor = old.Cursor
		searchAll = old.All
		searchProjection = old.Projection
		searchFormat = old.Format
	})

	searchLimit = 17
	searchPrefix = "利用者/"
	searchMode = "min-should"
	searchMinShould = 2
	searchPhrase = false
	searchFuzziness = 1
	searchPrefixTerms = true
	searchCursor = "AQID"
	searchAll = true
	searchProjection = "full-vertex"
	searchFormat = "ndjson"

	wantSource, err := parser.NewSource(`search "静かな rolling update" limit=17 prefix=利用者/ mode=min-should min_should=2 fuzziness=1 prefix_terms=true cursor=AQID all=true projection=full-vertex format=ndjson`)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parser.Verb(wantSource); err != nil {
		t.Fatal(err)
	}
	want, err := parser.SearchParam(wantSource)
	if err != nil {
		t.Fatal(err)
	}
	got := searchCommandModel("静かな rolling update")
	if !reflect.DeepEqual(got, *want) {
		t.Fatalf("Cobra model = %#v\nREPL model = %#v", got, *want)
	}
}

// TestSearchCommandRegistered guards that search is a top-level command with
// the full structured output/cursor option set.
func TestSearchCommandRegistered(t *testing.T) {
	var found bool
	for _, command := range rootCmd.Commands() {
		if command.Name() != "search" {
			continue
		}
		found = true
		for _, name := range []string{"mode", "phrase", "fuzziness", "prefix-terms", "min-should", "limit", "prefix", "cursor", "all", "projection", "format"} {
			if command.Flags().Lookup(name) == nil {
				t.Errorf("search command missing --%s flag", name)
			}
		}
	}
	if !found {
		t.Fatal("search command not registered on rootCmd")
	}
}

func TestSearchCommandHelpExplainsTheSearchContract(t *testing.T) {
	for _, want := range []string{
		"query:", "limit:", "prefix:", "mode:", "min_should:",
		"phrase:", "fuzziness:", "prefix_terms:", "cursor:", "all:",
		"projection:", "format:", "Compatibility:",
		"https://github.com/anaregdesign/lantern/blob/main/docs/search.md",
	} {
		if !strings.Contains(searchCmd.Long, want) {
			t.Errorf("search command help missing %q:\n%s", want, searchCmd.Long)
		}
	}
}
