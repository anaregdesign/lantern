package integration_test

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/anaregdesign/lantern/cli/parser"
)

// TestTraversalDocumentationGate keeps the maintained consumer examples and
// UI copy aligned with the family-verb grammar. The wire RPC and SDK method
// intentionally remain named Illuminate; this gate only rejects retired CLI
// grammar and routes, then parses every documented family command through the
// production parser.
func TestTraversalDocumentationGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	files := map[string]struct {
		required []string
		retired  []string
	}{
		"README.md": {
			required: []string{"bfs: {", "ppr: {", "community: {"},
		},
		"sdks/node/example/main.ts": {
			required: []string{"bfs: {", "ppr: {", "community: {"},
			retired:  []string{"Algorithm", "algorithm:"},
		},
		"sdks/go/example/main.go": {
			required: []string{"client.WithBFS", "client.WithPPR", "client.WithLocalCommunity"},
		},
		"sdks/go/README.md": {
			required: []string{"type Reduction = pb.Reduction"},
			retired:  []string{"type Algorithm = pb.Algorithm"},
		},
		"admin/app/components/cli/CliPage/CliPage.tsx": {
			required: []string{"bfs alice 2 5 reduction=spt"},
			retired:  []string{"illuminate alice 2 5"},
		},
		"admin/README.md": {
			required: []string{"`cli.tsx`", "CLI workspace"},
			retired:  []string{"`illuminate.tsx`"},
		},
		"admin/Caddyfile": {
			required: []string{"/cli, /ops"},
			retired:  []string{"/illuminate"},
		},
		"mcp/examples/README.md": {
			required: []string{"**CLI** (`/cli`)"},
			retired:  []string{"/illuminate"},
		},
		".github/agents/User.agent.md": {
			required: []string{
				"lantern-cli bfs user:alice 1 5",
				"lantern-cli bfs user:alice 2 10 reduction=mst objective=min",
			},
			retired: []string{"lantern-cli illuminate"},
		},
	}

	for path, checks := range files {
		t.Run(path, func(t *testing.T) {
			contents, readErr := os.ReadFile(filepath.Join(repoRoot, path))
			if readErr != nil {
				t.Fatalf("read %s: %v", path, readErr)
			}
			text := string(contents)
			for _, want := range checks.required {
				if !strings.Contains(text, want) {
					t.Errorf("%s is missing maintained example/copy %q", path, want)
				}
			}
			for _, retired := range checks.retired {
				if strings.Contains(text, retired) {
					t.Errorf("%s still contains retired traversal grammar/copy %q", path, retired)
				}
			}
		})
	}

	for _, example := range []string{
		"bfs user:42 3 8 reduction=spt objective=max",
		"pagerank user:42 10 restart_prob=0.15 epsilon=0.0001",
		"community user:42 30 reduction=mst objective=min",
		"bfs alice 2 5 reduction=spt",
		"lantern-cli bfs user:alice 1 5",
		"lantern-cli bfs user:alice 2 10 reduction=mst objective=min",
	} {
		t.Run(example, func(t *testing.T) {
			parseTraversalDocumentationCommand(t, example)
		})
	}
}

// TestDartFirstReleaseBlockerGate keeps the executable v0.1 release gate
// aligned with the documented dependency spine. Release-tracking Issues must
// remain open until publishing is verified, and Phase 2 design work must not
// silently become a v0.1 blocker.
func TestDartFirstReleaseBlockerGate(t *testing.T) {
	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}

	workflowPath := filepath.Join(repoRoot, ".github", "workflows", "dart-sdk.yml")
	workflow, err := os.ReadFile(workflowPath)
	if err != nil {
		t.Fatalf("read Dart SDK workflow: %v", err)
	}

	match := regexp.MustCompile(`for issue in ([0-9 ]+); do`).FindStringSubmatch(string(workflow))
	if len(match) != 2 {
		t.Fatal("Dart SDK workflow is missing the first-release Issue gate")
	}
	const wantBlockers = "1008 1009 1011 1014 1016 1017"
	if got := strings.Join(strings.Fields(match[1]), " "); got != wantBlockers {
		t.Fatalf("Dart v0.1 blockers = %q, want %q", got, wantBlockers)
	}

	contributing, err := os.ReadFile(filepath.Join(repoRoot, "CONTRIBUTING.md"))
	if err != nil {
		t.Fatalf("read CONTRIBUTING.md: %v", err)
	}
	for _, contract := range []string{
		"Release-tracking Issues #1020 and #1022 remain open",
		"#1021 is not a v0.1 release blocker",
	} {
		if !strings.Contains(string(contributing), contract) {
			t.Errorf("CONTRIBUTING.md is missing Dart release contract %q", contract)
		}
	}
}

func parseTraversalDocumentationCommand(t *testing.T, command string) {
	t.Helper()
	command = strings.TrimPrefix(command, "lantern-cli ")
	source, err := parser.NewSource(command)
	if err != nil {
		t.Fatalf("tokenise %q: %v", command, err)
	}
	verb, err := parser.Verb(source)
	if err != nil {
		t.Fatalf("parse verb in %q: %v", command, err)
	}
	switch verb {
	case "bfs":
		_, err = parser.BfsParam(source)
	case "pagerank":
		_, err = parser.PagerankParam(source)
	case "community":
		_, err = parser.CommunityParam(source)
	default:
		t.Fatalf("unexpected traversal verb %q", verb)
	}
	if err != nil {
		t.Fatalf("parse %q: %v", command, err)
	}
	if err := parser.EOF(source); err != nil {
		t.Fatalf("trailing token in %q: %v", command, err)
	}
}
