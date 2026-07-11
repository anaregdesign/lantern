package parser_test

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
)

// fixture is the shared CLI/REPL grammar drift-detection fixture
// described in #411. The Go-side test reads it at runtime via a
// relative path so changes to the canonical fixture in
// admin/test/cli-grammar/verbs.json are picked up automatically; the
// admin Bun test imports the exact same JSON file from its side. If
// one parser drifts away from the other, both sides go red the next
// time CI runs.
type fixture struct {
	Valid []struct {
		Input    string          `json:"input"`
		Comment  string          `json:"comment,omitempty"`
		Expected json.RawMessage `json:"expected,omitempty"`
	} `json:"valid"`
	Invalid []struct {
		Input   string `json:"input"`
		Comment string `json:"comment,omitempty"`
	} `json:"invalid"`
}

func loadFixture(t *testing.T) fixture {
	t.Helper()
	// Walk up from cli/parser/ to repo root, then into
	// admin/test/cli-grammar/. The Go test binary's cwd is the
	// package directory under `go test`, so the relative path is
	// stable regardless of where `go test ./...` is invoked from.
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	path := filepath.Join(wd, "..", "..", "admin", "test", "cli-grammar", "verbs.json")
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read fixture %s: %v", path, err)
	}
	var fx fixture
	if err := json.Unmarshal(b, &fx); err != nil {
		t.Fatalf("decode fixture: %v", err)
	}
	if len(fx.Valid) == 0 || len(fx.Invalid) == 0 {
		t.Fatalf("fixture must contain both valid and invalid cases; got %d valid / %d invalid", len(fx.Valid), len(fx.Invalid))
	}
	return fx
}

func TestSharedGrammarFixture_Valid(t *testing.T) {
	fx := loadFixture(t)
	for _, tc := range fx.Valid {
		t.Run(tc.Input, func(t *testing.T) {
			if err := parser.Validate(tc.Input); err != nil {
				t.Errorf("Validate(%q) returned error: %v (%s)", tc.Input, err, tc.Comment)
			}
		})
	}
}

func TestSharedGrammarFixture_Invalid(t *testing.T) {
	fx := loadFixture(t)
	for _, tc := range fx.Invalid {
		t.Run(tc.Input, func(t *testing.T) {
			if err := parser.Validate(tc.Input); err == nil {
				t.Errorf("Validate(%q) accepted invalid input (%s)", tc.Input, tc.Comment)
			}
		})
	}
}

func TestSharedGrammarFixture_FamilyNormalizedAST(t *testing.T) {
	fx := loadFixture(t)
	for _, tc := range fx.Valid {
		s, err := parser.NewSource(tc.Input)
		if err != nil {
			t.Fatalf("NewSource(%q): %v", tc.Input, err)
		}
		verb, err := parser.Verb(s)
		if err != nil {
			t.Fatalf("Verb(%q): %v", tc.Input, err)
		}
		if verb != "bfs" && verb != "pagerank" && verb != "community" && verb != "help" {
			continue
		}
		t.Run(tc.Input, func(t *testing.T) {
			if len(tc.Expected) == 0 {
				t.Fatal("family fixture is missing its expected normalized AST")
			}
			got, err := normalizedFamilyAST(tc.Input)
			if err != nil {
				t.Fatalf("normalizedFamilyAST(%q): %v", tc.Input, err)
			}
			var want any
			if err := json.Unmarshal(tc.Expected, &want); err != nil {
				t.Fatalf("decode expected AST: %v", err)
			}
			b, err := json.Marshal(got)
			if err != nil {
				t.Fatalf("encode actual AST: %v", err)
			}
			var actual any
			if err := json.Unmarshal(b, &actual); err != nil {
				t.Fatalf("decode actual AST: %v", err)
			}
			if !reflect.DeepEqual(actual, want) {
				t.Errorf("normalized AST mismatch\n got: %s\nwant: %s", b, tc.Expected)
			}
		})
	}
}

func normalizedFamilyAST(input string) (map[string]any, error) {
	s, err := parser.NewSource(input)
	if err != nil {
		return nil, err
	}
	verb, err := parser.Verb(s)
	if err != nil {
		return nil, err
	}
	switch verb {
	case "help":
		p, err := parser.HelpParam(s)
		if err != nil {
			return nil, err
		}
		return map[string]any{"topic": p.Topic}, nil
	case "bfs":
		p, err := parser.BfsParam(s)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"family": "bfs", "seed": p.Seed, "step": p.Step, "fan_out": p.FanOut,
			"reduction": p.Reduction, "objective": p.Objective, "weighting": p.Weighting, "prefix": p.Prefix,
		}, nil
	case "pagerank":
		p, err := parser.PagerankParam(s)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"family": "pagerank", "seed": p.Seed, "top_n": p.TopN,
			"restart_prob_f32_bits": math.Float32bits(p.RestartProb), "epsilon_f32_bits": math.Float32bits(p.Epsilon),
			"weighting": p.Weighting, "prefix": p.Prefix,
		}, nil
	case "community":
		p, err := parser.CommunityParam(s)
		if err != nil {
			return nil, err
		}
		return map[string]any{
			"family": "community", "seed": p.Seed, "max_size": p.MaxSize,
			"restart_prob_f32_bits": math.Float32bits(p.RestartProb), "epsilon_f32_bits": math.Float32bits(p.Epsilon),
			"reduction": p.Reduction, "objective": p.Objective, "weighting": p.Weighting, "prefix": p.Prefix,
		}, nil
	default:
		return nil, fmt.Errorf("%q is not a family verb", verb)
	}
}
