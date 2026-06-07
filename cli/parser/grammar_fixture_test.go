package parser_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
		Input   string `json:"input"`
		Comment string `json:"comment,omitempty"`
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
