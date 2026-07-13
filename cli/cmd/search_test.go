package cmd

import (
	"encoding/json"
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
		{"", client.MatchServerDefault, false},
		{"server", client.MatchServerDefault, false},
		{"default", client.MatchServerDefault, false},
		{"unset", client.MatchServerDefault, false},
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

func TestParseSearchProjection(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  client.SearchProjection
	}{
		{"key-score", client.SearchProjectionKeyScore},
		{"KEY_SCORE", client.SearchProjectionKeyScore},
		{"full-vertex", client.SearchProjectionFullVertex},
		{"full_vertex", client.SearchProjectionFullVertex},
	} {
		got, err := parseSearchProjection(tc.input)
		if err != nil || got != tc.want {
			t.Errorf("parseSearchProjection(%q) = (%v, %v), want (%v, nil)", tc.input, got, err, tc.want)
		}
	}
	if _, err := parseSearchProjection("everything"); err == nil {
		t.Fatal("parseSearchProjection(everything) = nil error")
	}
}

func TestSearchHitForOutputPreservesProjectedVertex(t *testing.T) {
	projected, err := client.UnmarshalVertexJSON([]byte(`{"key":"doc/1","type":"string","value":"alpha"}`))
	if err != nil {
		t.Fatal(err)
	}
	hit, err := searchHitForOutput(client.SearchHit{
		Key:              "doc/1",
		Score:            3.5,
		Vertex:           projected,
		ProjectionStatus: client.SearchHitProjectionSnapshot,
	})
	if err != nil {
		t.Fatal(err)
	}
	var vertex map[string]any
	if err := json.Unmarshal(hit.Vertex, &vertex); err != nil {
		t.Fatal(err)
	}
	if hit.ProjectionStatus != "snapshot" || vertex["key"] != "doc/1" || vertex["value"] != "alpha" {
		t.Fatalf("output = %#v, vertex = %#v", hit, vertex)
	}
}

func TestSearchFlagOptionsValidateBeforeDial(t *testing.T) {
	tests := []struct {
		name string
		opts []client.SearchOption
	}{
		{"minimum without min mode", []client.SearchOption{client.WithMinShouldMatch(1)}},
		{"phrase with explicit mode", []client.SearchOption{client.WithPhrase(), client.WithMatchMode(client.MatchAny)}},
		{"phrase with fuzzy", []client.SearchOption{client.WithPhrase(), client.WithFuzziness(1)}},
		{"fuzziness outside range", []client.SearchOption{client.WithFuzziness(3)}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := client.ValidateSearchOptions(tc.opts...); !errors.Is(err, client.ErrInvalidArgument) {
				t.Fatalf("ValidateSearchOptions = %v, want ErrInvalidArgument", err)
			}
		})
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
	incomplete := actionableSearchError(errors.Join(client.ErrFailedPrecondition, client.ErrSearchIndexIncomplete))
	if !strings.Contains(incomplete.Error(), "bounded rebuild") {
		t.Errorf("incomplete error is not actionable: %q", incomplete)
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
		for _, name := range []string{"mode", "phrase", "fuzziness", "prefix-terms", "min-should", "limit", "prefix", "cursor", "all", "projection"} {
			if c.Flags().Lookup(name) == nil {
				t.Errorf("search command missing --%s flag", name)
			}
		}
	}
	if !found {
		t.Fatal("search command not registered on rootCmd")
	}
}
