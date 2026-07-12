package cmd

import (
	"errors"
	"strings"
	"testing"

	client "github.com/anaregdesign/lantern/sdks/go"
)

func TestParseSearchMode(t *testing.T) {
	cases := []struct {
		in      string
		want    client.MatchMode
		wantErr bool
	}{
		{"", client.MatchAny, false},
		{"any", client.MatchAny, false},
		{"ANY", client.MatchAny, false},
		{"all", client.MatchAll, false},
		{"min-should", client.MatchMinShould, false},
		{"minshould", client.MatchMinShould, false},
		{"bogus", client.MatchAny, true},
	}
	for _, c := range cases {
		got, err := parseSearchMode(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("parseSearchMode(%q) err = nil, want error", c.in)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseSearchMode(%q) err = %v", c.in, err)
		}
		if got != c.want {
			t.Errorf("parseSearchMode(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestActionableSearchError(t *testing.T) {
	positions := actionableSearchError(errors.Join(client.ErrFailedPrecondition, client.ErrSearchPositionsDisabled))
	for _, want := range []string{"LANTERN_SEARCH_POSITIONS=true", "omit --phrase"} {
		if !strings.Contains(positions.Error(), want) {
			t.Errorf("positions error %q does not contain %q", positions, want)
		}
	}
	disabled := actionableSearchError(errors.Join(client.ErrFailedPrecondition, client.ErrSearchDisabled))
	if !strings.Contains(disabled.Error(), "LANTERN_SEARCH_ENABLED=true") {
		t.Errorf("disabled error is not actionable: %q", disabled)
	}
}

// TestSearchCommandRegistered guards that search is a top-level command with the
// full option flag set (#892).
func TestSearchCommandRegistered(t *testing.T) {
	var found bool
	for _, c := range rootCmd.Commands() {
		if c.Name() != "search" {
			continue
		}
		found = true
		for _, name := range []string{"mode", "phrase", "fuzziness", "prefix-terms", "min-should", "limit", "prefix"} {
			if c.Flags().Lookup(name) == nil {
				t.Errorf("search command missing --%s flag", name)
			}
		}
	}
	if !found {
		t.Fatal("search command not registered on rootCmd")
	}
}
